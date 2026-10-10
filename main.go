// jabberwock is a tiny terminal YouTube search client. It uses only the Go
// standard library; mpv (and usually yt-dlp) is used for playback.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

const (
	viewVideos = iota
	viewFilters
	viewSettings
)

var viewNames = []string{"Home", "Filters", "Settings"}

type App struct {
	cfg        Config
	cfgPath    string
	filters    Filters
	query      string
	videos     []Video
	selected   int
	page       int
	cursor     SearchCursor
	hasMore    bool
	view       int
	filterRow  int
	settingRow int
	inputMode  string // "search" or "instance"
	input      []rune
	notice     string
	fatalErr   error
	quit       bool
	term       terminal
}

type terminal struct {
	fd       int
	original syscall.Termios
	active   bool
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		printHelp()
		return
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		fmt.Fprintln(os.Stderr, "jabberwock needs an interactive terminal. Run it directly in your terminal (not through a pipe).")
		os.Exit(2)
	}

	cfg, cfgPath, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jabberwock:", err)
		os.Exit(1)
	}
	app := &App{cfg: cfg, cfgPath: cfgPath, filters: DefaultFilters(), view: viewVideos, notice: "Tab switches Home / Filters / Settings; arrow keys navigate the current section."}
	app.term = terminal{fd: int(os.Stdin.Fd())}
	if err := app.term.enable(); err != nil {
		fmt.Fprintln(os.Stderr, "jabberwock: cannot enter terminal mode:", err)
		os.Exit(1)
	}
	defer app.term.restore()
	defer fmt.Print("\x1b[0m\x1b[?25h\x1b[2J\x1b[H")

	for !app.quit {
		app.render()
		key := readKey()
		app.handleKey(key)
	}
}

func printHelp() {
	fmt.Println(`jabberwock — tiny terminal YouTube client

Run: go run .
Build: go build -o jabberwock .

Requires an interactive terminal. Playback is delegated to mpv; for YouTube
URLs, mpv generally needs yt-dlp installed as well. No Go modules are required.
Settings: ~/.config/jabberwock/config.json (or $JABBERWOCK_CONFIG).

The local backend scrapes YouTube search HTML. The Invidious backend uses its
public /api/v1/search endpoint. No history or subscriptions are stored.`)
}

func isTerminal(file *os.File) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

func (t *terminal) enable() error {
	if t.active {
		return nil
	}
	var original syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(t.fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&original)))
	if errno != 0 {
		return errno
	}
	raw := original
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = 1 // a 100ms timeout also lets us recognize a lone Esc
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(t.fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&raw)))
	if errno != 0 {
		return errno
	}
	t.original = original
	t.active = true
	fmt.Print("\x1b[?1049h\x1b[?25l")
	return nil
}

func (t *terminal) restore() {
	if !t.active {
		return
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(t.fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&t.original)))
	t.active = false
	fmt.Print("\x1b[0m\x1b[?25h\x1b[?1049l")
}

func readKey() string {
	var first [1]byte
	for {
		n, err := os.Stdin.Read(first[:])
		if err != nil {
			return ""
		}
		if n == 1 {
			break
		}
	}
	b := first[0]
	switch b {
	case 0x13:
		return "ctrl+s"
	case 0x11:
		return "ctrl+q"
	case 0x03:
		return "ctrl+c"
	case 0x7f, 0x08:
		return "backspace"
	case '\r', '\n':
		return "enter"
	case '\t':
		return "tab"
	case 0x1b:
		var seq [2]byte
		n, _ := os.Stdin.Read(seq[:1])
		if n == 0 {
			return "esc"
		}
		if seq[0] != '[' && seq[0] != 'O' {
			return "esc"
		}
		n, _ = os.Stdin.Read(seq[1:])
		if n == 0 {
			return "esc"
		}
		switch seq[1] {
		case 'A':
			return "up"
		case 'B':
			return "down"
		case 'C':
			return "right"
		case 'D':
			return "left"
		case 'H':
			return "home"
		case 'F':
			return "end"
		default:
			return "esc"
		}
	default:
		if b < 0x20 {
			return fmt.Sprintf("ctrl+%c", b+'a'-1)
		}
		need := 1
		switch {
		case b&0xE0 == 0xC0:
			need = 2
		case b&0xF0 == 0xE0:
			need = 3
		case b&0xF8 == 0xF0:
			need = 4
		}
		buf := []byte{b}
		for len(buf) < need {
			var next [1]byte
			n, _ := os.Stdin.Read(next[:])
			if n == 0 {
				break
			}
			buf = append(buf, next[0])
		}
		r, _ := utf8.DecodeRune(buf)
		if r == utf8.RuneError && len(buf) == 1 {
			return "�"
		}
		return string(r)
	}
}

func (a *App) handleKey(key string) {
	if key == "" {
		return
	}
	if a.inputMode != "" {
		a.handleInputKey(key)
		return
	}
	binds := a.cfg.effectiveKeybinds()
	if action, ok := binds[key]; ok {
		a.dispatchAction(action)
		return
	}
	switch a.view {
	case viewVideos:
		switch key {
		case "up", "k":
			a.moveSelection(-1)
		case "down", "j":
			a.moveSelection(1)
		case "enter":
			a.playSelected()
		case "home":
			a.selected = 0
		case "end":
			if a.hasMore {
				a.selected = len(a.videos)
			} else if len(a.videos) > 0 {
				a.selected = len(a.videos) - 1
			}
		}
	case viewFilters:
		switch key {
		case "up", "k":
			a.filterRow = (a.filterRow + len(filterOptions) - 1) % len(filterOptions)
		case "down", "j":
			a.filterRow = (a.filterRow + 1) % len(filterOptions)
		case "left":
			a.cycleFilter(-1)
		case "right", "enter":
			a.cycleFilter(1)
		}
	case viewSettings:
		switch key {
		case "up", "k":
			a.settingRow = (a.settingRow + 2) % 3
		case "down", "j":
			a.settingRow = (a.settingRow + 1) % 3
		case "left":
			a.cycleSetting(-1)
		case "right", "enter":
			a.cycleSetting(1)
		}
	}
}

func (a *App) dispatchAction(action string) {
	switch action {
	case "search":
		a.beginInput("search")
	case "quit":
		a.quit = true
	case "next_view":
		a.view = (a.view + 1) % len(viewNames)
	case "refresh":
		if strings.TrimSpace(a.query) == "" {
			a.notice = "Search first with Ctrl+S."
		} else {
			a.performSearch()
		}
	case "top":
		a.selected = 0
	case "bottom":
		if a.hasMore {
			a.selected = len(a.videos)
		} else if len(a.videos) > 0 {
			a.selected = len(a.videos) - 1
		}
	default:
		if handler, ok := registeredActions[action]; ok {
			handler(a)
		} else {
			a.notice = "Unknown key action: " + action
		}
	}
}

func (a *App) beginInput(mode string) {
	a.inputMode = mode
	a.input = nil
	if mode == "instance" {
		a.input = []rune(a.cfg.InvidiousURL)
	}
	if mode == "search" {
		a.notice = "Type a query and press Enter; Esc cancels."
	} else {
		a.notice = "Edit the Invidious base URL, then press Enter."
	}
}

func (a *App) handleInputKey(key string) {
	switch key {
	case "esc":
		a.inputMode = ""
		a.notice = "Input cancelled."
	case "enter":
		value := strings.TrimSpace(string(a.input))
		mode := a.inputMode
		a.inputMode = ""
		if mode == "search" {
			if value == "" {
				a.notice = "Search cancelled: query was empty."
				return
			}
			a.query = value
			a.view = viewVideos
			a.selected = 0
			a.performSearch()
		} else if mode == "instance" {
			if !strings.HasPrefix(value, "https://") && !strings.HasPrefix(value, "http://") {
				a.notice = "URL must start with https:// or http://. Setting was not changed."
				return
			}
			a.cfg.InvidiousURL = strings.TrimRight(value, "/")
			a.saveConfig("Invidious URL updated.")
		}
	case "backspace":
		if len(a.input) > 0 {
			a.input = a.input[:len(a.input)-1]
		}
	case "ctrl+u":
		a.input = nil
	default:
		if len([]rune(key)) == 1 {
			r := []rune(key)[0]
			if !unicode.IsControl(r) {
				a.input = append(a.input, r)
			}
		}
	}
}

func (a *App) moveSelection(delta int) {
	last := len(a.videos) - 1
	if a.hasMore {
		last = len(a.videos) // the final selectable row is Next page
	}
	if last < 0 {
		if a.hasMore {
			a.selected = 0
		}
		return
	}
	a.selected += delta
	if a.selected < 0 {
		a.selected = 0
	}
	if a.selected > last {
		a.selected = last
	}
}

func (a *App) cycleFilter(direction int) {
	if direction > 0 {
		a.filters.cycle(a.filterRow)
	} else {
		option := filterOptions[a.filterRow]
		current := a.filters.valueAt(a.filterRow)
		pos := 0
		for i, value := range option.Values {
			if value == current {
				pos = i
				break
			}
		}
		previous := option.Values[(pos+len(option.Values)-1)%len(option.Values)]
		switch a.filterRow {
		case 0:
			a.filters.Sort = previous
		case 1:
			a.filters.Date = previous
		case 2:
			a.filters.Duration = previous
		case 3:
			a.filters.HideShorts = previous == "on"
		}
	}
	if strings.TrimSpace(a.query) != "" {
		a.performSearch()
	} else {
		a.notice = "Filters updated. Search a query to apply them."
	}
}

func (a *App) cycleSetting(direction int) {
	switch a.settingRow {
	case 0:
		if a.cfg.Backend == "local" {
			a.cfg.Backend = "invidious"
		} else {
			a.cfg.Backend = "local"
		}
		a.saveConfig("Backend changed to " + a.cfg.Backend + ".")
		if a.query != "" {
			a.performSearch()
		}
	case 1:
		if direction < 0 || direction > 0 {
			a.beginInput("instance")
		}
	case 2:
		names := a.cfg.themeNames()
		if len(names) == 0 {
			return
		}
		pos := 0
		for i, name := range names {
			if name == a.cfg.Theme {
				pos = i
				break
			}
		}
		if direction >= 0 {
			pos = (pos + 1) % len(names)
		} else {
			pos = (pos + len(names) - 1) % len(names)
		}
		a.cfg.Theme = names[pos]
		a.saveConfig("Theme changed to " + a.cfg.Theme + ".")
	}
}

func (a *App) saveConfig(success string) {
	if err := SaveConfig(a.cfgPath, a.cfg); err != nil {
		a.notice = "Could not save settings: " + err.Error()
		return
	}
	a.notice = success
}

func (a *App) performSearch() {
	if strings.TrimSpace(a.query) == "" {
		a.notice = "Search first with Ctrl+S."
		return
	}
	a.videos = nil
	a.selected = 0
	a.page = 1
	a.cursor = SearchCursor{}
	a.hasMore = false
	a.fetchSearchPage(false)
}

func (a *App) loadNextPage() {
	if !a.hasMore {
		a.notice = "You've reached the end of the search results."
		return
	}
	a.fetchSearchPage(true)
}

func (a *App) fetchSearchPage(next bool) {
	page := 1
	cursor := SearchCursor{}
	if next {
		page = a.page + 1
		cursor = a.cursor
		a.notice = fmt.Sprintf("Loading search page %d…", page)
	} else {
		a.notice = "Searching…"
	}
	a.render()
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	result, err := SearchVideosPage(ctx, a.cfg, a.query, a.filters, page, cursor)
	if err != nil {
		a.notice = "Search failed: " + err.Error()
		if !next {
			a.videos = nil
			a.selected = 0
			a.hasMore = false
			a.cursor = SearchCursor{}
		}
		return
	}

	if next {
		previousCount := len(a.videos)
		a.videos = appendUniqueVideos(a.videos, result.Videos)
		a.videos = FilterAndSort(a.videos, a.filters)
		a.page = page
		a.cursor = result.Cursor
		a.hasMore = result.HasMore
		if len(a.videos) > previousCount {
			a.selected = min(previousCount, len(a.videos)-1)
			newCount := len(a.videos) - previousCount
			a.notice = fmt.Sprintf("Loaded %d more videos (page %d).", newCount, page)
		} else if a.hasMore {
			a.selected = len(a.videos) // keep the Next page row selected
			a.notice = "No new videos matched on this page. Press Enter on Next page to continue."
		} else {
			a.selected = max(0, len(a.videos)-1)
			a.notice = "You've reached the end of the search results."
		}
		return
	}

	a.page = page
	a.videos = result.Videos
	a.cursor = result.Cursor
	a.hasMore = result.HasMore
	a.selected = 0
	if len(a.videos) == 0 {
		a.notice = "No results matched those filters. Try broader filters."
		return
	}
	a.notice = fmt.Sprintf("Found %d videos on page 1.%s", len(a.videos), nextPageNotice(a.hasMore))
}

func nextPageNotice(hasMore bool) string {
	if hasMore {
		return " Select Next page at the bottom for more."
	}
	return ""
}

func appendUniqueVideos(existing, incoming []Video) []Video {
	out := make([]Video, 0, len(existing)+len(incoming))
	seen := make(map[string]bool, len(existing)+len(incoming))
	for _, video := range existing {
		if video.VideoID == "" || seen[video.VideoID] {
			continue
		}
		seen[video.VideoID] = true
		out = append(out, video)
	}
	for _, video := range incoming {
		if video.VideoID == "" || seen[video.VideoID] {
			continue
		}
		seen[video.VideoID] = true
		out = append(out, video)
	}
	return out
}

func (a *App) playSelected() {
	if a.hasMore && a.selected >= len(a.videos) {
		a.loadNextPage()
		return
	}
	if len(a.videos) == 0 || a.selected < 0 || a.selected >= len(a.videos) {
		a.notice = "No video selected. Search with Ctrl+S first."
		return
	}
	video := a.videos[a.selected]
	if _, err := exec.LookPath("mpv"); err != nil {
		a.notice = "mpv not found. Install mpv to watch videos."
		return
	}
	a.term.restore()
	fmt.Print("\x1b[0m\x1b[?25h\x1b[2J\x1b[H")
	fmt.Printf("jabberwock: launching mpv for %s\r\n", video.Title)
	cmd := exec.Command("mpv", "--", video.WatchURL)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	fmt.Print("\r\nReturning to jabberwock…\r\n")
	if enableErr := a.term.enable(); enableErr != nil {
		a.quit = true
		a.fatalErr = enableErr
		return
	}
	if err != nil {
		a.notice = "mpv exited: " + err.Error()
	} else {
		a.notice = "Playback finished."
	}
}

func (a *App) render() {
	width, height := terminalSize()
	if width < 48 {
		width = 48
	}
	if height < 12 {
		height = 12
	}
	theme := a.cfg.selectedTheme()
	reset := "\x1b[0m"
	paint := func(code, text string) string {
		if code == "" {
			return text
		}
		return "\x1b[" + code + "m" + text + reset
	}
	clip := func(s string) string {
		r := []rune(s)
		if len(r) > width-2 {
			return string(r[:max(0, width-5)]) + "…"
		}
		return s
	}
	lines := make([]string, 0, height)
	backendLabel := "LOCAL SCRAPE"
	if a.cfg.Backend == "invidious" {
		backendLabel = "INVIDIOUS API"
	}
	lines = append(lines, "  "+paint(theme.Title, "JABBERWOCK")+"  "+paint(theme.Muted, "minimal YouTube TUI")+"  "+paint(theme.Accent, backendLabel))
	var tabs []string
	for i, name := range viewNames {
		if i == a.view {
			tabs = append(tabs, paint(theme.Selected, " "+name+" "))
		} else {
			tabs = append(tabs, paint(theme.Muted, " "+name+" "))
		}
	}
	lines = append(lines, "  "+strings.Join(tabs, "  "))
	lines = append(lines, paint(theme.Border, strings.Repeat("─", width-2)))

	switch a.view {
	case viewVideos:
		if a.query == "" {
			lines = append(lines, "  Welcome to jabberwock — a lightweight YouTube search and player.")
			lines = append(lines, "  TAB switches sections: Home → Filters → Settings (then wraps around).")
			lines = append(lines, "  ↑/↓ move through results or rows; ←/→ change the selected filter or setting.")
			lines = append(lines, "  Ctrl+S searches; Enter plays a video in mpv. At the end, select Next page.")
			lines = append(lines, "  No watch history or local subscriptions are stored.")
		} else {
			lines = append(lines, "  Query: "+clip(a.query))
			lines = append(lines, "  Filters: sort="+a.filters.Sort+"  uploaded="+a.filters.Date+"  duration="+a.filters.Duration+"  hide-shorts="+onOff(a.filters.HideShorts))
			lines = append(lines, "")
			available := height - 10
			if available < 1 {
				available = 1
			}
			listCount := len(a.videos)
			if a.hasMore {
				listCount++ // one additional, selectable Next page row
			}
			start := 0
			if a.selected >= available {
				start = a.selected - available + 1
			}
			end := min(listCount, start+available)
			if len(a.videos) == 0 && !a.hasMore {
				lines = append(lines, paint(theme.Muted, "  No videos to display. Press Ctrl+S to search or visit Filters."))
			} else {
				for i := start; i < end; i++ {
					marker := "  "
					if i == a.selected {
						marker = "› "
					}
					if i == len(a.videos) && a.hasMore {
						row := clip("  " + marker + "Next page  —  load more search results")
						if i == a.selected {
							lines = append(lines, paint(theme.Selected, row))
						} else {
							lines = append(lines, paint(theme.Accent, row))
						}
						continue
					}
					video := a.videos[i]
					meta := fmt.Sprintf("[%s] %s", formatDuration(video), video.Title)
					if video.Author != "" {
						meta += "  —  " + video.Author
					}
					if video.ViewsKnown {
						meta += "  ·  " + compactCount(video.Views) + " views"
					}
					if video.PublishedText != "" {
						meta += "  ·  " + video.PublishedText
					}
					row := clip("  " + marker + meta)
					if i == a.selected {
						lines = append(lines, paint(theme.Selected, row))
					} else {
						lines = append(lines, paint(theme.Text, row))
					}
				}
			}
		}
	case viewFilters:
		lines = append(lines, "  Select a row with ↑/↓. Press Enter (or →) to cycle; ← cycles backwards.")
		lines = append(lines, "")
		for i, option := range filterOptions {
			row := fmt.Sprintf("  %-14s  %s", option.Name, a.filters.labelAt(i))
			if i == a.filterRow {
				lines = append(lines, paint(theme.Selected, clip("› "+row)))
			} else {
				lines = append(lines, paint(theme.Text, clip("  "+row)))
			}
		}
		lines = append(lines, "")
		lines = append(lines, paint(theme.Muted, "  No Shorts: Shorts-marked results are hidden. Older Invidious instances may not label every Short."))
	case viewSettings:
		lines = append(lines, "  Settings are saved to: "+clip(a.cfgPath))
		lines = append(lines, "  Select with ↑/↓; Enter or → changes the selected setting.")
		lines = append(lines, "")
		backend := "Local YouTube HTML scrape"
		if a.cfg.Backend == "invidious" {
			backend = "Invidious API"
		}
		settings := []string{
			"Backend           " + backend,
			"Invidious base URL " + a.cfg.InvidiousURL,
			"Theme             " + a.cfg.Theme,
		}
		for i, setting := range settings {
			row := clip("  " + setting)
			if i == a.settingRow {
				lines = append(lines, paint(theme.Selected, "› "+row))
			} else {
				lines = append(lines, paint(theme.Text, "  "+row))
			}
		}
		lines = append(lines, "")
		lines = append(lines, paint(theme.Muted, "  Themes: add ANSI SGR palette entries in config.json, or call RegisterTheme in config.go."))
		lines = append(lines, paint(theme.Muted, "  Keybinds: key -> action in config.json; Go forks can add custom RegisterAction callbacks."))
	}

	for len(lines) < height-3 {
		lines = append(lines, "")
	}
	lines = append(lines, paint(theme.Border, strings.Repeat("─", width-2)))
	if a.inputMode != "" {
		prompt := "Search> "
		if a.inputMode == "instance" {
			prompt = "Invidious URL> "
		}
		lines = append(lines, paint(theme.Accent, "  "+prompt)+clip(string(a.input))+"█")
	} else {
		lines = append(lines, paint(theme.Muted, "  Tab: sections  ↑/↓: items  ←/→: options  Ctrl+S: search  Enter: select/play  Ctrl+Q: quit"))
	}
	statusCode := theme.Muted
	if strings.HasPrefix(a.notice, "Search failed:") || strings.HasPrefix(a.notice, "Could not") || strings.Contains(a.notice, "not found") {
		statusCode = theme.Error
	}
	status := "  " + a.notice
	if a.fatalErr != nil {
		status = "  Error: " + a.fatalErr.Error()
	}
	lines = append(lines, paint(statusCode, clip(status)))
	if len(lines) > height {
		lines = lines[:height]
	}
	fmt.Print("\x1b[?25l\x1b[H\x1b[2J")
	for _, line := range lines {
		fmt.Print(line, "\r\n")
	}
	if a.inputMode != "" {
		fmt.Print("\x1b[?25h")
	}
}

func terminalSize() (int, int) {
	var size struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	if errno != 0 || size.Col == 0 || size.Row == 0 {
		return 80, 24
	}
	return int(size.Col), int(size.Row)
}

func formatDuration(video Video) string {
	if !video.DurationKnown {
		return "LIVE"
	}
	seconds := video.DurationSeconds
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

func compactCount(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprint(n)
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

// Avoid a shadowed future toolchain function if building with Go versions before 1.21.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

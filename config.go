package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Theme colors are ANSI SGR parameter strings. Examples: "36", "1;35", "38;5;110".
// Add themes in config.json or register them from Go code with RegisterTheme.
type Theme struct {
	Name     string `json:"name,omitempty"`
	Title    string `json:"title"`
	Accent   string `json:"accent"`
	Text     string `json:"text"`
	Muted    string `json:"muted"`
	Selected string `json:"selected"`
	Error    string `json:"error"`
	Border   string `json:"border"`
}

type Config struct {
	Backend            string            `json:"backend"` // "local" or "invidious"
	InvidiousURL       string            `json:"invidious_url"`
	SoundCloudClientID string            `json:"soundcloud_client_id,omitempty"`
	Theme              string            `json:"theme"`
	SoundCloudTheme    string            `json:"soundcloud_theme,omitempty"`
	Themes             map[string]Theme  `json:"themes,omitempty"`
	Keybinds           map[string]string `json:"keybinds,omitempty"` // key -> action
}

type ActionHandler func(*App)

var registeredThemes = map[string]Theme{}
var registeredKeybinds = map[string]string{}
var registeredActions = map[string]ActionHandler{}

// RegisterTheme makes a theme available to the running client. Call from init() in
// a locally modified build. The same theme can also be authored in config.json.
func RegisterTheme(theme Theme) {
	if strings.TrimSpace(theme.Name) == "" {
		return
	}
	registeredThemes[theme.Name] = theme
}

// RegisterKeybind adds a key -> action mapping to the built-in config. It is useful
// for small forks that want extra bindings without editing the TUI event loop.
func RegisterKeybind(key, action string) {
	key = normalizeKeyName(key)
	if key != "" && strings.TrimSpace(action) != "" {
		registeredKeybinds[key] = strings.TrimSpace(action)
	}
}

// RegisterAction exposes a Go callback to RegisterKeybind and config-file mappings.
// Custom actions are deliberately code-defined; config files cannot execute code.
func RegisterAction(name string, handler ActionHandler) {
	if strings.TrimSpace(name) != "" && handler != nil {
		registeredActions[strings.TrimSpace(name)] = handler
	}
}

func init() {
	RegisterTheme(Theme{Name: "wock", Title: "1;36", Accent: "36", Text: "37", Muted: "90", Selected: "1;30;46", Error: "1;31", Border: "34"})
	RegisterTheme(Theme{Name: "paper", Title: "1;34", Accent: "35", Text: "30", Muted: "90", Selected: "1;37;44", Error: "1;31", Border: "94"})
	RegisterTheme(Theme{Name: "moss", Title: "1;32", Accent: "32", Text: "37", Muted: "90", Selected: "1;30;42", Error: "1;31", Border: "32"})
	RegisterTheme(Theme{Name: "soundcloud", Title: "1;38;5;208", Accent: "38;5;208", Text: "37", Muted: "90", Selected: "1;30;48;5;208", Error: "1;31", Border: "38;5;166"})

	RegisterKeybind("ctrl+s", "search")
	RegisterKeybind("ctrl+q", "quit")
	RegisterKeybind("ctrl+c", "quit")
	RegisterKeybind("ctrl+p", "switch_source")
	RegisterKeybind("tab", "next_view")
	RegisterKeybind("/", "search")
	RegisterKeybind("r", "refresh")
	RegisterKeybind("g", "top")
	RegisterKeybind("G", "bottom")
}

func DefaultConfig() Config {
	return Config{
		Backend:         "local",
		InvidiousURL:    "https://yewtu.be",
		Theme:           "wock",
		SoundCloudTheme: "soundcloud",
		Themes:          map[string]Theme{},
		Keybinds:        map[string]string{},
	}
}

func configPath() string {
	if p := strings.TrimSpace(os.Getenv("JABBERWOCK_CONFIG")); p != "" {
		return p
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return filepath.Join(".", "jabberwock.json")
	}
	return filepath.Join(base, "jabberwock", "config.json")
}

func LoadConfig() (Config, string, error) {
	cfg := DefaultConfig()
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := SaveConfig(path, cfg); err != nil {
				return cfg, path, fmt.Errorf("create config: %w", err)
			}
			return cfg, path, nil
		}
		return cfg, path, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Backend != "invidious" {
		cfg.Backend = "local"
	}
	if strings.TrimSpace(cfg.InvidiousURL) == "" {
		cfg.InvidiousURL = DefaultConfig().InvidiousURL
	}
	cfg.InvidiousURL = strings.TrimRight(strings.TrimSpace(cfg.InvidiousURL), "/")
	if cfg.SoundCloudClientID == "" {
		cfg.SoundCloudClientID = strings.TrimSpace(os.Getenv("JABBERWOCK_SC_CLIENT_ID"))
	}
	if strings.TrimSpace(cfg.SoundCloudTheme) == "" {
		cfg.SoundCloudTheme = "soundcloud"
	}
	if cfg.Themes == nil {
		cfg.Themes = map[string]Theme{}
	}
	if cfg.Keybinds == nil {
		cfg.Keybinds = map[string]string{}
	}
	return cfg, path, nil
}

func SaveConfig(path string, cfg Config) error {
	if path == "" {
		path = configPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (cfg Config) themeNames() []string {
	seen := map[string]bool{}
	for name := range registeredThemes {
		seen[name] = true
	}
	for name := range cfg.Themes {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (cfg Config) selectedTheme() Theme {
	if t, ok := cfg.Themes[cfg.Theme]; ok {
		if t.Name == "" {
			t.Name = cfg.Theme
		}
		return t
	}
	if t, ok := registeredThemes[cfg.Theme]; ok {
		return t
	}
	return registeredThemes["wock"]
}

// selectedThemeFor keeps separate theme preferences for YouTube and SoundCloud.
// Existing `theme` settings continue to control YouTube; SoundCloud defaults to
// its orange-accent theme but remains customizable through the Settings screen.
func (cfg Config) selectedThemeFor(provider string) Theme {
	name := cfg.Theme
	if provider == "soundcloud" {
		name = cfg.SoundCloudTheme
	}
	if t, ok := cfg.Themes[name]; ok {
		if t.Name == "" {
			t.Name = name
		}
		return t
	}
	if t, ok := registeredThemes[name]; ok {
		return t
	}
	return cfg.selectedTheme()
}

func (cfg Config) effectiveKeybinds() map[string]string {
	out := make(map[string]string, len(registeredKeybinds)+len(cfg.Keybinds))
	for key, action := range registeredKeybinds {
		out[normalizeKeyName(key)] = action
	}
	for key, action := range cfg.Keybinds {
		out[normalizeKeyName(key)] = action
	}
	return out
}

// normalizeKeyName intentionally preserves letter case so `g` and `G` can differ.
func normalizeKeyName(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "Control+", "ctrl+")
	key = strings.ReplaceAll(key, "Ctrl+", "ctrl+")
	key = strings.ReplaceAll(key, "CTRL+", "ctrl+")
	return key
}

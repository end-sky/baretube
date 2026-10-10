# jabberwock

A deliberately small, native terminal YouTube search client written in Go. The app has no third-party Go dependencies and keeps no watch history or local subscription list. Search results live only in memory; the only file it writes is its settings file.

## Build and run

```sh
go build -o jabberwock .
./jabberwock
```

Or run it without creating a binary:

```sh
go run .
```

On the Home screen, the navigation rule is shown explicitly: use `Tab` to switch between Home, Filters, and Settings; use the arrow keys to move among results and rows inside the current section. `jabberwock` expects an interactive Linux terminal. Watching a result launches `mpv` with the video's YouTube URL. To play YouTube URLs, `mpv` generally also needs `yt-dlp` installed and available on `PATH`; those are runtime tools, not Go libraries.

## Controls

| Key | Action |
| --- | --- |
| `Ctrl+S` or `/` | Search |
| `Tab` | Switch section: Home → Filters → Settings (wraps around) |
| `↑` / `↓` | Move through search results or the current filter/setting rows |
| `←` / `→` | Change the selected filter or setting |
| `Enter` | Play the selected video, load **Next page**, or apply the selected option |
| `r` | Refresh the current search |
| `g` / `G` | Jump to first / last result |
| `Ctrl+Q` / `Ctrl+C` | Quit |
| `Esc` | Cancel an input prompt |

Search results are paginated. When more results are available, a selectable **Next page** row appears after the current results; move to it with `↓` and press `Enter` to append more videos to the same list. Pagination uses YouTube continuation tokens in local mode and the `page` parameter in Invidious mode. Filters include relevance/views/date sorting, upload date, duration, and a default-on **Hide Shorts** switch. YouTube's HTML does not have a stable public scraping contract, so the local backend may need adjustments if YouTube changes its markup. The Invidious backend depends on the configured instance's availability; neither backend can reliably identify every Short when the upstream result omits that metadata.

## Settings and customization

By default, settings are stored in `~/.config/jabberwock/config.json`. Override that path with `JABBERWOCK_CONFIG=/path/to/config.json`.

Example config:

```json
{
  "backend": "local",
  "invidious_url": "https://yewtu.be",
  "theme": "wock",
  "themes": {
    "midnight": {
      "name": "midnight",
      "title": "1;35",
      "accent": "36",
      "text": "37",
      "muted": "90",
      "selected": "1;30;45",
      "error": "1;31",
      "border": "35"
    }
  },
  "keybinds": {
    "ctrl+r": "refresh",
    "home": "top"
  }
}
```

Theme values are ANSI SGR parameter strings (e.g. `36`, `1;35`, `38;5;110`). Add a theme under `themes`, then choose its name in **Settings → Theme**. `keybinds` maps keys to built-in action names: `search`, `quit`, `next_view`, `refresh`, `top`, or `bottom`. Built-in Go forks can expose extra behavior by calling `RegisterAction("action_name", func(app *App) { ... })` and bind it with `RegisterKeybind("ctrl+x", "action_name")`. Themes can likewise be registered in code using `RegisterTheme(Theme{...})`.

Backend can be changed in **Settings**. Invidious mode uses its `/api/v1/search` endpoint; local mode requests and parses YouTube's search page directly. The selected backend and theme are persisted, but search history, playback history, and subscriptions are not.

## Project files

- `main.go` — terminal raw-mode handling, input, UI, navigation, mpv launch
- `scrape.go` — YouTube HTML parser and Invidious API client
- `filters.go` — shared filters and result sorting
- `config.go` — settings, theme registry, and keybind/action extension points

# jabberwock

A deliberately small, native terminal media client written in Go. It combines **YouTube** and **SoundCloud** search in one TUI, keeps no watch history or local subscriptions, and uses only the Go standard library. Playback is delegated to `mpv`.

## Build and run

```sh
go build -ldflags='-s -w' -o jabberwock .
./jabberwock
```

You need an interactive Linux terminal. Install `mpv` to play videos and audio. YouTube playback generally also needs `yt-dlp` available on `PATH`. SoundCloud terminal visualizations are optional: if `cava` is installed, jabberwock runs `mpv` audio-only and quietly in the background while CAVA takes over the terminal; without CAVA, `mpv` plays audio normally in the foreground.

There are no third-party Go modules. Runtime media tools are external programs, not Go dependencies.

## Choose a source

At startup, select **YouTube** or **SoundCloud** with `↑` / `↓` and press `Enter`. Press `Ctrl+P` at any time to switch source. Each source has its own scraper directory (`yt/` and `soundcloud/`) so they can be diagnosed and changed independently.

## Controls

| Key | Action |
| --- | --- |
| `Ctrl+S` or `/` | Search the selected source |
| `Tab` | Switch section: Home → Filters → Settings |
| `↑` / `↓` | Navigate results or the rows in the current section |
| `←` / `→` | Change the selected filter or setting |
| `Enter` | Play a result, load **Next page**, or apply an option |
| `Ctrl+P` | Reopen the YouTube / SoundCloud source chooser |
| `r` | Refresh the current search |
| `g` / `G` | Jump to first / last result |
| `Ctrl+Q` / `Ctrl+C` | Quit |
| `Esc` | Cancel an input prompt |

**Navigation rule:** `Tab` switches between Home, Filters, and Settings. Arrow keys navigate the results and rows *inside* the current section.

Search results are paginated. When more results are available, a selectable **Next page** row appears after the current results. The current list stays in place while the next page is appended, and overlapping results are deduplicated.

## YouTube

The YouTube source can use either direct HTML scraping or an Invidious instance. Change the backend under **Settings**. Local mode parses YouTube search data and uses its continuation token for additional pages. Invidious mode calls the configured instance's `/api/v1/search` endpoint and increments its `page` parameter.

Filters include relevance/views/date sorting, upload time, duration, and a default-on **Hide Shorts** option. YouTube's HTML does not have a stable public scraping contract, and some Invidious instances do not identify every Short, so the backend may need adjustments if its upstream responses change.

## SoundCloud

SoundCloud mode searches tracks (not playlists or user profiles) through SoundCloud's public web API. If no client ID is configured, it loads the SoundCloud homepage and inspects JavaScript bundles newest-first, gathering a small set of candidate `client_id` values. If an API request returns 401/403, jabberwock tries other candidates and reloads the homepage/bundles once to recover from a rotated ID. Candidate IDs are cached only in process memory.

Tracks are filtered before display by default. jabberwock excludes tracks marked blocked or preview-only, SoundCloud Go/subscriber-only monetization models, DRM/encrypted transcodings, and tracks without a supported audio stream. It prefers unencrypted AAC HLS and resolves SoundCloud's media-transcoding API endpoint to the short-lived CDN playlist URL before launching `mpv`. Progressive HTTP is retained only as a compatibility fallback because SoundCloud announced its deprecation after December 31, 2025. See the [SoundCloud streaming migration note](https://developers.soundcloud.com/blog/api-streaming-urls/).

If automatic client-ID discovery stops working, set `soundcloud_client_id` in the config file, edit it in **Settings → SoundCloud client ID**, or export `JABBERWOCK_SC_CLIENT_ID`. Leave the value empty to return to automatic discovery. A directory named `client_id` does not supply an API client ID; it must be discovered from the site or configured as a string. If every discovered ID receives a 401/403, SoundCloud may have changed or restricted its undocumented web API and refreshing the homepage may no longer be sufficient.

When CAVA is installed, selected SoundCloud tracks play with `mpv --no-video --no-terminal` in the background while CAVA displays terminal audio levels. SoundCloud media endpoints are resolved to signed CDN playlist URLs before playback; if `mpv` fails, its warning output is shown instead of being silently discarded. Stop CAVA with `Ctrl+C` to return to jabberwock. If CAVA is absent, audio plays plainly with `mpv`. CAVA's audio-capture backend still depends on the local audio setup.

## Settings and customization

By default, settings are stored at `~/.config/jabberwock/config.json`. Override the path with `JABBERWOCK_CONFIG=/path/to/config.json`.

Example config:

```json
{
  "backend": "local",
  "invidious_url": "https://yewtu.be",
  "soundcloud_client_id": "",
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
    "home": "top",
    "ctrl+p": "switch_source"
  }
}
```

Theme values are ANSI SGR parameter strings (e.g. `36`, `1;35`, `38;5;110`). Add a theme under `themes`, then select it in **Settings → Theme**. `keybinds` maps keys to built-in action names: `search`, `quit`, `next_view`, `switch_source`, `refresh`, `top`, or `bottom`. Go forks can add callbacks with `RegisterAction` and bindings with `RegisterKeybind`; themes can be registered with `RegisterTheme` in `config.go`.

The client writes its configuration file only. Search results stay in memory; no playback history or subscriptions are stored locally.

## Project layout

- `main.go` — raw terminal handling, unified UI, navigation, and playback orchestration
- `config.go` — settings, theme registry, and keybind/action extension points
- `media.go` / `yt_adapter.go` — common search-result model and YouTube adapter
- `filters.go` — common date/duration sorting and filter UI
- `yt/` — YouTube HTML / Invidious search implementation and parser tests
- `soundcloud/` — SoundCloud client-ID discovery, track search, AAC/HLS transcoding selection and stream resolution, and filters

## Related projects

The SoundCloud scraper was implemented specifically for jabberwock using Go's standard library. Its design was informed by the public API/streaming approaches described by [soundcloak](https://github.com/maid-zone/soundcloak) and [wisp](https://github.com/end-sky/wisp); their source code was not copied into this project. See each repository for its respective license and implementation details.

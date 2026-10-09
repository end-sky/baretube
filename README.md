# BareTube

BareTube is an intentionally small desktop YouTube client: a GTK3 GUI written in C, and a standard-library-only Go scraper. There are no accounts, subscriptions, likes, playlists, telemetry, or watch-history database. Search is on demand and playback is delegated to `mpv`.

The project takes inspiration from the *built-in extractor / optional Invidious API* model used by [FreeTube](https://github.com/FreeTubeApp/FreeTube) and [LibreTube](https://github.com/libre-tube/LibreTube). This is a fresh compact implementation, not a verbatim copy of either codebase.

## Build on NixOS

With flakes enabled:

```sh
nix build
./result/bin/baretube
```

Or launch it directly:

```sh
nix run
```

For a non-flake Nix setup:

```sh
nix-build -E 'with import <nixpkgs> {}; callPackage ./default.nix {}'
./result/bin/baretube
```

The flake currently defines Linux packages for `x86_64-linux` and `aarch64-linux`. The C GUI is GTK3-based and the player is `mpv`. The project is intentionally arranged so the scraper and GUI can be built separately.

## Local development

Install a C compiler, Go 1.23+, GTK3 development files, pkg-config, and mpv. Then:

```sh
make
./baretube
```

To run the scraper without the GUI:

```sh
./baretube-backend search local '' 'ambient music'
./baretube-backend search invidious https://yewtu.be 'ambient music'
./baretube-backend stream local '' VIDEO_ID video
```

`stream` prints a tab-separated video URL and optional separate audio URL. The `search` output is tab-separated as `video-id`, `title`, `author`, and `duration` so it can be consumed by the small C frontend without a JSON parser dependency.

## Features

- Small GTK3 window with search results, Play video, and Audio actions.
- Built-in YouTube Innertube search/player lookup with fallback to the watch-page player response.
- Best-effort support for common player signature transforms when streams return a `signatureCipher`.
- Optional Invidious search/playback API and editable instance URL.
- Two themes: pure AMOLED black and simple white.
- Settings saved to `$XDG_CONFIG_HOME/baretube/config.ini` (or the platform's GLib config directory).
- Uses `mpv` as a separate process for audio/video playback.
- No subscription, like, playlist, login, or viewing-history storage.

## Size target

Go is built with `CGO_ENABLED=0`, `-trimpath`, and linker stripping. This is designed to keep the two BareTube executables under the 10 MiB target on common Linux architectures, but the precise size depends on the Go toolchain and architecture. This is **executable size**, not the full Nix runtime closure: GTK3, GLib, and mpv are separate runtime dependencies and make the closure much larger.

## Limits

YouTube changes player formats, signature code, and bot protections regularly. The built-in extractor supports direct stream URLs and several common cipher patterns, but it cannot guarantee every video/format will work; some streams may require a token or transformation that has changed upstream. Invidious mode is the practical fallback. Public Invidious instances can be rate-limited, blocked, or offline, so the instance can be changed in Settings. This app does not bypass access controls or guarantee availability of restricted/age-gated content.

The GTK application is implemented for Linux and the Go scraper is portable Go. The included Nix build is Linux-only; Windows packaging needs a Windows GTK3 toolchain and a Windows `mpv` binary.

## Licensing

BareTube is distributed under GNU AGPL version 3 or later. The implementation is new code; FreeTube and LibreTube are cited as architectural references. Their projects retain their own licensing and attribution:

- FreeTube: AGPL-3.0-or-later — <https://github.com/FreeTubeApp/FreeTube>
- LibreTube: GPL-3.0-or-later — <https://github.com/libre-tube/LibreTube>

### Windows (GTK/MSYS2)

The Go scraper is portable. To build the desktop GUI on Windows, use an MSYS2 UCRT64 environment with GTK3, GCC, pkg-config, and Go installed; make sure GTK runtime DLLs and `mpv.exe` are on `PATH`, then run `./build-windows.ps1` from PowerShell launched with that environment. Windows packaging is not part of the Nix flake, and the resulting executable requires the GTK runtime DLLs.

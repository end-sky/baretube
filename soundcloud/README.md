# SoundCloud backend

This package independently handles SoundCloud client-ID discovery, track-only search, pagination, transcoding selection, and stream URL resolution. It prefers unencrypted AAC HLS streams, keeps progressive HTTP only as a compatibility fallback, and filters blocked/snipped, subscriber-only/Go, DRM/encrypted, and unsupported tracks before returning results to the TUI.

The `media.transcodings[].url` values are API endpoints, not the audio bytes themselves. `ResolveStreamURL` requests the chosen endpoint and resolves it to a short-lived CDN playlist URL (or follows a direct redirect) before the UI invokes `mpv`. This is necessary for current SoundCloud AAC/HLS playback.

The scraper uses only the Go standard library. SoundCloud's web API and streaming formats may change; see SoundCloud's [official streaming migration announcement](https://developers.soundcloud.com/blog/api-streaming-urls/). Run backend tests with `go test ./soundcloud`.

# YouTube backend

This package owns the YouTube HTML scraping and Invidious API implementation. Keep changes to YouTube response parsing, continuation tokens, and Invidious paging here. The root `yt_adapter.go` converts results to the terminal UI's common media model.

Run its parser tests with `go test ./yt`.

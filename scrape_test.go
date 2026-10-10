package main

import "testing"

func TestSelectionCanReachNextPage(t *testing.T) {
	app := App{videos: []Video{{VideoID: "aaaaaaaaaaa"}, {VideoID: "bbbbbbbbbbb"}}, hasMore: true}
	app.moveSelection(1)
	app.moveSelection(1)
	if app.selected != len(app.videos) {
		t.Fatalf("selection = %d, want Next page row index %d", app.selected, len(app.videos))
	}
	app.moveSelection(1)
	if app.selected != len(app.videos) {
		t.Fatalf("selection moved past Next page: %d", app.selected)
	}
	app.moveSelection(-1)
	if app.selected != len(app.videos)-1 {
		t.Fatalf("moving up from Next page selected %d, want %d", app.selected, len(app.videos)-1)
	}
}

func TestAppendUniqueVideos(t *testing.T) {
	old := []Video{{VideoID: "aaaaaaaaaaa", Title: "first"}}
	more := []Video{{VideoID: "aaaaaaaaaaa", Title: "duplicate"}, {VideoID: "bbbbbbbbbbb", Title: "second"}}
	got := appendUniqueVideos(old, more)
	if len(got) != 2 || got[0].Title != "first" || got[1].Title != "second" {
		t.Fatalf("appendUniqueVideos() = %#v", got)
	}
}

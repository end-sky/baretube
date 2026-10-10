package main

import (
	"time"
)

// Video is the UI's common result model. SoundCloud tracks use the same list
// renderer but retain a direct stream URL for audio-only playback.
type Video struct {
	Title           string
	Author          string
	VideoID         string
	WatchURL        string
	StreamURL       string
	IsTrack         bool
	DurationSeconds int64
	DurationKnown   bool
	Views           int64
	ViewsKnown      bool
	Published       time.Time
	PublishedKnown  bool
	PublishedText   string
	IsShort         bool
}

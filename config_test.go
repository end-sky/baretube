package main

import (
	"os/exec"
	"testing"
)

func TestSoundCloudHasOrangeThemeByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.selectedThemeFor("soundcloud").Accent; got != "38;5;208" {
		t.Fatalf("SoundCloud accent = %q, want orange ANSI color 38;5;208", got)
	}
	if got := cfg.selectedThemeFor("youtube").Accent; got != "36" {
		t.Fatalf("YouTube accent = %q, want default wock cyan accent", got)
	}
}

func TestSoundCloudThemeCanBeCustomizedSeparately(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SoundCloudTheme = "paper"
	if got := cfg.selectedThemeFor("soundcloud").Accent; got != "35" {
		t.Fatalf("custom SoundCloud accent = %q, want paper magenta", got)
	}
	if got := cfg.selectedThemeFor("youtube").Accent; got != "36" {
		t.Fatalf("changing SoundCloud theme changed YouTube accent to %q", got)
	}
}

func TestPlaybackProcessCanBeStopped(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exec sleep 10")
	p, err := startPlaybackProcess(cmd)
	if err != nil {
		t.Fatalf("startPlaybackProcess: %v", err)
	}
	p.stop()
	if p.running {
		t.Fatal("process still marked as running after stop")
	}
	if p.err == nil {
		t.Fatal("stopped sleep process unexpectedly exited successfully")
	}
}

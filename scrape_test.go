package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTextValue(t *testing.T) {
	got := textValue(map[string]any{"runs": []any{
		map[string]any{"text": "hello "}, map[string]any{"text": "world"},
	}})
	if got != "hello world" {
		t.Fatalf("textValue() = %q", got)
	}
}

func TestBalancedJSONRespectsStrings(t *testing.T) {
	input := `noise {"title":"brace } in a string", "nested":{"ok":true}} tail`
	start := 6
	got := balancedJSON(input, start)
	var value map[string]any
	if err := json.Unmarshal([]byte(got), &value); err != nil {
		t.Fatalf("invalid extracted JSON %q: %v", got, err)
	}
	if textValue(value["title"]) != "brace } in a string" {
		t.Fatalf("wrong title: %#v", value["title"])
	}
}

func TestChooseFormatPrefersMuxedAtOrBelow720p(t *testing.T) {
	formats := []map[string]any{
		{"url": "1080", "type": `video/mp4; codecs="avc1, mp4a"`, "qualityLabel": "1080p", "bitrate": 5000000},
		{"url": "720", "type": `video/mp4; codecs="avc1, mp4a"`, "qualityLabel": "720p", "bitrate": 2500000},
		{"url": "360", "type": `video/mp4; codecs="avc1, mp4a"`, "qualityLabel": "360p", "bitrate": 900000},
	}
	got := chooseFormat(formats, "muxed")
	if got == nil || textValue(got["url"]) != "720" {
		t.Fatalf("wanted 720p, got %#v", got)
	}
}

func TestSignatureDecipherCommonOps(t *testing.T) {
	script := `AB={rv:function(a){a.reverse()},sw:function(a,b){var c=a[0];a[0]=a[b%a.length];a[b]=c}};XY=function(a){a=a.split("");AB.rv(a);AB.sw(a,2);return a.join("")};x.sig||XY(x);`
	got, ok := decipherSignature(script, "abcdef")
	if !ok {
		t.Fatal("signature operations were not detected")
	}
	if got != "defcba" {
		t.Fatalf("decipherSignature() = %q", got)
	}
}

func TestSearchInvidious(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "test query" {
			t.Fatalf("unexpected query %q", r.URL.Query().Get("q"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"video","videoId":"abcDEF12345","title":"Test title","author":"Test channel","lengthSeconds":125},{"type":"channel","author":"Ignored"}]`))
	}))
	defer server.Close()

	videos, err := searchInvidious(context.Background(), server.URL, "test query")
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 1 {
		t.Fatalf("expected one video, got %d", len(videos))
	}
	if videos[0].ID != "abcDEF12345" || videos[0].Title != "Test title" || videos[0].Duration != "2:05" {
		t.Fatalf("unexpected video: %#v", videos[0])
	}
}

func TestStreamInvidiousSelectsProgressiveAndAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/videos/abcDEF12345" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"formatStreams":[
				{"url":"https://media.invalid/1080","type":"video/mp4; codecs=\"avc1, mp4a\"","qualityLabel":"1080p","bitrate":5000000},
				{"url":"https://media.invalid/720","type":"video/mp4; codecs=\"avc1, mp4a\"","qualityLabel":"720p","bitrate":2500000}
			],
			"adaptiveFormats":[{"url":"https://media.invalid/audio","type":"audio/webm; codecs=\"opus\"","bitrate":160000}]
		}`))
	}))
	defer server.Close()

	video, err := streamInvidious(context.Background(), server.URL, "abcDEF12345", "video")
	if err != nil {
		t.Fatal(err)
	}
	if video.VideoURL != "https://media.invalid/720" || video.AudioURL != "" {
		t.Fatalf("unexpected video stream: %#v", video)
	}
	audio, err := streamInvidious(context.Background(), server.URL, "abcDEF12345", "audio")
	if err != nil {
		t.Fatal(err)
	}
	if audio.VideoURL != "https://media.invalid/audio" {
		t.Fatalf("unexpected audio stream: %#v", audio)
	}
}

func TestChooseAudioPrefersAudioOnly(t *testing.T) {
	formats := []map[string]any{
		{"url": "muxed", "mimeType": `video/mp4; codecs="avc1, mp4a"`, "qualityLabel": "720p", "bitrate": 2500000},
		{"url": "audio", "mimeType": `audio/webm; codecs="opus"`, "bitrate": 160000},
	}
	got := chooseFormat(formats, "audio")
	if got == nil || textValue(got["url"]) != "audio" {
		t.Fatalf("wanted audio-only stream, got %#v", got)
	}
}

func TestCipherURLAlreadyDecipheredSignature(t *testing.T) {
	got := cipherURLFromScript("url=https%3A%2F%2Fmedia.invalid%2Fstream&sig=plain-signature&sp=sig", "")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("sig") != "plain-signature" {
		t.Fatalf("signature missing from URL %q", got)
	}
}

package soundcloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindClientID(t *testing.T) {
	for _, input := range []string{
		`window.SC = {client_id:"abcdefghijklmnopqrstuvwxyz123456"}`,
		`{"client_id":"abcdefghijklmnopqrstuvwxyz123456"}`,
	} {
		if got := findClientID([]byte(input)); got != "abcdefghijklmnopqrstuvwxyz123456" {
			t.Fatalf("findClientID(%q) = %q", input, got)
		}
	}
}

func TestSearchFiltersGoAndSupportsHLSAndProgressiveTracks(t *testing.T) {
	var sawClientID bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><script>window.SC = {client_id:"abcdefghijklmnopqrstuvwxyz123456"};</script></html>`))
		case r.URL.Path == "/api/search/tracks":
			sawClientID = r.URL.Query().Get("client_id") == "abcdefghijklmnopqrstuvwxyz123456"
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"next_href":"` + serverURLPlaceholder + `",
				"collection":[
				  {"id":1,"title":"Playable","permalink_url":"https://soundcloud.com/u/one","duration":120000,"policy":"ALLOW","access":"playable","monetization_model":"AD_SUPPORTED","media":{"transcodings":[{"url":"https://api-v2.soundcloud.com/media/one/stream/progressive","format":{"protocol":"progressive","mime_type":"audio/mpeg"}}]}},
				  {"id":2,"title":"Go only","policy":"ALLOW","access":"blocked","monetization_model":"SUB_HIGH_TIER","media":{"transcodings":[{"url":"https://api-v2.soundcloud.com/media/two/stream/progressive","format":{"protocol":"progressive","mime_type":"audio/mpeg"}}]}},
				  {"id":3,"title":"HLS only","policy":"ALLOW","access":"playable","monetization_model":"AD_SUPPORTED","media":{"transcodings":[{"url":"https://api-v2.soundcloud.com/media/three/stream/hls","format":{"protocol":"hls","mime_type":"audio/mpeg"}}]}}
				]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	// The returned next_href must be on the same host; it should indicate the
	// existence of another page without triggering another request in this test.
	serverURLPlaceholder = server.URL + "/api/search/tracks?offset=3"
	client := &Client{HTTPClient: server.Client(), HomeURL: server.URL, APIBase: server.URL + "/api"}
	page, err := client.Search(context.Background(), "ambient", 0, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if !sawClientID {
		t.Fatal("search request did not include discovered client_id")
	}
	if len(page.Tracks) != 2 || page.Tracks[0].ID != "1" || page.Tracks[1].ID != "3" {
		t.Fatalf("expected progressive and unencrypted HLS tracks, excluding Go-only tracks, got %#v", page.Tracks)
	}
	if !page.HasMore || page.NextOffset != 3 {
		t.Fatalf("unexpected pagination metadata: %#v", page)
	}
	if !strings.Contains(page.Tracks[0].StreamURL, "client_id=abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("stream URL did not include client ID: %s", page.Tracks[0].StreamURL)
	}
}

var serverURLPlaceholder string

func TestPlayableRejectsSubscriberAndSnippedTracks(t *testing.T) {
	base := Track{ID: "1", Title: "song", StreamURL: "https://example.test/stream"}
	cases := []Track{
		base,
		{ID: "2", Title: "go", StreamURL: base.StreamURL, MonetizationModel: "SUB_HIGH_TIER"},
		{ID: "3", Title: "snip", StreamURL: base.StreamURL, Policy: "SNIP"},
		{ID: "4", Title: "blocked", StreamURL: base.StreamURL, Access: "blocked"},
		{ID: "5", Title: "missing stream"},
	}
	want := []bool{true, false, false, false, false}
	for i, track := range cases {
		if got := Playable(track); got != want[i] {
			t.Errorf("Playable(%#v) = %v, want %v", track, got, want[i])
		}
	}
}

func TestDiscoveryPrefersNewestScriptBundle(t *testing.T) {
	oldID := "oldclientidabcdefghijklmnop123456"
	newID := "newclientidabcdefghijklmnop123456"
	var used []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><script src="/assets/old.js"></script><script src="/assets/new.js"></script></html>`))
		case "/assets/old.js":
			_, _ = w.Write([]byte(`window.SC={client_id:"` + oldID + `"}`))
		case "/assets/new.js":
			_, _ = w.Write([]byte(`window.SC={client_id:"` + newID + `"}`))
		case "/api/search/tracks":
			id := r.URL.Query().Get("client_id")
			used = append(used, id)
			if id != newID {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"collection":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), HomeURL: server.URL, APIBase: server.URL + "/api"}
	if _, err := client.Search(context.Background(), "ambient", 0, 20, ""); err != nil {
		t.Fatal(err)
	}
	if len(used) == 0 || used[0] != newID {
		t.Fatalf("expected newest asset ID %q first, got %v", newID, used)
	}
}

func TestSearchRefreshesHomepageAfterUnauthorizedClientID(t *testing.T) {
	oldID := "oldclientidabcdefghijklmnop123456"
	newID := "newclientidabcdefghijklmnop123456"
	var homeLoads int
	var used []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			homeLoads++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><script src="/assets/app.js"></script></html>`))
			// The page references the bundle, while the bundle's content changes
			// between home-page loads to simulate a client-ID rotation.
		case "/assets/app.js":
			id := oldID
			if homeLoads > 1 {
				id = newID
			}
			_, _ = w.Write([]byte(`window.SC={client_id:"` + id + `"}`))
		case "/api/search/tracks":
			id := r.URL.Query().Get("client_id")
			used = append(used, id)
			if id != newID {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"collection":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), HomeURL: server.URL, APIBase: server.URL + "/api"}
	if _, err := client.Search(context.Background(), "ambient", 0, 20, ""); err != nil {
		t.Fatal(err)
	}
	if homeLoads < 2 {
		t.Fatalf("expected homepage to be reloaded after 401, got %d loads", homeLoads)
	}
	if len(used) < 2 || used[0] != oldID || used[len(used)-1] != newID {
		t.Fatalf("expected stale then refreshed ID, got %v", used)
	}
}

func TestChoosePlayableTranscodingPrefersAACAndRejectsDRM(t *testing.T) {
	transcodings := []apiTranscoding{
		{URL: "https://api-v2.soundcloud.com/media/drm/stream/hls", Preset: "aac_160k", Format: struct {
			Protocol string `json:"protocol"`
			MimeType string `json:"mime_type"`
		}{Protocol: "ctr-encrypted-hls", MimeType: "application/vnd.apple.mpegurl"}},
		{URL: "https://api-v2.soundcloud.com/media/progressive/stream/progressive", Preset: "mp3_128k", Format: struct {
			Protocol string `json:"protocol"`
			MimeType string `json:"mime_type"`
		}{Protocol: "progressive", MimeType: "audio/mpeg"}},
		{URL: "https://api-v2.soundcloud.com/media/aac/stream/hls", Preset: "aac_160k", Format: struct {
			Protocol string `json:"protocol"`
			MimeType string `json:"mime_type"`
		}{Protocol: "hls", MimeType: "application/vnd.apple.mpegurl"}},
	}
	got, ok := choosePlayableTranscoding(transcodings)
	if !ok || !strings.Contains(got.URL, "/aac/") {
		t.Fatalf("expected unencrypted AAC HLS to win over progressive and encrypted HLS, got %#v, ok=%v", got, ok)
	}
}

func TestResolveStreamURLDecodesAPIEndpoint(t *testing.T) {
	var sawClientID bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/media/track/stream/hls" {
			http.NotFound(w, r)
			return
		}
		sawClientID = r.URL.Query().Get("client_id") == "valid-client-id-1234567890"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://cf-hls-media.sndcdn.com/playlist.m3u8?token=short-lived"}`))
	}))
	defer server.Close()
	client := &Client{
		HTTPClient: server.Client(),
		APIBase:    server.URL + "/api",
	}
	got, err := client.ResolveStreamURL(context.Background(), server.URL+"/api/media/track/stream/hls?client_id=valid-client-id-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if !sawClientID {
		t.Fatal("transcoding resolution did not preserve client_id")
	}
	if got != "https://cf-hls-media.sndcdn.com/playlist.m3u8?token=short-lived" {
		t.Fatalf("resolved URL = %q", got)
	}
}

func TestResolveStreamURLHandlesRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/media/track/stream/hls" {
			http.Redirect(w, r, "https://cf-hls-media.sndcdn.com/playlist.m3u8?token=redirected", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := &Client{HTTPClient: server.Client(), APIBase: server.URL + "/api"}
	got, err := client.ResolveStreamURL(context.Background(), server.URL+"/api/media/track/stream/hls?client_id=valid-client-id-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cf-hls-media.sndcdn.com/playlist.m3u8?token=redirected" {
		t.Fatalf("resolved redirect URL = %q", got)
	}
}

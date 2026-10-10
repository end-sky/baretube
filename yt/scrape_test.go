package yt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractInitialData(t *testing.T) {
	html := []byte(`<html><script>var ytInitialData = {"x":{"brace":"}"},"items":[1,2]};</script></html>`)
	data, err := extractInitialData(html)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("extracted data is not JSON: %v", err)
	}
	if decoded["x"] == nil {
		t.Fatalf("expected x property in %s", data)
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]int64{"1:23": 83, "12:34": 754, "1:02:03": 3723}
	for input, want := range cases {
		got, ok := parseDuration(input)
		if !ok || got != want {
			t.Errorf("parseDuration(%q) = %d, %v; want %d, true", input, got, ok, want)
		}
	}
	if _, ok := parseDuration("LIVE"); ok {
		t.Error("LIVE should not be parsed as a duration")
	}
}

func TestParseViewCount(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{{"1,234 views", 1234}, {"1.2K views", 1200}, {"2.5M views", 2500000}, {"1.2万 回視聴", 12000}, {"3億 回視聴", 300000000}}
	for _, tc := range cases {
		got, ok := parseViewCount(tc.input)
		if !ok || got != tc.want {
			t.Errorf("parseViewCount(%q) = %d, %v; want %d, true", tc.input, got, ok, tc.want)
		}
	}
}

func TestFilterAndSort(t *testing.T) {
	now := time.Now()
	videos := []Video{
		{VideoID: "aaaaaaaaaaa", Title: "old", Views: 100, ViewsKnown: true, Published: now.Add(-48 * time.Hour), PublishedKnown: true, DurationSeconds: 600, DurationKnown: true},
		{VideoID: "bbbbbbbbbbb", Title: "new", Views: 1000, ViewsKnown: true, Published: now.Add(-10 * time.Minute), PublishedKnown: true, DurationSeconds: 900, DurationKnown: true},
		{VideoID: "ccccccccccc", Title: "short", IsShort: true, Views: 9999, ViewsKnown: true, Published: now, PublishedKnown: true, DurationSeconds: 20, DurationKnown: true},
	}
	filters := DefaultFilters()
	filters.Sort = "views"
	got := FilterAndSort(videos, filters)
	if len(got) != 2 || got[0].Title != "new" {
		t.Fatalf("unexpected filtered/sorted videos: %#v", got)
	}
	filters.Date = "hour"
	got = FilterAndSort(videos, filters)
	if len(got) != 1 || got[0].Title != "new" {
		t.Fatalf("date filter returned unexpected videos: %#v", got)
	}
}

func TestExtractContinuationToken(t *testing.T) {
	var data any
	raw := []byte(`{"contents":{"continuationItemRenderer":{"continuationEndpoint":{"continuationCommand":{"token":"next-token"}}}}}`)
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if got := extractContinuationToken(data); got != "next-token" {
		t.Fatalf("extractContinuationToken() = %q, want %q", got, "next-token")
	}
}

func TestExtractYouTubeSettings(t *testing.T) {
	html := []byte(`<script>ytcfg.set({"INNERTUBE_API_KEY":"test-key","INNERTUBE_CONTEXT":{"client":{"clientName":"WEB","clientVersion":"test-version"}}});</script>`)
	if got := extractSettingString(html, "INNERTUBE_API_KEY"); got != "test-key" {
		t.Fatalf("API key = %q, want test-key", got)
	}
	contextJSON := extractSettingObject(html, "INNERTUBE_CONTEXT")
	var context map[string]any
	if err := json.Unmarshal(contextJSON, &context); err != nil {
		t.Fatalf("context JSON didn't parse: %v", err)
	}
	client, ok := context["client"].(map[string]any)
	if !ok || client["clientName"] != "WEB" {
		t.Fatalf("unexpected client context: %#v", context)
	}
}

func TestInvidiousSearchUsesPageParameter(t *testing.T) {
	var requestedPage string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPage = r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"video","title":"demo","videoId":"abcdefghijk","author":"creator","lengthSeconds":120,"viewCount":100,"published":1760000000,"publishedText":"2 days ago"}]`))
	}))
	defer server.Close()

	result, err := searchInvidious(context.Background(), Config{InvidiousURL: server.URL}, "test", DefaultFilters(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if requestedPage != "2" {
		t.Fatalf("request page = %q, want 2", requestedPage)
	}
	if len(result.Videos) != 1 || !result.HasMore {
		t.Fatalf("unexpected paged result: %#v", result)
	}
}

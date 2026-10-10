package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 24 << 20

var (
	countRE    = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?\s*[万億]|[0-9][0-9,.]*\s*[kmb]|[0-9][0-9,.]*)`)
	relativeRE = regexp.MustCompile(`(?i)(\d+)\s*(seconds?|minutes?|hours?|days?|weeks?|months?|years?)\s*ago`)
	videoIDRE  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
)

type Video struct {
	Title           string
	Author          string
	VideoID         string
	WatchURL        string
	DurationSeconds int64
	DurationKnown   bool
	Views           int64
	ViewsKnown      bool
	Published       time.Time
	PublishedKnown  bool
	PublishedText   string
	IsShort         bool
}

// SearchCursor holds the opaque state required to request the next search page.
// The local backend uses YouTube's continuation token and client context; Invidious
// uses a numeric page parameter instead.
type SearchCursor struct {
	Token   string          `json:"token,omitempty"`
	APIKey  string          `json:"api_key,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
}

type SearchPage struct {
	Videos  []Video
	Cursor  SearchCursor
	HasMore bool
}

// SearchVideos is kept as a small compatibility wrapper for first-page callers.
func SearchVideos(ctx context.Context, cfg Config, query string, filters Filters) ([]Video, error) {
	page, err := SearchVideosPage(ctx, cfg, query, filters, 1, SearchCursor{})
	if err != nil {
		return nil, err
	}
	return page.Videos, nil
}

// SearchVideosPage fetches one page from the selected backend. page is one-based
// and is used by Invidious; cursor carries continuation data for local scraping.
func SearchVideosPage(ctx context.Context, cfg Config, query string, filters Filters, page int, cursor SearchCursor) (SearchPage, error) {
	if strings.TrimSpace(query) == "" {
		return SearchPage{}, errors.New("enter a search query with Ctrl+S")
	}
	if page < 1 {
		page = 1
	}
	var result SearchPage
	var err error
	if cfg.Backend == "invidious" {
		result, err = searchInvidious(ctx, cfg, query, filters, page)
	} else if cursor.Token != "" {
		result, err = searchYouTubeContinuation(ctx, cursor)
	} else {
		result, err = searchYouTubeHTML(ctx, query)
	}
	if err != nil {
		return SearchPage{}, err
	}
	result.Videos = FilterAndSort(result.Videos, filters)
	return result, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 18 * time.Second}
}

func getBody(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36 jabberwock/1.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// searchYouTubeHTML scrapes the public YouTube search HTML without browser or
// third-party-library dependencies. YouTube may change the page structure at any time.
func searchYouTubeHTML(ctx context.Context, query string) (SearchPage, error) {
	target := "https://www.youtube.com/results?search_query=" + url.QueryEscape(query)
	body, err := getBody(ctx, target)
	if err != nil {
		return SearchPage{}, fmt.Errorf("YouTube request failed: %w", err)
	}
	data, err := extractInitialData(body)
	if err != nil {
		return SearchPage{}, fmt.Errorf("could not find YouTube search data (the page may have changed or returned a consent screen): %w", err)
	}
	videos, token, err := parseYouTubeSearchData(data)
	if err != nil {
		return SearchPage{}, err
	}
	cursor := SearchCursor{
		Token:   token,
		APIKey:  extractSettingString(body, "INNERTUBE_API_KEY"),
		Context: extractSettingObject(body, "INNERTUBE_CONTEXT"),
	}
	if len(videos) == 0 {
		return SearchPage{}, errors.New("no standard videos found; YouTube may have changed its search page")
	}
	return SearchPage{Videos: videos, Cursor: cursor, HasMore: cursor.Token != "" && cursor.APIKey != ""}, nil
}

func searchYouTubeContinuation(ctx context.Context, cursor SearchCursor) (SearchPage, error) {
	if cursor.Token == "" || cursor.APIKey == "" {
		return SearchPage{}, errors.New("YouTube did not provide a usable next-page token; refresh the search")
	}
	contextValue := any(map[string]any{
		"client": map[string]any{"clientName": "WEB", "clientVersion": "2.20260114.08.00", "hl": "en", "gl": "US"},
	})
	if len(cursor.Context) > 0 {
		if err := json.Unmarshal(cursor.Context, &contextValue); err != nil {
			return SearchPage{}, fmt.Errorf("decode YouTube client context: %w", err)
		}
	}
	payload, err := json.Marshal(map[string]any{
		"context":      contextValue,
		"continuation": cursor.Token,
	})
	if err != nil {
		return SearchPage{}, fmt.Errorf("prepare YouTube continuation request: %w", err)
	}
	target := "https://www.youtube.com/youtubei/v1/search?key=" + url.QueryEscape(cursor.APIKey) + "&prettyPrint=false"
	body, err := postJSON(ctx, target, payload)
	if err != nil {
		return SearchPage{}, fmt.Errorf("YouTube next-page request failed: %w", err)
	}
	videos, token, err := parseYouTubeSearchData(body)
	if err != nil {
		return SearchPage{}, err
	}
	cursor.Token = token
	return SearchPage{Videos: videos, Cursor: cursor, HasMore: token != ""}, nil
}

func parseYouTubeSearchData(data []byte) ([]Video, string, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, "", fmt.Errorf("decode YouTube search data: %w", err)
	}
	var videos []Video
	walkJSON(root, func(key string, value map[string]any) {
		if key != "videoRenderer" {
			return
		}
		video, ok := parseYouTubeRenderer(value)
		if ok {
			videos = append(videos, video)
		}
	})
	return uniqueVideos(videos), extractContinuationToken(root), nil
}

func extractContinuationToken(root any) string {
	var token string
	walkJSON(root, func(key string, value map[string]any) {
		if token != "" || key != "continuationItemRenderer" {
			return
		}
		endpoint, _ := value["continuationEndpoint"].(map[string]any)
		command, _ := endpoint["continuationCommand"].(map[string]any)
		token = stringValue(command["token"])
	})
	return token
}

var innertubeAPIKeyRE = regexp.MustCompile(`"INNERTUBE_API_KEY"\s*:\s*"([^"]+)"`)

func extractSettingString(body []byte, key string) string {
	if key == "INNERTUBE_API_KEY" {
		match := innertubeAPIKeyRE.FindSubmatch(body)
		if len(match) == 2 {
			return string(match[1])
		}
		return ""
	}
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:\s*"([^"]+)"`)
	match := pattern.FindSubmatch(body)
	if len(match) == 2 {
		return string(match[1])
	}
	return ""
}

func extractSettingObject(body []byte, key string) json.RawMessage {
	text := string(body)
	needle := `"` + key + `"`
	at := strings.Index(text, needle)
	if at < 0 {
		return nil
	}
	at += len(needle)
	colon := strings.Index(text[at:], ":")
	if colon < 0 {
		return nil
	}
	at += colon + 1
	for at < len(text) && (text[at] == ' ' || text[at] == '\n' || text[at] == '\r' || text[at] == '\t') {
		at++
	}
	if at >= len(text) || text[at] != '{' {
		return nil
	}
	end := findJSONObjectEnd(text, at)
	if end < at {
		return nil
	}
	return json.RawMessage(text[at : end+1])
}

func postJSON(ctx context.Context, target string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36 jabberwock/1.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-YouTube-Client-Name", "1")
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func extractInitialData(html []byte) ([]byte, error) {
	text := string(html)
	needle := "ytInitialData"
	searchFrom := 0
	for searchFrom < len(text) {
		rel := strings.Index(text[searchFrom:], needle)
		if rel < 0 {
			break
		}
		pos := searchFrom + rel + len(needle)
		braceRel := strings.Index(text[pos:], "{")
		if braceRel < 0 {
			return nil, errors.New("initial data object missing")
		}
		start := pos + braceRel
		if end := findJSONObjectEnd(text, start); end > start {
			return []byte(text[start : end+1]), nil
		}
		searchFrom = pos
	}
	return nil, errors.New("ytInitialData not present")
}

func findJSONObjectEnd(s string, start int) int {
	if start >= len(s) || s[start] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func walkJSON(node any, fn func(key string, value map[string]any)) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if childMap, ok := child.(map[string]any); ok {
				fn(key, childMap)
			}
			walkJSON(child, fn)
		}
	case []any:
		for _, child := range value {
			walkJSON(child, fn)
		}
	}
}

func parseYouTubeRenderer(renderer map[string]any) (Video, bool) {
	id := stringValue(renderer["videoId"])
	if !videoIDRE.MatchString(id) {
		return Video{}, false
	}
	title := rendererText(renderer["title"])
	if title == "" {
		return Video{}, false
	}
	author := rendererText(renderer["ownerText"])
	if author == "" {
		author = rendererText(renderer["shortBylineText"])
	}
	if author == "" {
		author = rendererText(renderer["longBylineText"])
	}
	publishedText := rendererText(renderer["publishedTimeText"])
	durationText := rendererText(renderer["lengthText"])
	viewsText := rendererText(renderer["viewCountText"])
	if viewsText == "" {
		viewsText = rendererText(renderer["shortViewCountText"])
	}
	watchURL := "https://www.youtube.com/watch?v=" + id
	short := false
	if endpoint, ok := renderer["navigationEndpoint"].(map[string]any); ok {
		if meta, ok := endpoint["commandMetadata"].(map[string]any); ok {
			if webCommand, ok := meta["webCommandMetadata"].(map[string]any); ok {
				path := stringValue(webCommand["url"])
				if strings.Contains(path, "/shorts/") {
					short = true
					watchURL = "https://www.youtube.com" + path
				} else if strings.HasPrefix(path, "/watch?") {
					watchURL = "https://www.youtube.com" + path
				}
			}
		}
	}
	if rendererHasShortsBadge(renderer) {
		short = true
	}
	video := Video{Title: cleanText(title), Author: cleanText(author), VideoID: id, WatchURL: watchURL, PublishedText: cleanText(publishedText), IsShort: short}
	if seconds, ok := parseDuration(durationText); ok {
		video.DurationSeconds, video.DurationKnown = seconds, true
	}
	if views, ok := parseViewCount(viewsText); ok {
		video.Views, video.ViewsKnown = views, true
	}
	if published, ok := parsePublishedText(publishedText); ok {
		video.Published, video.PublishedKnown = published, true
	}
	return video, true
}

func rendererHasShortsBadge(node any) bool {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.Contains(strings.ToLower(key), "badge") {
				encoded, _ := json.Marshal(child)
				text := strings.ToLower(string(encoded))
				if strings.Contains(text, "shorts") || strings.Contains(text, "badge_style_type_shorts") {
					return true
				}
			}
			if rendererHasShortsBadge(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if rendererHasShortsBadge(child) {
				return true
			}
		}
	}
	return false
}

func rendererText(value any) string {
	m, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if text := stringValue(m["simpleText"]); text != "" {
		return text
	}
	if runs, ok := m["runs"].([]any); ok {
		var b strings.Builder
		for _, item := range runs {
			if run, ok := item.(map[string]any); ok {
				b.WriteString(stringValue(run["text"]))
			}
		}
		return b.String()
	}
	return ""
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func uniqueVideos(in []Video) []Video {
	seen := make(map[string]bool, len(in))
	out := make([]Video, 0, len(in))
	for _, video := range in {
		if video.VideoID == "" || seen[video.VideoID] {
			continue
		}
		seen[video.VideoID] = true
		out = append(out, video)
	}
	return out
}

type invidiousResult struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	VideoID       string `json:"videoId"`
	Author        string `json:"author"`
	LengthSeconds int64  `json:"lengthSeconds"`
	ViewCount     int64  `json:"viewCount"`
	Published     int64  `json:"published"`
	PublishedText string `json:"publishedText"`
	LiveNow       bool   `json:"liveNow"`
	IsShort       bool   `json:"isShort"`
	ShortForm     bool   `json:"shortForm"`
}

func searchInvidious(ctx context.Context, cfg Config, query string, filters Filters, page int) (SearchPage, error) {
	if strings.TrimSpace(query) == "" {
		return SearchPage{}, errors.New("enter a search query with Ctrl+S")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.InvidiousURL), "/")
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return SearchPage{}, errors.New("Invidious URL must start with https:// or http://")
	}
	endpoint, err := url.Parse(base + "/api/v1/search")
	if err != nil {
		return SearchPage{}, fmt.Errorf("invalid Invidious URL: %w", err)
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("page", strconv.Itoa(page))
	params.Set("type", "video")
	params.Set("sort", map[string]string{"relevance": "relevance", "views": "views", "date": "relevance"}[filters.Sort])
	if params.Get("sort") == "" {
		params.Set("sort", "relevance")
	}
	if filters.Date != "any" {
		params.Set("date", filters.Date)
	}
	if filters.Duration != "any" {
		params.Set("duration", filters.Duration)
	}
	endpoint.RawQuery = params.Encode()
	body, err := getBody(ctx, endpoint.String())
	if err != nil {
		return SearchPage{}, fmt.Errorf("Invidious request failed: %w", err)
	}
	var response []invidiousResult
	if err := json.Unmarshal(body, &response); err != nil {
		return SearchPage{}, fmt.Errorf("decode Invidious response: %w", err)
	}
	videos := make([]Video, 0, len(response))
	for _, item := range response {
		id := strings.TrimSpace(item.VideoID)
		if !videoIDRE.MatchString(id) || item.Title == "" {
			continue
		}
		kind := strings.ToLower(item.Type)
		video := Video{
			Title:           cleanText(item.Title),
			Author:          cleanText(item.Author),
			VideoID:         id,
			WatchURL:        "https://www.youtube.com/watch?v=" + id,
			DurationSeconds: item.LengthSeconds,
			DurationKnown:   item.LengthSeconds > 0 && !item.LiveNow,
			Views:           item.ViewCount,
			ViewsKnown:      item.ViewCount > 0,
			PublishedText:   cleanText(item.PublishedText),
			IsShort:         item.IsShort || item.ShortForm || kind == "shortvideo" || kind == "short_video",
		}
		if item.Published > 0 {
			video.Published = time.Unix(item.Published, 0)
			video.PublishedKnown = true
		} else if published, ok := parsePublishedText(item.PublishedText); ok {
			video.Published = published
			video.PublishedKnown = true
		}
		videos = append(videos, video)
	}
	return SearchPage{Videos: uniqueVideos(videos), HasMore: len(response) > 0}, nil
}

func parseDuration(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "LIVE") || strings.Contains(strings.ToLower(value), "upcoming") {
		return 0, false
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total int64
	for _, part := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

func parseViewCount(value string) (int64, bool) {
	value = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, ",", "")))
	if value == "" {
		return 0, false
	}
	match := countRE.FindString(value)
	if match == "" {
		return 0, false
	}
	match = strings.TrimSpace(strings.ReplaceAll(match, " ", ""))
	multiplier := float64(1)
	if strings.HasSuffix(match, "万") {
		multiplier, match = 1e4, strings.TrimSuffix(match, "万")
	} else if strings.HasSuffix(match, "億") {
		multiplier, match = 1e8, strings.TrimSuffix(match, "億")
	} else if strings.HasSuffix(match, "k") {
		multiplier, match = 1e3, strings.TrimSuffix(match, "k")
	} else if strings.HasSuffix(match, "m") {
		multiplier, match = 1e6, strings.TrimSuffix(match, "m")
	} else if strings.HasSuffix(match, "b") {
		multiplier, match = 1e9, strings.TrimSuffix(match, "b")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(match), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int64(f * multiplier), true
}

func parsePublishedText(value string) (time.Time, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return time.Time{}, false
	}
	now := time.Now()
	if strings.Contains(value, "just now") || strings.Contains(value, "moments ago") || strings.Contains(value, "今") && strings.Contains(value, "前") {
		if strings.Contains(value, "今") {
			return now, true
		}
		return now, true
	}
	if m := relativeRE.FindStringSubmatch(value); len(m) == 3 {
		amount, _ := strconv.Atoi(m[1])
		unit := strings.ToLower(m[2])
		var d time.Duration
		switch {
		case strings.HasPrefix(unit, "second"):
			d = time.Duration(amount) * time.Second
		case strings.HasPrefix(unit, "minute"):
			d = time.Duration(amount) * time.Minute
		case strings.HasPrefix(unit, "hour"):
			d = time.Duration(amount) * time.Hour
		case strings.HasPrefix(unit, "day"):
			d = time.Duration(amount) * 24 * time.Hour
		case strings.HasPrefix(unit, "week"):
			d = time.Duration(amount) * 7 * 24 * time.Hour
		case strings.HasPrefix(unit, "month"):
			d = time.Duration(amount) * 30 * 24 * time.Hour
		case strings.HasPrefix(unit, "year"):
			d = time.Duration(amount) * 365 * 24 * time.Hour
		}
		return now.Add(-d), true
	}
	// Basic Japanese relative upload labels, e.g. "3日前", "2週間前".
	jpRE := regexp.MustCompile(`([0-9]+)\s*(秒|分|時間|日|週間|か月|ヶ月|年)前`)
	if m := jpRE.FindStringSubmatch(value); len(m) == 3 {
		amount, _ := strconv.Atoi(m[1])
		var d time.Duration
		switch m[2] {
		case "秒":
			d = time.Duration(amount) * time.Second
		case "分":
			d = time.Duration(amount) * time.Minute
		case "時間":
			d = time.Duration(amount) * time.Hour
		case "日":
			d = time.Duration(amount) * 24 * time.Hour
		case "週間":
			d = time.Duration(amount) * 7 * 24 * time.Hour
		case "か月", "ヶ月":
			d = time.Duration(amount) * 30 * 24 * time.Hour
		case "年":
			d = time.Duration(amount) * 365 * 24 * time.Hour
		}
		return now.Add(-d), true
	}
	return time.Time{}, false
}

func cleanText(value string) string {
	value = strings.ReplaceAll(value, "\u200b", "")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.Join(strings.Fields(value), " ")
}

// Package soundcloud implements a dependency-free SoundCloud search scraper.
// It discovers the current public client ID from SoundCloud's web assets,
// searches only tracks, and keeps tracks without a directly playable
// progressive stream out of results by default.
package soundcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHomeURL        = "https://soundcloud.com"
	defaultAPIURL         = "https://api-v2.soundcloud.com"
	defaultLimit          = 20
	maxBodyBytes          = 20 << 20
	maxClientIDCandidates = 16
)

var (
	// SoundCloud ships its web API client ID inside JavaScript assets. The
	// latest bundle tends to be near the end of the script list, so discovery
	// checks assets newest-first instead of trusting the first match.
	scriptSrcRE   = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+\.js(?:\?[^"']*)?)["']`)
	clientIDRE    = regexp.MustCompile(`(?i)["']?client_id["']?\s*[:=]\s*["']([A-Za-z0-9_-]{16,})["']`)
	clientIDLoose = regexp.MustCompile(`(?i)client_id.{0,80}?["'=:\s]([A-Za-z0-9_-]{24,})`)
)

// Client is safe to reuse for multiple searches. The discovered client ID is
// cached only in memory and is never written to disk.
type Client struct {
	HTTPClient *http.Client
	HomeURL    string
	APIBase    string
	ClientID   string

	mu        sync.Mutex
	cachedIDs []string
}

type Track struct {
	ID                 string
	Title              string
	Username           string
	PermalinkURL       string
	StreamURL          string
	DurationMS         int64
	PlaybackCount      int64
	PlaybackCountKnown bool
	CreatedAt          time.Time
	CreatedAtText      string
	Policy             string
	Access             string
	MonetizationModel  string
}

type SearchPage struct {
	Tracks     []Track
	HasMore    bool
	NextOffset int
	NextURL    string
}

type apiTranscoding struct {
	URL      string `json:"url"`
	Preset   string `json:"preset"`
	Quality  string `json:"quality"`
	Protocol string `json:"-"`
	Format   struct {
		Protocol string `json:"protocol"`
		MimeType string `json:"mime_type"`
	} `json:"format"`
}

type apiTrack struct {
	ID                json.RawMessage `json:"id"`
	Urn               string          `json:"urn"`
	Title             string          `json:"title"`
	PermalinkURL      string          `json:"permalink_url"`
	Duration          int64           `json:"duration"`
	PlaybackCount     *int64          `json:"playback_count"`
	CreatedAt         string          `json:"created_at"`
	Policy            string          `json:"policy"`
	Access            string          `json:"access"`
	MonetizationModel string          `json:"monetization_model"`
	Streamable        *bool           `json:"streamable"`
	User              struct {
		Username string `json:"username"`
	} `json:"user"`
	Media struct {
		Transcodings []apiTranscoding `json:"transcodings"`
	} `json:"media"`
}

type apiSearchResponse struct {
	Collection   []apiTrack `json:"collection"`
	NextHref     string     `json:"next_href"`
	TotalResults int        `json:"total_results"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 12 * time.Second}
}

func (c *Client) homeURL() string {
	if strings.TrimSpace(c.HomeURL) != "" {
		return strings.TrimRight(c.HomeURL, "/")
	}
	return defaultHomeURL
}

func (c *Client) apiBase() string {
	if strings.TrimSpace(c.APIBase) != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return defaultAPIURL
}

func (c *Client) clientIDs(ctx context.Context, refresh bool) ([]string, error) {
	// An explicitly configured ID is authoritative. Do not silently replace a
	// user's configured value; return a useful 401 message if it is rejected.
	if id := strings.TrimSpace(c.ClientID); id != "" {
		return []string{id}, nil
	}
	if !refresh {
		c.mu.Lock()
		cached := append([]string(nil), c.cachedIDs...)
		c.mu.Unlock()
		if len(cached) != 0 {
			return cached, nil
		}
	}

	ids, err := c.discoverClientIDs(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cachedIDs = append([]string(nil), ids...)
	c.mu.Unlock()
	return ids, nil
}

func (c *Client) discoverClientIDs(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, c.homeURL())
	if err != nil {
		return nil, fmt.Errorf("load SoundCloud home page: %w", err)
	}

	ids := make([]string, 0, 8)
	seenIDs := make(map[string]bool)
	addIDs := func(data []byte) {
		for _, id := range findClientIDs(data) {
			if len(ids) >= maxClientIDCandidates {
				return
			}
			if !seenIDs[id] {
				seenIDs[id] = true
				ids = append(ids, id)
			}
		}
	}

	// Prioritize JavaScript assets over any older IDs embedded in HTML. The
	// homepage can have bootstrapped data left over from a previous deployment.
	matches := scriptSrcRE.FindAllSubmatch(body, 64)
	// The page may reference many analytics bundles. Inspect a bounded number
	// of the last assets first, where SoundCloud's application bundle is often
	// emitted, rather than accepting the first unrelated matching string.
	if len(matches) > 32 {
		matches = matches[len(matches)-32:]
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if len(ids) >= maxClientIDCandidates {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		base, err := url.Parse(c.homeURL())
		if err != nil {
			continue
		}
		src, err := url.Parse(string(matches[i][1]))
		if err != nil {
			continue
		}
		assetURL := base.ResolveReference(src)
		if assetURL.Scheme != "https" && assetURL.Scheme != "http" {
			continue
		}
		asset, err := c.get(ctx, assetURL.String())
		if err != nil {
			continue
		}
		addIDs(asset)
	}
	// Use inline HTML IDs only as a fallback after checking current JS bundles.
	addIDs(body)
	if len(ids) == 0 {
		return nil, errors.New("could not discover a SoundCloud client ID; leave soundcloud_client_id empty for automatic discovery or set JABBERWOCK_SC_CLIENT_ID")
	}
	return ids, nil
}

func findClientIDs(data []byte) []string {
	text := string(data)
	matches := clientIDRE.FindAllStringSubmatch(text, -1)
	ids := make([]string, 0, len(matches)+1)
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		id := strings.TrimSpace(match[1])
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	// Some SoundCloud bundles serialize the key in a minified form that the
	// stricter expression above doesn't catch. Keep this fallback but dedupe it.
	for _, match := range clientIDLoose.FindAllStringSubmatch(text, -1) {
		if len(match) != 2 {
			continue
		}
		id := strings.TrimSpace(match[1])
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func findClientID(data []byte) string {
	ids := findClientIDs(data)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

type httpStatusError struct {
	Code   int
	Status string
	Host   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %s from %s", e.Status, e.Host)
}

func isAuthStatus(err error) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && (statusErr.Code == http.StatusUnauthorized || statusErr.Code == http.StatusForbidden)
}

func (c *Client) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/125 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/html, */*")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpStatusError{Code: resp.StatusCode, Status: resp.Status, Host: req.URL.Host}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, errors.New("response exceeds size limit")
	}
	return body, nil
}

// Search returns one page of playable SoundCloud tracks. nextURL should be the
// prior API-provided next_href, if any; the client validates its host before use.
func (c *Client) Search(ctx context.Context, query string, offset, limit int, nextURL string) (SearchPage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchPage{}, errors.New("enter a search query")
	}
	if offset < 0 {
		offset = 0
	}
	if limit < 1 || limit > 50 {
		limit = defaultLimit
	}
	var err error
	if strings.TrimSpace(nextURL) != "" {
		next, parseErr := url.Parse(nextURL)
		if parseErr != nil || (next.Scheme != "https" && next.Scheme != "http") {
			return SearchPage{}, errors.New("SoundCloud returned an invalid pagination URL")
		}
		base, _ := url.Parse(c.apiBase())
		if base == nil || !strings.EqualFold(next.Host, base.Host) {
			return SearchPage{}, errors.New("refusing unexpected host in SoundCloud pagination URL")
		}
	}

	ids, err := c.clientIDs(ctx, false)
	if err != nil {
		return SearchPage{}, err
	}
	tried := make(map[string]bool)
	var authErr error
	for refreshRound := 0; refreshRound < 2; refreshRound++ {
		for _, id := range ids {
			if tried[id] {
				continue
			}
			tried[id] = true
			endpoint, endpointErr := c.searchEndpoint(query, offset, limit, nextURL, id)
			if endpointErr != nil {
				return SearchPage{}, endpointErr
			}
			body, requestErr := c.get(ctx, endpoint.String())
			if requestErr != nil {
				if isAuthStatus(requestErr) {
					authErr = requestErr
					continue
				}
				return SearchPage{}, fmt.Errorf("SoundCloud search failed: %w", requestErr)
			}
			var response apiSearchResponse
			if err := json.Unmarshal(body, &response); err != nil {
				return SearchPage{}, fmt.Errorf("decode SoundCloud search response: %w", err)
			}
			c.rememberWorkingID(id, ids)
			return c.parseSearchPage(response, id, offset, limit), nil
		}
		// If all IDs discovered from our cached homepage have been rejected,
		// fetch the homepage and bundles again once in case SoundCloud rotated its
		// web client ID since the last search.
		if refreshRound == 0 && strings.TrimSpace(c.ClientID) == "" {
			ids, err = c.clientIDs(ctx, true)
			if err != nil {
				return SearchPage{}, err
			}
		} else {
			break
		}
	}
	if authErr != nil {
		if strings.TrimSpace(c.ClientID) != "" {
			return SearchPage{}, fmt.Errorf("SoundCloud rejected the configured client ID (%w); clear soundcloud_client_id to re-enable automatic discovery, or configure a currently valid ID", authErr)
		}
		return SearchPage{}, fmt.Errorf("SoundCloud rejected all discovered client IDs (%v). jabberwock refreshed soundcloud.com and tried up to %d IDs from its JavaScript bundles. A folder named client_id cannot fix a 401 because client_id must be a valid API value, not a directory; leave the setting blank for auto-discovery or configure a currently accepted ID. If this persists, SoundCloud may have changed or restricted this web API", authErr, maxClientIDCandidates)
	}
	return SearchPage{}, errors.New("SoundCloud search failed without a response; please retry")
}

func (c *Client) searchEndpoint(query string, offset, limit int, nextURL, clientID string) (*url.URL, error) {
	var endpoint *url.URL
	var err error
	if strings.TrimSpace(nextURL) != "" {
		endpoint, err = url.Parse(nextURL)
		if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
			return nil, errors.New("SoundCloud returned an invalid pagination URL")
		}
		base, _ := url.Parse(c.apiBase())
		if base == nil || !strings.EqualFold(endpoint.Host, base.Host) {
			return nil, errors.New("refusing unexpected host in SoundCloud pagination URL")
		}
	} else {
		endpoint, err = url.Parse(c.apiBase() + "/search/tracks")
		if err != nil {
			return nil, err
		}
		params := endpoint.Query()
		params.Set("q", query)
		params.Set("limit", strconv.Itoa(limit))
		params.Set("offset", strconv.Itoa(offset))
		params.Set("linked_partitioning", "1")
		endpoint.RawQuery = params.Encode()
	}
	params := endpoint.Query()
	params.Set("client_id", clientID)
	endpoint.RawQuery = params.Encode()
	return endpoint, nil
}

func (c *Client) rememberWorkingID(id string, candidates []string) {
	if strings.TrimSpace(c.ClientID) != "" || id == "" {
		return
	}
	ordered := make([]string, 0, len(candidates))
	ordered = append(ordered, id)
	for _, candidate := range candidates {
		if candidate != id {
			ordered = append(ordered, candidate)
		}
	}
	c.mu.Lock()
	c.cachedIDs = ordered
	c.mu.Unlock()
}

func (c *Client) parseSearchPage(response apiSearchResponse, clientID string, offset, limit int) SearchPage {
	tracks := make([]Track, 0, len(response.Collection))
	for _, item := range response.Collection {
		track, ok := c.parseTrack(item, clientID)
		if !ok || !Playable(track) {
			continue
		}
		tracks = append(tracks, track)
	}
	nextOffset := offset + len(response.Collection)
	hasMore := strings.TrimSpace(response.NextHref) != "" || len(response.Collection) >= limit
	return SearchPage{Tracks: tracks, HasMore: hasMore, NextOffset: nextOffset, NextURL: response.NextHref}
}

func (c *Client) parseTrack(item apiTrack, clientID string) (Track, bool) {
	id := strings.TrimSpace(string(item.ID))
	if len(id) > 0 && id[0] == '"' {
		_ = json.Unmarshal(item.ID, &id)
	}
	if id == "" || id == "null" {
		id = strings.TrimSpace(item.Urn)
	}
	if id == "" || strings.TrimSpace(item.Title) == "" {
		return Track{}, false
	}
	track := Track{
		ID: id, Title: strings.TrimSpace(item.Title), Username: strings.TrimSpace(item.User.Username),
		PermalinkURL: strings.TrimSpace(item.PermalinkURL), DurationMS: item.Duration,
		Policy: strings.ToUpper(strings.TrimSpace(item.Policy)), Access: strings.ToLower(strings.TrimSpace(item.Access)),
		MonetizationModel: strings.ToUpper(strings.TrimSpace(item.MonetizationModel)),
	}
	if item.PlaybackCount != nil {
		track.PlaybackCount = *item.PlaybackCount
		track.PlaybackCountKnown = true
	}
	track.CreatedAtText = strings.TrimSpace(item.CreatedAt)
	if item.CreatedAt != "" {
		for _, layout := range []string{time.RFC3339Nano, "2006/01/02 15:04:05 -0700"} {
			if parsed, err := time.Parse(layout, item.CreatedAt); err == nil {
				track.CreatedAt = parsed
				break
			}
		}
	}
	if transcode, ok := choosePlayableTranscoding(item.Media.Transcodings); ok {
		stream, err := url.Parse(transcode.URL)
		if err == nil && (stream.Scheme == "https" || stream.Scheme == "http") {
			params := stream.Query()
			params.Set("client_id", clientID)
			stream.RawQuery = params.Encode()
			track.StreamURL = stream.String()
		}
	}
	return track, true
}

// choosePlayableTranscoding prefers the unencrypted AAC HLS streams currently
// served by SoundCloud. Progressive HTTP was deprecated in late 2025; it is
// retained only as a compatibility fallback for older API responses. Never
// select encrypted/DRM protocols or preview/snippet-only transcodings.
func choosePlayableTranscoding(transcodings []apiTranscoding) (apiTranscoding, bool) {
	bestScore := int(^uint(0) >> 1)
	var best apiTranscoding
	for _, tc := range transcodings {
		endpoint := strings.TrimSpace(tc.URL)
		protocol := strings.ToLower(strings.TrimSpace(tc.Format.Protocol))
		preset := strings.ToLower(strings.TrimSpace(tc.Preset))
		mime := strings.ToLower(strings.TrimSpace(tc.Format.MimeType))
		if endpoint == "" || strings.Contains(protocol, "encrypted") || strings.Contains(strings.ToLower(endpoint), "encrypted-hls") {
			continue
		}
		if strings.Contains(preset, "preview") || strings.Contains(preset, "snip") || preset == "abr" {
			continue
		}
		if mime != "" && !strings.HasPrefix(mime, "audio/") && !strings.Contains(mime, "mpegurl") && !strings.Contains(mime, "playlist") {
			continue
		}

		score := 100
		switch protocol {
		case "hls", "hls-aac", "hls_aac":
			score = 20
			if strings.Contains(preset, "aac_160") || strings.Contains(preset, "aac-160") {
				score = 0
			} else if strings.Contains(preset, "aac_96") || strings.Contains(preset, "aac-96") {
				score = 1
			} else if strings.Contains(preset, "aac") {
				score = 2
			}
		case "progressive":
			if !strings.HasPrefix(mime, "audio/") {
				continue
			}
			score = 50
		default:
			continue
		}
		if score < bestScore {
			bestScore = score
			best = tc
		}
	}
	return best, bestScore != int(^uint(0)>>1)
}

// ResolveStreamURL resolves SoundCloud's media-transcoding API endpoint to
// its short-lived CDN URL or playlist. The URL in media.transcodings is an API
// endpoint returning JSON, not the audio itself; passing it to mpv directly
// makes mpv try to decode JSON as media. For older/direct URLs, it returns the
// original URL unchanged.
func (c *Client) ResolveStreamURL(ctx context.Context, endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("SoundCloud returned an invalid stream URL")
	}
	if !strings.Contains(u.Path, "/media/") || !strings.Contains(u.Path, "/stream/") {
		return u.String(), nil
	}
	base, err := url.Parse(c.apiBase())
	if err != nil || base.Host == "" || !strings.EqualFold(u.Host, base.Host) {
		return "", errors.New("refusing unexpected host in SoundCloud media endpoint")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/125 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/html, */*")
	client := *c.httpClient()
	// Keep redirects visible: some API deployments return JSON, while others
	// redirect straight to the signed CDN URL.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve SoundCloud media URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := strings.TrimSpace(resp.Header.Get("Location"))
		if location == "" {
			return "", fmt.Errorf("SoundCloud media endpoint returned %s without a redirect URL", resp.Status)
		}
		resolved, parseErr := u.Parse(location)
		if parseErr != nil || (resolved.Scheme != "https" && resolved.Scheme != "http") || resolved.Host == "" {
			return "", errors.New("SoundCloud returned an invalid redirect URL")
		}
		return resolved.String(), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &httpStatusError{Code: resp.StatusCode, Status: resp.Status, Host: req.URL.Host}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read SoundCloud media response: %w", err)
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.URL) != "" {
		resolved, parseErr := url.Parse(strings.TrimSpace(payload.URL))
		if parseErr != nil || (resolved.Scheme != "https" && resolved.Scheme != "http") || resolved.Host == "" {
			return "", errors.New("SoundCloud returned an invalid resolved audio URL")
		}
		return resolved.String(), nil
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "audio/") || strings.Contains(contentType, "mpegurl") || strings.HasPrefix(strings.TrimSpace(string(body)), "#EXTM3U") {
		return u.String(), nil
	}
	return "", fmt.Errorf("SoundCloud media endpoint did not return a resolved URL (content type %q)", contentType)
}

// Playable rejects SoundCloud Go/subscriber-only, blocked, preview-only, DRM,
// and tracks without a supported unencrypted audio transcoding.
func Playable(track Track) bool {
	if track.ID == "" || track.Title == "" || track.StreamURL == "" {
		return false
	}
	switch strings.ToUpper(track.Policy) {
	case "BLOCK", "SNIP":
		return false
	}
	if track.Access != "" && track.Access != "playable" {
		return false
	}
	switch strings.ToUpper(track.MonetizationModel) {
	case "SUB_ONLY", "SUB_HIGH_TIER", "SUBSCRIBER_ONLY", "SOUNDCLOUD_GO":
		return false
	}
	return true
}

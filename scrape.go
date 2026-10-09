package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	youtubeOrigin    = "https://www.youtube.com"
	clientVersion    = "2.20260901.00.00"
	publicAPIKey     = "AIzaSyAO_FJ2SlqU8Q4STEHLGCilw_Y9_11qcW8"
	defaultInvidious = "https://yewtu.be"
)

type Video struct {
	ID       string
	Title    string
	Author   string
	Duration string
	Thumb    string
}

type Stream struct {
	VideoURL string
	AudioURL string
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func searchVideos(ctx context.Context, backend, instance, query string) ([]Video, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("enter a search query")
	}
	switch strings.ToLower(backend) {
	case "invidious":
		return searchInvidious(ctx, instance, query)
	case "local", "builtin", "built-in", "":
		return searchYouTube(ctx, query)
	default:
		return nil, fmt.Errorf("unknown backend %q (use local or invidious)", backend)
	}
}

func searchYouTube(ctx context.Context, query string) ([]Video, error) {
	payload := map[string]any{
		"context": map[string]any{"client": map[string]any{
			"clientName": "WEB", "clientVersion": clientVersion,
			"hl": "en", "gl": "US",
		}},
		"query": query,
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		youtubeOrigin+"/youtubei/v1/search?key="+publicAPIKey+"&prettyPrint=false", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	setYouTubeHeaders(req, "WEB")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("YouTube search failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, 12<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("YouTube search returned HTTP %d: %s", resp.StatusCode, shortBody(body))
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("invalid YouTube search response: %w", err)
	}
	videos := []Video{}
	seen := make(map[string]bool)
	walkSearch(root, &videos, seen)
	if len(videos) == 0 {
		return nil, errors.New("YouTube returned no video results; try Invidious mode or another query")
	}
	if len(videos) > 30 {
		videos = videos[:30]
	}
	return videos, nil
}

func setYouTubeHeaders(req *http.Request, client string) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", youtubeOrigin)
	req.Header.Set("Referer", youtubeOrigin+"/")
	if client == "ANDROID" {
		req.Header.Set("User-Agent", "com.google.android.youtube/21.26.364 (Linux; U; Android 11) gzip")
		req.Header.Set("X-YouTube-Client-Name", "3")
		req.Header.Set("X-YouTube-Client-Version", "21.26.364")
	} else {
		req.Header.Set("X-YouTube-Client-Name", "1")
		req.Header.Set("X-YouTube-Client-Version", clientVersion)
	}
}

func searchInvidious(ctx context.Context, instance, query string) ([]Video, error) {
	base := normalizeInstance(instance)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/search?q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BareTube/0.1 (+lightweight open-source client)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Invidious request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, 12<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Invidious returned HTTP %d: %s", resp.StatusCode, shortBody(body))
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("invalid Invidious response: %w", err)
	}
	out := make([]Video, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if textValue(row["type"]) != "video" {
			continue
		}
		id := textValue(row["videoId"])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		duration := ""
		if n, ok := numberValue(row["lengthSeconds"]); ok {
			duration = formatDuration(int64(n))
		}
		out = append(out, Video{ID: id, Title: textValue(row["title"]), Author: textValue(row["author"]), Duration: duration})
		if len(out) >= 30 {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Invidious returned no video results")
	}
	return out, nil
}

// walkSearch only emits video renderers; playlists, channels and continuation
// payloads are intentionally ignored to keep the client UI small and predictable.
func walkSearch(node any, out *[]Video, seen map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		for _, key := range []string{"videoRenderer", "gridVideoRenderer", "playlistVideoRenderer"} {
			if renderer, ok := value[key].(map[string]any); ok {
				id := textValue(renderer["videoId"])
				if id != "" && !seen[id] {
					seen[id] = true
					thumb := ""
					if tm, ok := renderer["thumbnail"].(map[string]any); ok {
						if arr, ok := tm["thumbnails"].([]any); ok && len(arr) > 0 {
							for _, item := range arr {
								if im, ok := item.(map[string]any); ok {
									if s := textValue(im["url"]); s != "" {
										thumb = s
									}
								}
							}
						}
					}
					length := textValue(renderer["lengthText"])
					if length == "" {
						if n, ok := numberValue(renderer["lengthSeconds"]); ok {
							length = formatDuration(int64(n))
						}
					}
					*out = append(*out, Video{ID: id, Title: textValue(renderer["title"]), Author: firstText(renderer["ownerText"], renderer["shortBylineText"], renderer["longBylineText"]), Duration: length, Thumb: thumb})
				}
			}
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "videoRenderer" || key == "gridVideoRenderer" || key == "playlistVideoRenderer" {
				continue
			}
			walkSearch(value[key], out, seen)
		}
	case []any:
		for _, child := range value {
			walkSearch(child, out, seen)
		}
	}
}

func firstText(values ...any) string {
	for _, v := range values {
		if s := textValue(v); s != "" {
			return s
		}
	}
	return ""
}

func textValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]any:
		if s, ok := x["simpleText"]; ok {
			return textValue(s)
		}
		if s, ok := x["text"]; ok {
			return textValue(s)
		}
		if arr, ok := x["runs"].([]any); ok {
			parts := make([]string, 0, len(arr))
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					if t := textValue(m["text"]); t != "" {
						parts = append(parts, t)
					}
				}
			}
			return strings.Join(parts, "")
		}
	}
	return ""
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func normalizeInstance(instance string) string {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = defaultInvidious
	}
	return strings.TrimRight(instance, "/")
}

func fetchStream(ctx context.Context, backend, instance, videoID, mode string) (Stream, error) {
	if !validVideoID(videoID) {
		return Stream{}, errors.New("invalid video ID")
	}
	if mode != "video" && mode != "audio" {
		return Stream{}, errors.New("mode must be video or audio")
	}
	switch strings.ToLower(backend) {
	case "invidious":
		return streamInvidious(ctx, instance, videoID, mode)
	case "local", "builtin", "built-in", "":
		return streamYouTube(ctx, videoID, mode)
	default:
		return Stream{}, fmt.Errorf("unknown backend %q", backend)
	}
}

func validVideoID(id string) bool {
	if len(id) < 6 || len(id) > 20 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func streamInvidious(ctx context.Context, instance, id, mode string) (Stream, error) {
	base := normalizeInstance(instance)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/videos/"+url.PathEscape(id), nil)
	if err != nil {
		return Stream{}, err
	}
	req.Header.Set("User-Agent", "BareTube/0.1 (+lightweight open-source client)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Stream{}, fmt.Errorf("Invidious video request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, 16<<20)
	if err != nil {
		return Stream{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Stream{}, fmt.Errorf("Invidious returned HTTP %d: %s", resp.StatusCode, shortBody(body))
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return Stream{}, fmt.Errorf("invalid Invidious video response: %w", err)
	}
	formats := mapSlice(data["formatStreams"])
	adaptive := mapSlice(data["adaptiveFormats"])
	if mode == "video" {
		if f := chooseFormat(formats, "muxed"); f != nil {
			if u := textValue(f["url"]); u != "" {
				return Stream{VideoURL: u}, nil
			}
		}
		video := chooseFormat(adaptive, "video")
		audio := chooseFormat(adaptive, "audio")
		if video != nil && textValue(video["url"]) != "" {
			if audio != nil {
				return Stream{VideoURL: textValue(video["url"]), AudioURL: textValue(audio["url"])}, nil
			}
			return Stream{VideoURL: textValue(video["url"])}, nil
		}
	} else {
		if a := chooseFormat(adaptive, "audio"); a != nil && textValue(a["url"]) != "" {
			return Stream{VideoURL: textValue(a["url"])}, nil
		}
		if f := chooseFormat(formats, "muxed"); f != nil && textValue(f["url"]) != "" {
			return Stream{VideoURL: textValue(f["url"])}, nil
		}
	}
	if hls := textValue(data["hlsUrl"]); hls != "" {
		return Stream{VideoURL: hls}, nil
	}
	return Stream{}, errors.New("this Invidious instance did not expose a usable stream URL; try another instance or local mode")
}

func streamYouTube(ctx context.Context, id, mode string) (Stream, error) {
	// WEB and Android clients occasionally expose different sets of direct URLs.
	for _, client := range []string{"WEB", "ANDROID"} {
		data, html, err := playerResponse(ctx, id, client)
		if err != nil && data == nil {
			continue
		}
		if data == nil && html != "" {
			data = parseInitialPlayerResponse(html)
		}
		if data == nil {
			continue
		}
		streaming, _ := data["streamingData"].(map[string]any)
		if streaming == nil {
			continue
		}
		formats := append(mapSlice(streaming["formats"]), mapSlice(streaming["adaptiveFormats"])...)
		// A single player bundle fetch is shared across all ciphered formats.
		playerScript := ""
		for _, f := range formats {
			if textValue(f["url"]) == "" && (textValue(f["signatureCipher"]) != "" || textValue(f["cipher"]) != "") {
				if playerURL := findPlayerJS(html); playerURL != "" {
					playerScript, _ = fetchPlayerJS(ctx, playerURL)
				}
				break
			}
		}
		for i := range formats {
			if u := textValue(formats[i]["url"]); u == "" {
				cipher := textValue(formats[i]["signatureCipher"])
				if cipher == "" {
					cipher = textValue(formats[i]["cipher"])
				}
				if cipher != "" && playerScript != "" {
					formats[i]["url"] = cipherURLFromScript(cipher, playerScript)
				}
			}
		}
		formats = filterUsable(formats)
		if mode == "video" {
			if f := chooseFormat(formats, "muxed"); f != nil {
				if u := textValue(f["url"]); u != "" {
					return Stream{VideoURL: u}, nil
				}
			}
			v, a := chooseFormat(formats, "video"), chooseFormat(formats, "audio")
			if v != nil && a != nil {
				return Stream{VideoURL: textValue(v["url"]), AudioURL: textValue(a["url"])}, nil
			}
			if v != nil {
				return Stream{VideoURL: textValue(v["url"])}, nil
			}
		} else {
			if a := chooseFormat(formats, "audio"); a != nil {
				return Stream{VideoURL: textValue(a["url"])}, nil
			}
			if f := chooseFormat(formats, "muxed"); f != nil {
				return Stream{VideoURL: textValue(f["url"])}, nil
			}
		}
	}
	return Stream{}, errors.New("YouTube did not provide a directly playable stream. Try Settings → Invidious API; direct URLs and ciphered signatures vary with YouTube changes")
}

func playerResponse(ctx context.Context, id, client string) (map[string]any, string, error) {
	payload := map[string]any{"videoId": id, "contentCheckOk": true, "racyCheckOk": true}
	if client == "ANDROID" {
		payload["context"] = map[string]any{"client": map[string]any{"clientName": "ANDROID", "clientVersion": "21.26.364", "androidSdkVersion": 30, "osName": "Android", "osVersion": "11", "hl": "en", "gl": "US"}}
	} else {
		payload["context"] = map[string]any{"client": map[string]any{"clientName": "WEB", "clientVersion": clientVersion, "hl": "en", "gl": "US"}}
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, youtubeOrigin+"/youtubei/v1/player?key="+publicAPIKey+"&prettyPrint=false", bytes.NewReader(b))
	setYouTubeHeaders(req, client)
	resp, err := httpClient.Do(req)
	var data map[string]any
	if err == nil {
		defer resp.Body.Close()
		body, readErr := readLimited(resp.Body, 16<<20)
		if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = json.Unmarshal(body, &data)
		}
	}
	// Fetch HTML only when the API lacks streams or signatureCipher needs the
	// player bundle. This avoids downloading a large page for normal direct URLs.
	needsHTML := data == nil
	if data != nil {
		streaming, _ := data["streamingData"].(map[string]any)
		if streaming == nil {
			needsHTML = true
		} else {
			for _, f := range append(mapSlice(streaming["formats"]), mapSlice(streaming["adaptiveFormats"])...) {
				if textValue(f["url"]) == "" && (textValue(f["signatureCipher"]) != "" || textValue(f["cipher"]) != "") {
					needsHTML = true
					break
				}
			}
		}
	}
	html := ""
	if needsHTML {
		page, pageErr := fetchWatchPage(ctx, id)
		if pageErr == nil {
			html = page
			pageData := parseInitialPlayerResponse(html)
			if data == nil || (data["streamingData"] == nil && pageData != nil) {
				data = pageData
			}
		} else if data == nil {
			return nil, "", fmt.Errorf("player API: %v; watch page: %v", err, pageErr)
		}
	}
	return data, html, nil
}

func fetchWatchPage(ctx context.Context, id string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, youtubeOrigin+"/watch?v="+url.QueryEscape(id), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, 24<<20)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(body), nil
}

func parseInitialPlayerResponse(html string) map[string]any {
	for _, marker := range []string{"ytInitialPlayerResponse", "player_response"} {
		start := 0
		for {
			i := strings.Index(html[start:], marker)
			if i < 0 {
				break
			}
			i += start
			brace := strings.IndexByte(html[i+len(marker):], '{')
			if brace < 0 {
				break
			}
			pos := i + len(marker) + brace
			if raw := balancedJSON(html, pos); raw != "" {
				var obj map[string]any
				if json.Unmarshal([]byte(raw), &obj) == nil && obj != nil {
					return obj
				}
			}
			start = pos + 1
			if start >= len(html) {
				break
			}
		}
	}
	return nil
}

func balancedJSON(s string, start int) string {
	if start >= len(s) || s[start] != '{' {
		return ""
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '{' {
			depth++
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func unencryptedCipherURL(ctx context.Context, cipher, html string) string {
	playerURL := findPlayerJS(html)
	if playerURL == "" {
		return ""
	}
	script, err := fetchPlayerJS(ctx, playerURL)
	if err != nil {
		return ""
	}
	return cipherURLFromScript(cipher, script)
}

func cipherURLFromScript(cipher, script string) string {
	values, err := url.ParseQuery(cipher)
	if err != nil {
		return ""
	}
	u := values.Get("url")
	if u == "" {
		return ""
	}
	sig := values.Get("s")
	decoded := values.Get("sig") // Some formats ship an already-deciphered signature.
	if sig != "" {
		var ok bool
		decoded, ok = decipherSignature(script, sig)
		if !ok {
			return ""
		}
	}
	if decoded == "" {
		return u
	}
	param := values.Get("sp")
	if param == "" {
		param = "signature"
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	q := parsed.Query()
	q.Set(param, decoded)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func findPlayerJS(html string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`"jsUrl"\s*:\s*"([^"]+base\.js[^"]*)"`),
		regexp.MustCompile(`"PLAYER_JS_URL"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`(?:src|href)="([^"]*/s/player/[^" ]+/[^" ]*base\.js[^"]*)"`),
	}
	for _, re := range patterns {
		if m := re.FindStringSubmatch(html); len(m) > 1 {
			raw := strings.ReplaceAll(m[1], `\/`, `/`)
			if strings.HasPrefix(raw, "//") {
				return "https:" + raw
			}
			if strings.HasPrefix(raw, "/") {
				return youtubeOrigin + raw
			}
			if strings.HasPrefix(raw, "http") {
				return raw
			}
		}
	}
	return ""
}

func fetchPlayerJS(ctx context.Context, playerURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, playerURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("player JS HTTP %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, 8<<20)
	return string(body), err
}

// decipherSignature handles the common reverse / drop / swap operations used
// by YouTube player bundles. It deliberately has no JavaScript runtime or deps.
func decipherSignature(script, sig string) (string, bool) {
	assignRE := regexp.MustCompile(`([\w$]+)\s*=\s*function\((\w+)\)\s*\{\s*[\w$]+\s*=\s*[\w$]+\.split\(""\);(.*?)return\s+[\w$]+\.join\(""\)\s*\}`)
	funcRE := regexp.MustCompile(`function\s+([\w$]+)\((\w+)\)\s*\{\s*[\w$]+\s*=\s*[\w$]+\.split\(""\);(.*?)return\s+[\w$]+\.join\(""\)\s*\}`)
	var body string
	for _, re := range []*regexp.Regexp{assignRE, funcRE} {
		for _, m := range re.FindAllStringSubmatch(script, 300) {
			if len(m) >= 4 && signatureFunctionReferenced(script, m[1]) {
				body = m[3]
				break
			}
		}
		if body != "" {
			break
		}
	}
	if body == "" {
		return "", false
	}
	ops := parseSignatureOps(script, body)
	chars := []rune(sig)
	callRE := regexp.MustCompile(`([\w$]+)\.([\w$]+)\(\w+(?:,(\d+))?\)|\w+\.reverse\(\)|\w+\.splice\(0,(\d+)\)`)
	matches := callRE.FindAllStringSubmatch(body, -1)
	applied := false
	for _, m := range matches {
		if strings.HasSuffix(m[0], ".reverse()") {
			reverseRunes(chars)
			applied = true
			continue
		}
		if strings.Contains(m[0], ".splice(0,") {
			n, _ := strconv.Atoi(m[4])
			chars = dropRunes(chars, n)
			applied = true
			continue
		}
		op, ok := ops[m[1]+"."+m[2]]
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(m[3])
		switch op {
		case "reverse":
			reverseRunes(chars)
			applied = true
		case "drop":
			chars = dropRunes(chars, n)
			applied = true
		case "swap":
			if len(chars) > 0 {
				i := n % len(chars)
				chars[0], chars[i] = chars[i], chars[0]
				applied = true
			}
		}
	}
	if !applied {
		return "", false
	}
	return string(chars), true
}

func signatureFunctionReferenced(script, name string) bool {
	if name == "" {
		return false
	}
	patterns := []string{`\.sig\|\|` + regexp.QuoteMeta(name) + `\(`, `signature["']?\s*,\s*` + regexp.QuoteMeta(name) + `\(`, `\.set\(["']signature["']\s*,\s*` + regexp.QuoteMeta(name) + `\(`}
	for _, p := range patterns {
		if regexp.MustCompile(p).FindStringIndex(script) != nil {
			return true
		}
	}
	return false
}

func parseSignatureOps(script, body string) map[string]string {
	ops := map[string]string{}
	objectRE := regexp.MustCompile(`([\w$]+)\s*=\s*\{`)
	helperRE := regexp.MustCompile(`(?:^|,)\s*([\w$]+)\s*:\s*function\(([^)]*)\)\s*\{([^{}]*)\}`)
	for _, om := range objectRE.FindAllStringSubmatchIndex(script, -1) {
		name := script[om[2]:om[3]]
		used := false
		for _, call := range regexp.MustCompile(regexp.QuoteMeta(name)+`\.([\w$]+)\(`).FindAllStringSubmatch(body, -1) {
			if len(call) > 1 {
				used = true
			}
		}
		if !used {
			continue
		}
		start := om[1] - 1
		end := matchingBrace(script, start)
		if end <= start {
			continue
		}
		obj := script[start+1 : end]
		for _, hm := range helperRE.FindAllStringSubmatch(obj, -1) {
			key, code := name+"."+hm[1], hm[3]
			switch {
			case strings.Contains(code, ".reverse("):
				ops[key] = "reverse"
			case strings.Contains(code, ".splice(0,") || strings.Contains(code, ".slice("):
				ops[key] = "drop"
			case strings.Contains(code, "a[0]") && strings.Contains(code, "%a.length"):
				ops[key] = "swap"
			}
		}
	}
	return ops
}

func matchingBrace(s string, start int) int {
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' || c == '\'' || c == '`' {
				inString = false
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			inString = true
			continue
		}
		if c == '{' {
			depth++
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func reverseRunes(v []rune) {
	for i, j := 0, len(v)-1; i < j; i, j = i+1, j-1 {
		v[i], v[j] = v[j], v[i]
	}
}
func dropRunes(v []rune, n int) []rune {
	if n < 0 {
		n = 0
	}
	if n > len(v) {
		n = len(v)
	}
	return v[n:]
}

func filterUsable(formats []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(formats))
	for _, f := range formats {
		if textValue(f["url"]) != "" {
			out = append(out, f)
		}
	}
	return out
}

func mapSlice(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func chooseFormat(formats []map[string]any, wanted string) map[string]any {
	candidates := make([]map[string]any, 0, len(formats))
	hasAudioOnly := false
	if wanted == "audio" {
		for _, f := range formats {
			typ := strings.ToLower(textValue(f["type"]))
			mime := strings.ToLower(textValue(f["mimeType"]))
			hasVideo := strings.Contains(typ, "video/") || strings.Contains(mime, "video/") || numberIsPresent(f["height"])
			hasAudio := looksLikeAudio(f, typ, mime)
			if hasAudio && !hasVideo {
				hasAudioOnly = true
				break
			}
		}
	}
	for _, f := range formats {
		typ := strings.ToLower(textValue(f["type"]))
		mime := strings.ToLower(textValue(f["mimeType"]))
		hasVideo := strings.Contains(typ, "video/") || strings.Contains(mime, "video/") || numberIsPresent(f["height"])
		hasAudio := looksLikeAudio(f, typ, mime)
		switch wanted {
		case "audio":
			if !hasAudio || (hasAudioOnly && hasVideo) {
				continue
			}
		case "video":
			if !hasVideo { // Video-only adaptive formats are valid when audio is selected separately.
				continue
			}
		case "muxed":
			if hasVideo && hasAudio {
				candidates = append(candidates, f)
				continue
			}
			if strings.Contains(strings.ToLower(textValue(f["qualityLabel"])), "p") && strings.Contains(typ, "video/mp4; codecs=") {
				candidates = append(candidates, f)
				continue
			}
			continue
		}
		candidates = append(candidates, f)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return formatScore(candidates[i]) > formatScore(candidates[j]) })
	// Avoid huge streams as a default; 720p is ample for a tiny client.
	best := candidates[0]
	for _, f := range candidates {
		h := formatHeight(f)
		if h > 0 && h <= 720 {
			return f
		}
	}
	return best
}

func looksLikeAudio(f map[string]any, typ, mime string) bool {
	return strings.Contains(typ, "audio/") || strings.Contains(mime, "audio/") || textValue(f["audioQuality"]) != "" ||
		strings.Contains(typ, "mp4a") || strings.Contains(mime, "mp4a") || strings.Contains(typ, "opus") || strings.Contains(mime, "opus") ||
		strings.Contains(typ, "vorbis") || strings.Contains(mime, "vorbis") || strings.Contains(typ, "aac") || strings.Contains(mime, "aac")
}

func numberIsPresent(v any) bool { n, ok := numberValue(v); return ok && n > 0 }
func formatHeight(f map[string]any) int {
	if n, ok := numberValue(f["height"]); ok {
		return int(n)
	}
	for _, key := range []string{"qualityLabel", "quality", "encoding"} {
		s := textValue(f[key])
		re := regexp.MustCompile(`([0-9]{3,4})p?`)
		if m := re.FindStringSubmatch(s); len(m) > 1 {
			n, _ := strconv.Atoi(m[1])
			if n >= 144 && n <= 4320 {
				return n
			}
		}
	}
	return 0
}
func formatScore(f map[string]any) float64 {
	if n, ok := numberValue(f["bitrate"]); ok {
		return n
	}
	if n, ok := numberValue(f["averageBitrate"]); ok {
		return n
	}
	if n, ok := numberValue(f["width"]); ok {
		return n * 1000
	}
	return float64(formatHeight(f)) * 1000
}
func formatDuration(seconds int64) string {
	if seconds < 0 {
		return ""
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeded %d bytes", limit)
	}
	return data, nil
}
func shortBody(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 180 {
		s = s[:180] + "…"
	}
	return s
}

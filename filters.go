package main

import (
	"sort"
	"strings"
	"time"
)

type Filters struct {
	Sort       string // relevance, views, date
	Date       string // any, hour, today, week, month, year
	Duration   string // any, short (<4m), medium (4-20m), long (>20m)
	HideShorts bool
}

func DefaultFilters() Filters {
	return Filters{Sort: "relevance", Date: "any", Duration: "any", HideShorts: true}
}

type filterOption struct {
	Name   string
	Values []string
	Labels []string
}

var filterOptions = []filterOption{
	{Name: "Sort", Values: []string{"relevance", "views", "date"}, Labels: []string{"Relevance", "Most viewed", "Newest first"}},
	{Name: "Uploaded", Values: []string{"any", "hour", "today", "week", "month", "year"}, Labels: []string{"Any time", "Last hour", "Today", "This week", "This month", "This year"}},
	{Name: "Duration", Values: []string{"any", "short", "medium", "long"}, Labels: []string{"Any length", "Under 4 minutes", "4–20 minutes", "Over 20 minutes"}},
	{Name: "Hide Shorts", Values: []string{"on", "off"}, Labels: []string{"On", "Off"}},
}

func (f Filters) valueAt(index int) string {
	switch index {
	case 0:
		return f.Sort
	case 1:
		return f.Date
	case 2:
		return f.Duration
	case 3:
		if f.HideShorts {
			return "on"
		}
		return "off"
	default:
		return ""
	}
}

func (f *Filters) cycle(index int) {
	if index < 0 || index >= len(filterOptions) {
		return
	}
	option := filterOptions[index]
	current := f.valueAt(index)
	pos := 0
	for i, value := range option.Values {
		if value == current {
			pos = i
			break
		}
	}
	next := option.Values[(pos+1)%len(option.Values)]
	switch index {
	case 0:
		f.Sort = next
	case 1:
		f.Date = next
	case 2:
		f.Duration = next
	case 3:
		f.HideShorts = next == "on"
	}
}

func (f Filters) labelAt(index int) string {
	if index < 0 || index >= len(filterOptions) {
		return ""
	}
	current := f.valueAt(index)
	option := filterOptions[index]
	for i, value := range option.Values {
		if value == current && i < len(option.Labels) {
			return option.Labels[i]
		}
	}
	return current
}

// FilterAndSort applies the same post-filtering to both backends. For upload dates
// parsed from YouTube text, the time is approximate; Invidious timestamps are exact.
func FilterAndSort(videos []Video, f Filters) []Video {
	now := time.Now()
	out := make([]Video, 0, len(videos))
	for _, video := range videos {
		if f.HideShorts && video.IsShort {
			continue
		}
		if f.Date != "any" {
			if !video.PublishedKnown || !recentEnough(video.Published, f.Date, now) {
				continue
			}
		}
		if f.Duration != "any" {
			if !video.DurationKnown || !durationMatches(video.DurationSeconds, f.Duration) {
				continue
			}
		}
		out = append(out, video)
	}
	switch f.Sort {
	case "views":
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].ViewsKnown != out[j].ViewsKnown {
				return out[i].ViewsKnown
			}
			return out[i].Views > out[j].Views
		})
	case "date":
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].PublishedKnown != out[j].PublishedKnown {
				return out[i].PublishedKnown
			}
			return out[i].Published.After(out[j].Published)
		})
	}
	return out
}

func recentEnough(published time.Time, period string, now time.Time) bool {
	age := now.Sub(published)
	if age < 0 {
		return true
	}
	var span time.Duration
	switch period {
	case "hour":
		span = time.Hour
	case "today":
		span = 24 * time.Hour
	case "week":
		span = 7 * 24 * time.Hour
	case "month":
		span = 30 * 24 * time.Hour
	case "year":
		span = 365 * 24 * time.Hour
	default:
		return true
	}
	return age <= span
}

func durationMatches(seconds int64, kind string) bool {
	switch strings.ToLower(kind) {
	case "short":
		return seconds < 4*60
	case "medium":
		return seconds >= 4*60 && seconds <= 20*60
	case "long":
		return seconds > 20*60
	default:
		return true
	}
}

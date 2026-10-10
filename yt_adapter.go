package main

import (
	"context"

	"jabberwock/yt"
)

// SearchCursor keeps backend-specific continuation state together without
// coupling the terminal UI to YouTube's response structures.
type SearchCursor struct {
	Token             string `json:"token,omitempty"`
	APIKey            string `json:"api_key,omitempty"`
	Context           []byte `json:"context,omitempty"`
	SoundCloudOffset  int    `json:"soundcloud_offset,omitempty"`
	SoundCloudNextURL string `json:"soundcloud_next_url,omitempty"`
}

type SearchPage struct {
	Videos  []Video
	Cursor  SearchCursor
	HasMore bool
}

func SearchVideosPage(ctx context.Context, cfg Config, query string, filters Filters, page int, cursor SearchCursor) (SearchPage, error) {
	result, err := yt.SearchVideosPage(ctx, yt.Config{Backend: cfg.Backend, InvidiousURL: cfg.InvidiousURL}, query, yt.Filters{
		Sort: filters.Sort, Date: filters.Date, Duration: filters.Duration, HideShorts: filters.HideShorts,
	}, page, yt.SearchCursor{Token: cursor.Token, APIKey: cursor.APIKey, Context: cursor.Context})
	if err != nil {
		return SearchPage{}, err
	}
	videos := make([]Video, 0, len(result.Videos))
	for _, v := range result.Videos {
		videos = append(videos, Video{
			Title: v.Title, Author: v.Author, VideoID: v.VideoID, WatchURL: v.WatchURL,
			DurationSeconds: v.DurationSeconds, DurationKnown: v.DurationKnown, Views: v.Views,
			ViewsKnown: v.ViewsKnown, Published: v.Published, PublishedKnown: v.PublishedKnown,
			PublishedText: v.PublishedText, IsShort: v.IsShort,
		})
	}
	return SearchPage{Videos: FilterAndSort(videos, filters), Cursor: SearchCursor{Token: result.Cursor.Token, APIKey: result.Cursor.APIKey, Context: result.Cursor.Context}, HasMore: result.HasMore}, nil
}

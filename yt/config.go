// Package yt contains jabberwock's YouTube HTML and Invidious search backends.
package yt

// Config contains the YouTube scraper settings needed by the backend.
type Config struct {
	Backend      string
	InvidiousURL string
}

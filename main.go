package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	switch os.Args[1] {
	case "search":
		if len(os.Args) < 5 {
			usage()
		}
		videos, err := searchVideos(ctx, os.Args[2], os.Args[3], strings.Join(os.Args[4:], " "))
		if err != nil {
			fail(err)
		}
		for _, v := range videos {
			fmt.Printf("%s\t%s\t%s\t%s\n", cleanField(v.ID), cleanField(v.Title), cleanField(v.Author), cleanField(v.Duration))
		}
	case "stream":
		if len(os.Args) != 6 {
			usage()
		}
		result, err := fetchStream(ctx, os.Args[2], os.Args[3], os.Args[4], os.Args[5])
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s\t%s\n", result.VideoURL, result.AudioURL)
	case "version":
		fmt.Println("BareTube scraper 0.1.0")
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  baretube-backend search <local|invidious> <instance-url-or-empty> <query>")
	fmt.Fprintln(os.Stderr, "  baretube-backend stream <local|invidious> <instance-url-or-empty> <video-id> <video|audio>")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "baretube:", err)
	os.Exit(1)
}

func cleanField(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

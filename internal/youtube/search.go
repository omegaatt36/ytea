// Package youtube finds tracks on YouTube through yt-dlp.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrEmptyQuery is returned when a search is requested without any keywords.
var ErrEmptyQuery = errors.New("empty search query")

// Track is a single playable YouTube entry.
type Track struct {
	ID       string
	Title    string
	Channel  string
	URL      string
	Duration time.Duration
	Live     bool
}

// ThumbnailURL returns a small fixed-size JPEG thumbnail.
// WHY: mqdefault is always JPEG (320x180); the URLs from search results may be
// WebP or carry signed query strings that expire.
func (t Track) ThumbnailURL() string {
	return "https://i.ytimg.com/vi/" + t.ID + "/mqdefault.jpg"
}

// Searcher runs yt-dlp searches.
type Searcher struct {
	bin string
}

// NewSearcher returns a Searcher that invokes the given yt-dlp binary.
func NewSearcher(bin string) *Searcher {
	return &Searcher{bin: bin}
}

// Search returns up to limit tracks matching query.
func (s *Searcher) Search(ctx context.Context, query string, limit int) ([]Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrEmptyQuery
	}

	// --flat-playlist skips per-video extraction, which turns a ~10s search into ~1s.
	cmd := exec.CommandContext(ctx, s.bin,
		fmt.Sprintf("ytsearch%d:%s", limit, query),
		"--flat-playlist", "--dump-single-json", "--no-warnings",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("run yt-dlp search %q: %w: %s", query, err, msg)
		}
		return nil, fmt.Errorf("run yt-dlp search %q: %w", query, err)
	}

	tracks, err := parseSearch(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("parse yt-dlp search %q: %w", query, err)
	}
	return tracks, nil
}

type searchResult struct {
	Entries []searchEntry `json:"entries"`
}

type searchEntry struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Channel    string  `json:"channel"`
	Uploader   string  `json:"uploader"`
	Duration   float64 `json:"duration"`
	LiveStatus string  `json:"live_status"`
	IEKey      string  `json:"ie_key"`
}

func parseSearch(data []byte) ([]Track, error) {
	var res searchResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}

	tracks := make([]Track, 0, len(res.Entries))
	for _, e := range res.Entries {
		// Channels and playlists can show up in search results; only videos are playable tracks.
		if e.ID == "" || (e.IEKey != "" && e.IEKey != "Youtube") {
			continue
		}
		tracks = append(tracks, trackFromEntry(e))
	}
	return tracks, nil
}

func trackFromEntry(e searchEntry) Track {
	channel := e.Channel
	if channel == "" {
		channel = e.Uploader
	}
	url := e.URL
	if url == "" {
		url = "https://www.youtube.com/watch?v=" + e.ID
	}
	return Track{
		ID:       e.ID,
		Title:    e.Title,
		Channel:  channel,
		URL:      url,
		Duration: time.Duration(e.Duration * float64(time.Second)),
		Live:     e.LiveStatus == "is_live",
	}
}

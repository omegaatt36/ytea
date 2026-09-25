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

// MaxPlaylistItems is the default number of videos to import from a playlist.
const MaxPlaylistItems = 200

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
	return s.extract(ctx, "search", fmt.Sprintf("ytsearch%d:%s", limit, query), 0)
}

// Lookup returns the videos behind a URL from RefOf. Non-positive limits use
// MaxPlaylistItems; positive limits stop the listing at the requested count.
func (s *Searcher) Lookup(ctx context.Context, url string, limit int) ([]Track, error) {
	if limit <= 0 {
		limit = MaxPlaylistItems
	}
	tracks, err := s.extract(ctx, "url", url, limit)
	if err != nil {
		return nil, err
	}
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}
	return tracks, nil
}

func (s *Searcher) extract(ctx context.Context, what, target string, items int) ([]Track, error) {
	// Flat extraction skips per-video stream resolution: ~1s instead of ~10s.
	args := []string{target, "--flat-playlist", "--dump-single-json", "--no-warnings"}
	if items > 0 {
		args = append(args, "--playlist-items", fmt.Sprintf("1:%d", items))
	}
	cmd := exec.CommandContext(ctx, s.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("run yt-dlp %s %q: %w: %s", what, target, err, msg)
		}
		return nil, fmt.Errorf("run yt-dlp %s %q: %w", what, target, err)
	}
	tracks, err := parseTracks(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("parse yt-dlp %s %q: %w", what, target, err)
	}
	return tracks, nil
}

// flatDump is a yt-dlp dump: entries for playlists, or a bare video at the top level.
type flatDump struct {
	flatEntry
	Type    string      `json:"_type"`
	Entries []flatEntry `json:"entries"`
}

type flatEntry struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Channel    string  `json:"channel"`
	Uploader   string  `json:"uploader"`
	Duration   float64 `json:"duration"`
	LiveStatus string  `json:"live_status"`
	IEKey      string  `json:"ie_key"`
}

func parseTracks(data []byte) ([]Track, error) {
	var res flatDump
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}

	if len(res.Entries) > 0 {
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
	// A bare video dumps itself at the top level.
	if res.Type == "video" && res.ID != "" {
		return []Track{trackFromEntry(res.flatEntry)}, nil
	}
	return []Track{}, nil
}

func trackFromEntry(e flatEntry) Track {
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

package domain

import "time"

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

// Link is search-box input recognized as a YouTube URL rather than keywords.
type Link struct {
	URL string
	Mix bool
}

// AccountPlaylist is a playlist owned by the authenticated channel, or its liked videos.
type AccountPlaylist struct {
	ID    string
	Title string
	URL   string
}

package youtube

import (
	"net/url"
	"strings"
)

// Link is search-box input recognized as a YouTube URL rather than keywords.
type Link struct {
	URL string
	Mix bool
}

// RefOf recognizes a YouTube link in search-box input and returns it in the
// form yt-dlp resolves.
func RefOf(query string) (Link, bool) {
	u, err := url.Parse(strings.TrimSpace(query))
	if err != nil || !youTubeHost(strings.ToLower(u.Host)) {
		return Link{}, false
	}
	if list := u.Query().Get("list"); list != "" {
		// Mixes resolve only next to their video; yt-dlp rejects playlist?list=RD.
		if v := u.Query().Get("v"); v != "" && isMixList(list) {
			return Link{URL: "https://www.youtube.com/watch?v=" + v + "&list=" + list, Mix: true}, true
		}
		return Link{URL: "https://www.youtube.com/playlist?list=" + list, Mix: isMixList(list)}, true
	}
	if v := videoID(u); v != "" {
		return Link{URL: "https://www.youtube.com/watch?v=" + v}, true
	}
	return Link{}, false
}

func videoID(u *url.URL) string {
	switch {
	case u.Path == "/watch":
		return u.Query().Get("v")
	case u.Host == "youtu.be":
		return tailSegment(u.Path)
	case strings.HasPrefix(u.Path, "/shorts/"):
		return tailSegment(strings.TrimPrefix(u.Path, "/shorts/"))
	}
	return ""
}

func tailSegment(path string) string {
	segment := strings.Trim(path, "/")
	if segment == "" || strings.Contains(segment, "/") {
		return ""
	}
	return segment
}

func youTubeHost(host string) bool {
	host = strings.TrimSuffix(host, ".") // fully qualified spelling
	return host == "youtu.be" || host == "youtube.com" ||
		strings.HasSuffix(host, ".youtube.com")
}

func isMixList(list string) bool {
	return strings.HasPrefix(list, "RD") ||
		strings.HasPrefix(list, "OLAK5uy_") ||
		strings.HasPrefix(list, "UL")
}

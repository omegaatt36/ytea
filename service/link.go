package service

import (
	"net/url"
	"strings"

	"github.com/omegaatt36/ytea/domain"
)

func linkOf(query string) (domain.Link, bool) {
	u, err := url.Parse(strings.TrimSpace(query))
	if err != nil || !youTubeHost(strings.ToLower(u.Host)) {
		return domain.Link{}, false
	}
	if list := u.Query().Get("list"); list != "" {
		// Mixes resolve only next to their video; yt-dlp rejects playlist?list=RD.
		if v := u.Query().Get("v"); v != "" && isMixList(list) {
			return domain.Link{URL: "https://www.youtube.com/watch?v=" + v + "&list=" + list, Mix: true}, true
		}
		return domain.Link{URL: "https://www.youtube.com/playlist?list=" + list, Mix: isMixList(list)}, true
	}
	if v := videoID(u); v != "" {
		return domain.Link{URL: "https://www.youtube.com/watch?v=" + v}, true
	}
	return domain.Link{}, false
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

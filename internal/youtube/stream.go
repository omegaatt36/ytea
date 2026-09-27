package youtube

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stream is the YouTube media format behind a URL yt-dlp resolved for playback.
type Stream struct {
	Itag     string // yt-dlp's format id, e.g. "251" (Opus) or "140" (AAC)
	MIME     string // e.g. "audio/webm"
	Size     int64  // bytes; 0 when unknown
	Duration time.Duration
}

// Bitrate is the average bitrate in bits per second, or 0 when unknown.
func (s Stream) Bitrate() int {
	if s.Size <= 0 || s.Duration <= 0 {
		return 0
	}
	return int(float64(s.Size*8) / s.Duration.Seconds())
}

// StreamOf finds the googlevideo media URL in what mpv opened, either the URL
// itself or an edl:// wrapper around it, and reads the format from its query.
func StreamOf(opened string) (Stream, bool) {
	for rest := opened; ; {
		i := strings.Index(rest, "https://")
		if i < 0 {
			return Stream{}, false
		}
		rest = rest[i:]
		// ';' separates EDL segments; the media URL escapes its own.
		raw, _, _ := strings.Cut(rest, ";")
		rest = rest[len("https://"):]
		u, err := url.Parse(raw)
		if err != nil || !strings.HasSuffix(u.Hostname(), ".googlevideo.com") {
			continue
		}
		q := u.Query()
		if q.Get("itag") == "" {
			continue
		}
		size, _ := strconv.ParseInt(q.Get("clen"), 10, 64)
		dur, _ := strconv.ParseFloat(q.Get("dur"), 64)
		return Stream{
			Itag:     q.Get("itag"),
			MIME:     q.Get("mime"),
			Size:     size,
			Duration: time.Duration(dur * float64(time.Second)),
		}, true
	}
}

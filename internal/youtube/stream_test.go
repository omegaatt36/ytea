package youtube

import (
	"testing"
	"time"
)

func TestStreamOf(t *testing.T) {
	media := "https://rr1---sn-abc.googlevideo.com/videoplayback?expire=1&itag=774&mime=audio%2Fwebm&clen=6852699&dur=213.061&sig=AE0s%3D"
	tests := []struct {
		name   string
		opened string
		want   Stream
		ok     bool
	}{
		{
			name:   "edl wrapper",
			opened: "edl://!new_stream;!no_clip;!no_chapters;%1207%" + media + ";!global_tags,ytdl_description=%5%https",
			want:   Stream{Itag: "774", MIME: "audio/webm", Size: 6852699, Duration: 213061 * time.Millisecond},
			ok:     true,
		},
		{
			name:   "direct url",
			opened: media,
			want:   Stream{Itag: "774", MIME: "audio/webm", Size: 6852699, Duration: 213061 * time.Millisecond},
			ok:     true,
		},
		{
			name:   "skips non-googlevideo urls",
			opened: "edl://%20%https://example.com/a.ogg;%" + "https://rr2.googlevideo.com/videoplayback?itag=140",
			want:   Stream{Itag: "140"},
			ok:     true,
		},
		{name: "watch url", opened: "https://www.youtube.com/watch?v=abc"},
		{name: "local file", opened: "/music/a.flac"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := StreamOf(tt.opened)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("StreamOf() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestStreamBitrate(t *testing.T) {
	s := Stream{Size: 6852699, Duration: 213061 * time.Millisecond}
	if got := s.Bitrate(); got/1000 != 257 {
		t.Fatalf("Bitrate() = %d, want ~257 kbps", got)
	}
	if got := (Stream{Size: 1}).Bitrate(); got != 0 {
		t.Fatalf("Bitrate() without duration = %d, want 0", got)
	}
}

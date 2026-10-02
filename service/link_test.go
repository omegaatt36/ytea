package service

import (
	"testing"

	"github.com/omegaatt36/ytea/domain"
)

func TestLinkOf(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  domain.Link
		ok    bool
	}{
		{
			name:  "watch page",
			query: "https://www.youtube.com/watch?v=abc&pp=ygU",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc"},
			ok:    true,
		},
		{
			name:  "playlist wins over its video",
			query: "https://www.youtube.com/watch?v=abc&list=PL123",
			want:  domain.Link{URL: "https://www.youtube.com/playlist?list=PL123"},
			ok:    true,
		},
		{
			name:  "mix keeps its video",
			query: "https://www.youtube.com/watch?v=abc&list=RDabc",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc&list=RDabc", Mix: true},
			ok:    true,
		},
		{
			name:  "music mix",
			query: "https://music.youtube.com/watch?v=abc&list=RDAMVMabc",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc&list=RDAMVMabc", Mix: true},
			ok:    true,
		},
		{
			name:  "album mix",
			query: "https://www.youtube.com/watch?v=abc&list=OLAK5uy_kx",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc&list=OLAK5uy_kx", Mix: true},
			ok:    true,
		},
		{
			name:  "bare mix page still reads as a mix",
			query: "https://www.youtube.com/playlist?list=RDabc",
			want:  domain.Link{URL: "https://www.youtube.com/playlist?list=RDabc", Mix: true},
			ok:    true,
		},
		{
			name:  "playlist page",
			query: "https://www.youtube.com/playlist?list=PL123",
			want:  domain.Link{URL: "https://www.youtube.com/playlist?list=PL123"},
			ok:    true,
		},
		{
			name:  "youtu.be share link",
			query: "https://youtu.be/abc?si=share",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc"},
			ok:    true,
		},
		{
			name:  "shorts",
			query: "https://www.youtube.com/shorts/abc",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc"},
			ok:    true,
		},
		{
			name:  "mobile host",
			query: "http://m.youtube.com/watch?v=abc",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc"},
			ok:    true,
		},
		{
			name:  "upper case host",
			query: "https://WWW.YOUTUBE.COM/watch?v=abc",
			want:  domain.Link{URL: "https://www.youtube.com/watch?v=abc"},
			ok:    true,
		},
		{
			name:  "channel is not a link",
			query: "https://www.youtube.com/@lofigirl",
			want:  domain.Link{},
			ok:    false,
		},
		{
			name:  "bare host is not a link",
			query: "https://www.youtube.com/",
			want:  domain.Link{},
			ok:    false,
		},
		{
			name:  "another site is keywords",
			query: "https://example.com/watch?v=abc",
			want:  domain.Link{},
			ok:    false,
		},
		{
			name:  "schemeless is keywords",
			query: "youtu.be/abc",
			want:  domain.Link{},
			ok:    false,
		},
		{
			name:  "keywords",
			query: "joe hisaishi",
			want:  domain.Link{},
			ok:    false,
		},
		{
			name:  "unparseable",
			query: "http://[::1",
			want:  domain.Link{},
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := linkOf(tt.query)
			if ok != tt.ok || got != tt.want {
				t.Errorf("linkOf(%q) = %+v, %v, want %+v, %v", tt.query, got, ok, tt.want, tt.ok)
			}
		})
	}
}

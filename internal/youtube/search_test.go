package youtube

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestParseSearch(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []Track
	}{
		{
			name: "video with channel",
			data: `{"entries":[{"ie_key":"Youtube","id":"abc","title":"Song","url":"https://www.youtube.com/watch?v=abc","channel":"Artist","duration":212.5}]}`,
			want: []Track{{ID: "abc", Title: "Song", Channel: "Artist", URL: "https://www.youtube.com/watch?v=abc", Duration: 212500 * time.Millisecond}},
		},
		{
			name: "falls back to uploader and builds url",
			data: `{"entries":[{"ie_key":"Youtube","id":"xyz","title":"Radio","uploader":"Lofi Girl","live_status":"is_live"}]}`,
			want: []Track{{ID: "xyz", Title: "Radio", Channel: "Lofi Girl", URL: "https://www.youtube.com/watch?v=xyz", Live: true}},
		},
		{
			name: "skips channels and entries without id",
			data: `{"entries":[{"ie_key":"YoutubeTab","id":"UC123","title":"A channel"},{"ie_key":"Youtube","title":"no id"}]}`,
			want: []Track{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSearch([]byte(tt.data))
			if err != nil {
				t.Fatalf("parseSearch() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSearch() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseSearchInvalidJSON(t *testing.T) {
	if _, err := parseSearch([]byte("not json")); err == nil {
		t.Fatal("parseSearch() error = nil, want error")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	_, err := NewSearcher("yt-dlp").Search(context.Background(), "   ", 10)
	if !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("Search() error = %v, want %v", err, ErrEmptyQuery)
	}
}

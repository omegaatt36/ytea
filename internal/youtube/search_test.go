package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseTracks(t *testing.T) {
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
			name: "single video dumped at the top level",
			data: `{"_type":"video","id":"abc","title":"Song","channel":"Artist","duration":213,"entries":null}`,
			want: []Track{{ID: "abc", Title: "Song", Channel: "Artist", URL: "https://www.youtube.com/watch?v=abc", Duration: 213000 * time.Millisecond}},
		},
		{
			name: "empty playlist yields no tracks",
			data: `{"_type":"playlist","id":"ytsearch30:nothing","entries":[]}`,
			want: []Track{},
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
			got, err := parseTracks([]byte(tt.data))
			if err != nil {
				t.Fatalf("parseTracks() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseTracks() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseTracksInvalidJSON(t *testing.T) {
	if _, err := parseTracks([]byte("not json")); err == nil {
		t.Fatal("parseTracks() error = nil, want error")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	_, err := NewSearcher("yt-dlp").Search(context.Background(), "   ", 10)
	if !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("Search() error = %v, want %v", err, ErrEmptyQuery)
	}
}

// fakeYtDlp is a yt-dlp stand-in that records its arguments and dumps empty listings.
func fakeYtDlp(t *testing.T, argsFile string) *Searcher {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done > " + argsFile + "\nprintf '{\"entries\":[]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return NewSearcher(bin)
}

func TestLookupCapsListing(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	s := fakeYtDlp(t, argsFile)
	ctx := context.Background()

	for _, tt := range []struct {
		name       string
		limit      int
		wantCapped bool
	}{
		{name: "capped", limit: 25, wantCapped: true},
		{name: "uncapped", limit: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Lookup(ctx, "https://www.youtube.com/playlist?list=PLx", tt.limit); err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			got := string(args)
			capped := strings.Contains(got, "--playlist-items")
			if capped != tt.wantCapped {
				t.Errorf("args = %q, want playlist-items: %v", got, tt.wantCapped)
			}
			if capped && !strings.Contains(got, "1:"+fmt.Sprint(tt.limit)) {
				t.Errorf("args = %q, want the cap %d applied", got, tt.limit)
			}
		})
	}
}

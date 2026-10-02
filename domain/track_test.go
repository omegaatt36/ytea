package domain

import "testing"

func TestTrackThumbnailURL(t *testing.T) {
	got := Track{ID: "abc"}.ThumbnailURL()
	if want := "https://i.ytimg.com/vi/abc/mqdefault.jpg"; got != want {
		t.Errorf("ThumbnailURL() = %q, want %q", got, want)
	}
}

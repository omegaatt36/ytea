package mpris

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestTrackPathIsValidObjectPath(t *testing.T) {
	for _, id := range []string{"n61ULEU7CO0", "rFZHOHl-L8A", "a_b-c"} {
		if p := TrackPath(id); !p.IsValid() {
			t.Errorf("TrackPath(%q) = %q, not a valid object path", id, p)
		}
	}
}

func TestMetadata(t *testing.T) {
	tests := []struct {
		name     string
		state    State
		wantKeys []string
	}{
		{name: "no track", state: State{}, wantKeys: []string{"mpris:trackid"}},
		{
			name:     "full track",
			state:    State{TrackID: "abc", Title: "Song", Artist: "Artist", URL: "u", ArtURL: "a", Length: time.Minute},
			wantKeys: []string{"mpris:trackid", "xesam:title", "xesam:url", "xesam:artist", "mpris:artUrl", "mpris:length"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metadata(tt.state)
			if len(got) != len(tt.wantKeys) {
				t.Errorf("metadata() = %v, want keys %v", got, tt.wantKeys)
			}
			for _, k := range tt.wantKeys {
				if _, ok := got[k]; !ok {
					t.Errorf("metadata() missing %q", k)
				}
			}
			if id, _ := got["mpris:trackid"].Value().(dbus.ObjectPath); !id.IsValid() {
				t.Errorf("mpris:trackid = %q, not a valid object path", id)
			}
		})
	}
}

func TestMetadataLengthInMicroseconds(t *testing.T) {
	got := metadata(State{TrackID: "abc", Length: 2 * time.Second})
	if v, _ := got["mpris:length"].Value().(int64); v != 2_000_000 {
		t.Errorf("mpris:length = %d, want 2000000", v)
	}
}

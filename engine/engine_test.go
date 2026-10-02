package engine

import (
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/session"
)

func TestSessionRoundTripKeepsUnplayedTrackMetadata(t *testing.T) {
	const first, second = "https://www.youtube.com/watch?v=first", "https://www.youtube.com/watch?v=second"
	tracks := map[string]domain.Track{
		second: {URL: second, ID: "second", Title: "Unplayed song", Channel: "Artist", Duration: 3 * time.Minute},
	}
	snapshot := mpv.PlaybackState{
		URLs:    []string{first, second},
		Entries: []domain.PlaylistEntry{{Filename: first, Title: "Already playing"}, {Filename: second}},
		Index:   0,
		Volume:  65,
	}
	dir := t.TempDir()
	if err := session.Save(dir, sessionFromSnapshot(snapshot, func(url string) domain.Track { return tracks[url] })); err != nil {
		t.Fatal(err)
	}
	saved, err := session.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored := tracksFromSession(saved)
	if got := restored[second]; got.Title != "Unplayed song" || got.Channel != "Artist" || got.ID != "second" || got.Duration != 3*time.Minute {
		t.Errorf("unplayed track after restart = %+v", got)
	}
	if got := restored[first].Title; got != "Already playing" {
		t.Errorf("mpv title after restart = %q, want Already playing", got)
	}
}

func TestSessionRoundTripKeepsRepeat(t *testing.T) {
	for _, mode := range []domain.Repeat{domain.RepeatOff, domain.RepeatAll, domain.RepeatOne} {
		snapshot := mpv.PlaybackState{URLs: []string{"song"}, Index: 0, Volume: 50, Repeat: mode}
		dir := t.TempDir()
		if err := session.Save(dir, sessionFromSnapshot(snapshot, func(string) domain.Track { return domain.Track{} })); err != nil {
			t.Fatal(err)
		}
		saved, err := session.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := playbackFromSession(saved).Repeat; got != mode {
			t.Errorf("restored repeat = %v, want %v", got, mode)
		}
	}
}

func TestPlaybackFromSessionRepeatFallsBackToOff(t *testing.T) {
	for _, stored := range []string{"", "shuffle"} {
		saved := session.State{Version: 1, URLs: []string{"song"}, Volume: 50, Repeat: stored}
		if got := playbackFromSession(saved).Repeat; got != domain.RepeatOff {
			t.Errorf("repeat for stored %q = %v, want off", stored, got)
		}
	}
}

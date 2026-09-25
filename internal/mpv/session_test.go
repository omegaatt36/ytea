package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestSnapshotAndRestore(t *testing.T) {
	var sourceOps, destOps [][]any
	source := newFakePair(t, func(req request) string {
		sourceOps = append(sourceOps, req.Command)
		var data string
		switch req.Command[1] {
		case PropPlaylist:
			data = `[{"filename":"u1","title":"first song"},{"filename":"u2"}]`
		case PropPlaylistPos:
			data = `1`
		case PropVolume:
			data = `65`
		}
		return fmt.Sprintf(`{"request_id":%d,"error":"success","data":%s}`, req.RequestID, data)
	})
	state, err := (&Player{client: source}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.URLs, []string{"u1", "u2"}) || len(state.Entries) != 2 || state.Entries[0].Title != "first song" || state.Index != 1 || state.Volume != 65 {
		t.Fatalf("snapshot = %+v", state)
	}
	if len(sourceOps) != 3 {
		t.Errorf("read operations = %v, want three properties", sourceOps)
	}

	dest := newFakePair(t, func(req request) string {
		destOps = append(destOps, req.Command)
		return fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID)
	})
	if err := (&Player{client: dest}).Restore(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	want := [][]any{
		{"set_property", PropVolume, float64(65)},
		{"set_property", PropPause, true},
		{"loadfile", "u1", "append"},
		{"loadfile", "u2", "append"},
		{"playlist-play-index", float64(1)},
	}
	if len(destOps) != len(want)+2*len(observed) {
		t.Fatalf("restore issued %d commands, want %d", len(destOps), len(want)+2*len(observed))
	}
	gotJSON, _ := json.Marshal(destOps[:len(want)])
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("restore commands = %s, want %s", gotJSON, wantJSON)
	}
}

func TestPlaylistPreservesTitlesForDuplicateURLs(t *testing.T) {
	c := newFakePair(t, func(req request) string {
		var data string
		switch req.Command[1] {
		case PropPlaylist:
			data = `[{"filename":"same","title":"first"},{"filename":"same","title":"second","current":true,"playing":true}]`
		case PropPlaylistPos:
			data = `1`
		}
		return fmt.Sprintf(`{"request_id":%d,"error":"success","data":%s}`, req.RequestID, data)
	})
	entries, pos, err := (&Player{client: c}).Playlist(context.Background())
	if err != nil {
		t.Fatalf("Playlist() error = %v", err)
	}
	if pos != 1 || len(entries) != 2 || entries[0].Title != "first" || entries[1].Title != "second" || !entries[1].Playing {
		t.Errorf("Playlist() = %+v, pos %d, want both full entries and second selected", entries, pos)
	}
}

func TestRestoreDrainsEventsBeforeUIStarts(t *testing.T) {
	const tracks = 300 // More than the Client.events buffer.
	c := newFakePair(t, func(req request) string {
		var event string
		if req.Command[0] == "loadfile" {
			event = `{"event":"property-change","name":"playlist","data":[]}` + "\n"
		}
		return event + fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID)
	})
	urls := make([]string, tracks)
	for i := range urls {
		urls[i] = fmt.Sprintf("u%d", i)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := (&Player{client: c}).Restore(ctx, PlaybackState{URLs: urls, Index: -1, Volume: 80}); err != nil {
		t.Fatalf("Restore(%d tracks) error = %v", tracks, err)
	}
}

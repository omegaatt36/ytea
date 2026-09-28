package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/internal/history"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func historyModel(t *testing.T) (Model, *history.Store) {
	t.Helper()
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := overlayModel(t, focusQueue)
	m.deps.History = store
	m.player.paused = true
	return m, store
}

func historyURLs(entries []history.Entry) []string {
	urls := make([]string, len(entries))
	for i, e := range entries {
		urls[i] = e.Track.URL
	}
	return urls
}

func TestHistoryRecordsATrackOnceItPlays(t *testing.T) {
	m, store := historyModel(t)
	m.player.timePos = 3 * time.Second
	if cmd := m.recordPlay(); cmd != nil {
		t.Fatal("a paused, restored track was recorded")
	}
	m.player.paused, m.player.timePos = false, 0
	if cmd := m.recordPlay(); cmd != nil {
		t.Fatal("a track not yet playing was recorded")
	}

	m.player.timePos = time.Second
	m.player.duration = 3 * time.Minute
	cmd := m.recordPlay()
	if cmd == nil {
		t.Fatal("a playing track was not recorded")
	}
	got, _ := m.update(cmd())
	m = got.(Model)
	if cmd := m.recordPlay(); cmd != nil {
		t.Error("one play was recorded twice")
	}
	entries := store.Entries()
	if len(entries) != 1 || entries[0].Track.Title != "Song" || entries[0].Track.Duration != 3*time.Minute {
		t.Fatalf("history = %+v", entries)
	}
	if len(m.history) != 1 {
		t.Errorf("the pane did not reload: %d entries", len(m.history))
	}
}

func TestHistoryRecordsFromMPVEvents(t *testing.T) {
	m, store := historyModel(t)
	m.player.paused = false
	_, cmd := m.update(mpvEventMsg(mpv.Event{Name: "property-change", Prop: mpv.PropTimePos, Data: json.RawMessage("2")}))
	runCmd(cmd)
	if got := historyURLs(store.Entries()); !slices.Equal(got, []string{watchURL}) {
		t.Errorf("history after a time-pos event = %q", got)
	}
}

func TestHistoryPaneKeys(t *testing.T) {
	m, store := historyModel(t)
	player := m.deps.Player.(*spyPlayer)
	at := time.Now()
	for _, url := range []string{"u1", "u2", "u3"} {
		if err := store.Record(youtube.Track{Title: "track " + url, URL: url}, at); err != nil {
			t.Fatal(err)
		}
	}
	m.reloadHistory()
	for m.focus != focusHistory {
		m = press(t, m, keyPress("tab"))
	}
	view := rendered(m)
	if !strings.Contains(view, "History (3)") || !strings.Contains(view, "track u3") || !strings.Contains(view, "now") {
		t.Fatalf("history pane:\n%s", view)
	}

	m = press(t, m, keyPress("j"))
	got, cmd := m.update(keyPress("enter"))
	m = got.(Model)
	runCmd(cmd)
	if !slices.Contains(player.calls, "play u2") {
		t.Errorf("enter on the second entry: calls %q", player.calls)
	}

	m = press(t, m, keyPress("d"))
	if got := historyURLs(store.Entries()); !slices.Equal(got, []string{"u3", "u1"}) || m.historyCur != 1 {
		t.Errorf("d: history %q cursor %d", got, m.historyCur)
	}

	// A new play lands on top without moving the cursor off its track.
	if err := store.Record(youtube.Track{URL: "u4"}, at); err != nil {
		t.Fatal(err)
	}
	m.reloadHistory()
	if sel, _ := m.selectedHistoryTrack(); sel.URL != "u1" {
		t.Errorf("after a new play the cursor is on %q, want u1", sel.URL)
	}
}

func TestPlayedAgo(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{2 * 24 * time.Hour, "2d"},
		{60 * 24 * time.Hour, "Jul 30"},
	} {
		if got := playedAgo(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("playedAgo(-%v) = %q, want %q", tt.ago, got, tt.want)
		}
		if len(playedAgo(now.Add(-tt.ago), now)) >= agoWidth {
			t.Errorf("playedAgo(-%v) overflows its column", tt.ago)
		}
	}
}

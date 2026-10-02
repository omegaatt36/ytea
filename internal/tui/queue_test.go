package tui

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

func TestRapidQueueMovesKeepSelectedTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 2

	for range 2 {
		got, cmd := m.update(keyPress("K"))
		if cmd == nil {
			t.Fatal("move command = nil")
		}
		m = got.(Model)
	}
	if m.queueCur != 0 {
		t.Errorf("cursor = %d, want 0", m.queueCur)
	}
	want := []string{"C", "A", "B"}
	for i, entry := range m.core.Queue.Entries {
		if entry.Filename != want[i] {
			t.Fatalf("queue[%d] = %q, want %q", i, entry.Filename, want[i])
		}
	}
	// mpv may report the first move after both keys were handled. That older
	// event must not roll back the locally projected second move.
	m.applyChange(service.QueueChanged{Entries: []domain.PlaylistEntry{{Filename: "A"}, {Filename: "C"}, {Filename: "B"}}})
	if m.core.Queue.Entries[0].Filename != "C" {
		t.Errorf("stale playlist event replaced projected queue: %+v", m.core.Queue.Entries)
	}
}

func TestEmptyQueueNavigationCannotDelete(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	for _, key := range []string{"down", "j", "G", "d", "J", "enter"} {
		got, cmd := m.update(keyPress(key))
		m = got.(Model)
		if cmd != nil || m.queueCur < 0 || len(m.core.Queue.Entries) != 0 {
			t.Fatalf("after %q: cursor=%d queue=%+v cmd=%v", key, m.queueCur, m.core.Queue.Entries, cmd != nil)
		}
	}
}

func TestClearQueueResetsCursorAndPlayback(t *testing.T) {
	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.focus = focusQueue
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	m.core.Queue.Pos, m.core.Playback.Idle = 0, false
	m.core.Playback.TimePos, m.core.Playback.Duration = 10*time.Second, time.Minute
	got, cmd := m.update(keyPress("C"))
	m = got.(Model)
	if cmd == nil || len(m.core.Queue.Entries) != 0 || m.queueCur != 0 || m.core.Queue.Pos != -1 || !m.core.Playback.Idle || m.core.Playback.TimePos != 0 || m.core.Playback.Duration != 0 {
		t.Fatalf("clear projection = queue %+v, cursor %d, pos %d, idle %v, time %v/%v", m.core.Queue.Entries, m.queueCur, m.core.Queue.Pos, m.core.Playback.Idle, m.core.Playback.TimePos, m.core.Playback.Duration)
	}
	got, refresh := m.update(cmd())
	m = got.(Model)
	if refresh == nil || m.status != "queue cleared" {
		t.Fatalf("clear completion = status %q, refresh %v", m.status, refresh != nil)
	}
	if want := []string{"stop"}; !slices.Equal(player.calls, want) {
		t.Errorf("player calls = %v, want %v", player.calls, want)
	}
}

func TestClearQueueKeepsImportStillResolving(t *testing.T) {
	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.input.SetValue("https://youtu.be/x1")
	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	importID := m.core.Active

	m.focus = focusQueue
	got, clear := m.update(keyPress("C"))
	m = got.(Model)
	cleared := make(chan tea.Msg, 1)
	go func() { cleared <- clear() }()
	select {
	case <-cleared:
	case <-time.After(time.Second):
		t.Fatal("clear waited on the unresolved import lookup")
	}

	got, write := m.update(service.LookupDone{RequestID: importID, Tracks: []domain.Track{{URL: "x1"}}})
	m = got.(Model)
	if write == nil {
		t.Fatal("resolved import was not queued after clear")
	}
	if err := write().(service.AppendDone).Err; err != nil {
		t.Fatal(err)
	}
	if want := []string{"stop", "append x1"}; !slices.Equal(player.calls, want) {
		t.Errorf("calls = %v, want %v", player.calls, want)
	}
}

func TestPlayNowBlocksIndexEditsUntilQueueRefresh(t *testing.T) {
	player := &spyPlayer{playlist: []domain.PlaylistEntry{{Filename: "A"}, {Filename: "D"}, {Filename: "B"}}}
	m := New(Deps{Player: player})
	m.focus = focusResults
	m.core.Search.Tracks = []domain.Track{{URL: "D", Title: "D"}}
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, play := m.update(keyPress("enter"))
	m = got.(Model)
	if play == nil || !m.core.Queue.InsertPending {
		t.Fatal("play-now did not reserve an insertion refresh")
	}
	m.focus = focusQueue
	for _, key := range []string{"d", "K", "J", "enter"} {
		got, cmd := m.update(keyPress(key))
		m = got.(Model)
		if cmd != nil || len(m.core.Queue.Entries) != 2 || m.core.Queue.Entries[1].Filename != "B" {
			t.Fatalf("%q edited a stale queue index", key)
		}
	}
	got, refresh := m.update(play())
	m = got.(Model)
	if refresh == nil || !m.core.Queue.InsertPending {
		t.Fatal("insertion was unblocked before mpv queue refresh")
	}
	got, _ = m.update(refresh())
	m = got.(Model)
	if m.core.Queue.InsertPending || len(m.core.Queue.Entries) != 3 || m.core.Queue.Entries[1].Filename != "D" {
		t.Fatalf("insertion refresh = %+v; pending = %v", m.core.Queue.Entries, m.core.Queue.InsertPending)
	}
	got, edit := m.update(keyPress("d"))
	m = got.(Model)
	if edit == nil || len(m.core.Queue.Entries) != 2 || m.core.Queue.Entries[1].Filename != "B" {
		t.Fatalf("delete did not target refreshed index: %+v", m.core.Queue.Entries)
	}
}

func TestRapidQueueDeletesAdvanceToNextTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 0

	for range 2 {
		got, cmd := m.update(keyPress("d"))
		if cmd == nil {
			t.Fatal("delete command = nil")
		}
		m = got.(Model)
	}
	if len(m.core.Queue.Entries) != 1 || m.core.Queue.Entries[0].Filename != "C" {
		t.Errorf("queue = %+v, want only C", m.core.Queue.Entries)
	}
}

func shuffleModel(player service.Player, pos int, names ...string) Model {
	m := New(Deps{Player: player})
	m.focus = focusQueue
	m.rng = rand.New(rand.NewPCG(1, 2))
	for _, n := range names {
		m.core.Queue.Entries = append(m.core.Queue.Entries, domain.PlaylistEntry{Filename: n})
	}
	m.core.Queue.Pos = pos
	m.core.Playback.Idle = pos < 0
	return m
}

// queue-repeat-shuffle R4, R5: the prefix and current track stay put, the tail is permuted, and nothing restarts playback.
func TestShuffleKeepsCurrentAndPrefix(t *testing.T) {
	player := &spyPlayer{}
	m := shuffleModel(player, 1, "A", "B", "C", "D", "E")
	m.queueCur = 1
	got, cmd := m.update(keyPress("Z"))
	m = got.(Model)
	if cmd == nil {
		t.Fatal("Z returned no command")
	}
	view := filenames(m.core.Queue.Entries)
	if view[0] != "A" || view[1] != "B" {
		t.Fatalf("view = %v, want A, B in place", view)
	}
	if tail := slices.Sorted(slices.Values(view[2:])); !slices.Equal(tail, []string{"C", "D", "E"}) {
		t.Fatalf("view tail = %v, want a permutation of C, D, E", view[2:])
	}
	if cur, _, ok := m.core.Current(); !ok || cur.Filename != "B" || m.core.Queue.Pos != 1 {
		t.Fatalf("current = %+v (pos %d), want B at 1", cur, m.core.Queue.Pos)
	}
	if !m.core.Queue.Projected() {
		t.Fatal("shuffle was not projected through the queue-sync write path")
	}
	msg := cmd().(service.ActionDone)
	if msg.Err != nil {
		t.Fatalf("reorder msg = %+v", msg)
	}
	if !slices.Equal(player.calls, []string{"reorder"}) {
		t.Fatalf("player calls = %v, want only reorder", player.calls)
	}
	order := player.orders[0]
	for i, from := range order {
		if view[i] != []string{"A", "B", "C", "D", "E"}[from] {
			t.Fatalf("view %v disagrees with sent order %v", view, order)
		}
	}
	// A playlist event from before the reorder must not undo the projected order.
	m.applyChange(service.QueueChanged{Entries: []domain.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}, {Filename: "D"}, {Filename: "E"}}})
	if !slices.Equal(filenames(m.core.Queue.Entries), view) {
		t.Fatalf("stale event replaced shuffled view: %v", filenames(m.core.Queue.Entries))
	}
}

// queue-repeat-shuffle R4: with nothing playing the whole queue is shuffled.
func TestShuffleWithoutCurrentShufflesAll(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	moved := false
	for seed := range uint64(20) {
		player := &spyPlayer{}
		m := shuffleModel(player, -1, names...)
		m.rng = rand.New(rand.NewPCG(seed, seed))
		got, cmd := m.update(keyPress("Z"))
		m = got.(Model)
		if cmd == nil {
			t.Fatal("Z returned no command")
		}
		view := filenames(m.core.Queue.Entries)
		if !slices.Equal(slices.Sorted(slices.Values(view)), names) {
			t.Fatalf("view = %v, want a permutation of %v", view, names)
		}
		if view[0] != "A" {
			moved = true
		}
	}
	if !moved {
		t.Error("index 0 never moved across 20 seeds; the whole queue was not shuffled")
	}
}

// queue-repeat-shuffle R5: the cursor follows the selected track to its new index.
func TestShuffleKeepsSelectionOnTrack(t *testing.T) {
	for seed := range uint64(10) {
		m := shuffleModel(&spyPlayer{}, 0, "A", "B", "C", "D", "E", "F")
		m.rng = rand.New(rand.NewPCG(seed, 7))
		m.queueCur = 3
		got, _ := m.update(keyPress("Z"))
		m = got.(Model)
		if sel := m.core.Queue.Entries[m.queueCur].Filename; sel != "D" {
			t.Fatalf("seed %d: selection = %q at %d, want D", seed, sel, m.queueCur)
		}
	}
}

// queue-repeat-shuffle R5: fewer than two tracks to reorder leaves the queue alone with a status message.
func TestShuffleNeedsTwoTracks(t *testing.T) {
	for _, tt := range []struct {
		pos   int
		names []string
	}{
		{1, []string{"A", "B", "C"}},
		{2, []string{"A", "B", "C"}},
		{-1, []string{"A"}},
		{-1, nil},
	} {
		player := &spyPlayer{}
		m := shuffleModel(player, tt.pos, tt.names...)
		m.status = ""
		got, cmd := m.update(keyPress("Z"))
		m = got.(Model)
		if cmd != nil || len(player.calls) != 0 || m.core.Queue.Projected() {
			t.Fatalf("pos %d %v: shuffle issued a write", tt.pos, tt.names)
		}
		if !slices.Equal(filenames(m.core.Queue.Entries), filenames(shuffleModel(nil, tt.pos, tt.names...).core.Queue.Entries)) {
			t.Fatalf("pos %d %v: queue changed to %v", tt.pos, tt.names, filenames(m.core.Queue.Entries))
		}
		if m.status == "" {
			t.Fatalf("pos %d %v: no status message", tt.pos, tt.names)
		}
	}
}

// queue-repeat-shuffle R5: a failed reorder reports the error and resyncs the queue from mpv.
func TestShuffleErrorResyncsFromMPV(t *testing.T) {
	want := errors.New("playlist-move failed")
	original := []domain.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}, {Filename: "D"}}
	player := &spyPlayer{err: want, playlist: original}
	m := shuffleModel(player, 1, "A", "B", "C", "D")
	got, cmd := m.update(keyPress("Z"))
	m = got.(Model)
	if cmd == nil {
		t.Fatal("Z returned no command")
	}
	got, refresh := m.update(cmd())
	m = got.(Model)
	if !m.statusErr || !strings.Contains(m.status, want.Error()) {
		t.Fatalf("status = %q (err %v), want reorder error", m.status, m.statusErr)
	}
	if refresh == nil {
		t.Fatal("failed reorder scheduled no resync")
	}
	got, _ = m.update(refresh())
	m = got.(Model)
	if !slices.Equal(filenames(m.core.Queue.Entries), filenames(original)) || m.core.Queue.Projected() {
		t.Fatalf("queue after resync = %v, want mpv's %v", filenames(m.core.Queue.Entries), filenames(original))
	}
}

// queue-repeat-shuffle R5: playlist-pos only follows moves once mpv reports them, so Z waits until
// earlier edits are confirmed instead of shuffling from a stale current index.
func TestShuffleWaitsForPendingEdits(t *testing.T) {
	player := &spyPlayer{}
	m := shuffleModel(player, 0, "A", "B", "C", "D", "E")
	m.queueCur = 0
	got, move := m.update(keyPress("J"))
	m = got.(Model)
	moved := filenames(m.core.Queue.Entries)

	got, cmd := m.update(keyPress("Z"))
	m = got.(Model)
	if cmd != nil || len(player.orders) != 0 || !slices.Equal(filenames(m.core.Queue.Entries), moved) {
		t.Fatalf("Z during a pending move issued a reorder: cmd %v, orders %v, view %v", cmd != nil, player.orders, filenames(m.core.Queue.Entries))
	}

	got, _ = m.update(move())
	m = got.(Model)
	got, cmd = m.update(keyPress("Z"))
	m = got.(Model)
	if cmd != nil || len(player.orders) != 0 || !slices.Equal(filenames(m.core.Queue.Entries), moved) {
		t.Fatalf("Z before the move was read back issued a reorder: cmd %v, orders %v, view %v", cmd != nil, player.orders, filenames(m.core.Queue.Entries))
	}
}

func TestShuffleKeyOnlyInQueuePane(t *testing.T) {
	for _, f := range []focus{focusResults, focusPlaylists, focusPlaylistTracks} {
		player := &spyPlayer{}
		m := shuffleModel(player, 0, "A", "B", "C", "D")
		m.focus = f
		got, cmd := m.update(keyPress("Z"))
		m = got.(Model)
		if cmd != nil || len(player.calls) != 0 || !slices.Equal(filenames(m.core.Queue.Entries), []string{"A", "B", "C", "D"}) {
			t.Fatalf("focus %d: Z acted outside the Queue pane", f)
		}
	}
}

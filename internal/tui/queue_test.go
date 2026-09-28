package tui

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestRapidQueueMovesKeepSelectedTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
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
	for i, entry := range m.queue.entries {
		if entry.Filename != want[i] {
			t.Fatalf("queue[%d] = %q, want %q", i, entry.Filename, want[i])
		}
	}
	// mpv may report the first move after both keys were handled. That older
	// event must not roll back the locally projected second move.
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`)})
	if m.queue.entries[0].Filename != "C" {
		t.Errorf("stale playlist event replaced projected queue: %+v", m.queue.entries)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"C"},{"filename":"A"},{"filename":"B"}]`)})
	if m.queue.projection == nil {
		t.Error("projection cleared before command completion")
	}
}

func TestEmptyQueueNavigationCannotDelete(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	for _, key := range []string{"down", "j", "G", "d", "J", "enter"} {
		got, cmd := m.update(keyPress(key))
		m = got.(Model)
		if cmd != nil || m.queueCur < 0 || len(m.queue.entries) != 0 {
			t.Fatalf("after %q: cursor=%d queue=%+v cmd=%v", key, m.queueCur, m.queue.entries, cmd != nil)
		}
	}
}

func TestClearQueueProjectsEmptyAndIgnoresStalePlaylist(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	m.queue.pos, m.player.idle = 0, false
	m.player.timePos, m.player.duration = 10*time.Second, time.Minute
	got, cmd := m.update(keyPress("C"))
	m = got.(Model)
	if cmd == nil || len(m.queue.entries) != 0 || m.queueCur != 0 || m.queue.pos != -1 || !m.player.idle || m.player.timePos != 0 || m.player.duration != 0 {
		t.Fatalf("clear projection = queue %+v, cursor %d, pos %d, idle %v, time %v/%v", m.queue.entries, m.queueCur, m.queue.pos, m.player.idle, m.player.timePos, m.player.duration)
	}
	if m.queue.editsPending != 1 || m.queue.projection == nil {
		t.Fatal("clear did not reserve an authoritative queue refresh")
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"B"}]`)})
	if len(m.queue.entries) != 0 {
		t.Fatal("stale playlist event repopulated cleared queue")
	}
	got, refresh := m.update(queueActionDoneMsg{requestID: m.activeRequest, status: "queue cleared", projected: true})
	m = got.(Model)
	if refresh == nil || m.status != "queue cleared" {
		t.Fatalf("clear completion = status %q, refresh %v", m.status, refresh != nil)
	}
	got, _ = m.update(queueRefreshMsg{revision: m.queue.revision, entries: nil, pos: -1})
	m = got.(Model)
	if len(m.queue.entries) != 0 || m.queue.pos != -1 || m.queue.projection != nil {
		t.Fatalf("clear refresh = queue %+v, pos %d, projection %v", m.queue.entries, m.queue.pos, m.queue.projection)
	}
}

func TestClearQueueKeepsImportStillResolving(t *testing.T) {
	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.input.SetValue("https://youtu.be/x1")
	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	importID := m.activeRequest

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

	got, write := m.update(lookupDoneMsg{requestID: importID, tracks: []youtube.Track{{URL: "x1"}}})
	m = got.(Model)
	if write == nil {
		t.Fatal("resolved import was not queued after clear")
	}
	if err := write().(queueDoneMsg).err; err != nil {
		t.Fatal(err)
	}
	if want := []string{"stop", "append x1"}; !slices.Equal(player.calls, want) {
		t.Errorf("calls = %v, want %v", player.calls, want)
	}
}

func TestQueueEnterReservesSlotAfterProjectedMove(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, move := m.update(keyPress("K"))
	m = got.(Model)
	if move == nil || m.queueCur != 0 {
		t.Fatal("move did not project B to index 0")
	}
	moveDone := m.queue.tail
	got, play := m.update(keyPress("enter"))
	m = got.(Model)
	if play == nil || m.queue.tail == moveDone {
		t.Fatal("play selection was not reserved behind the projected move")
	}
	if m.queue.editsPending != 1 {
		t.Errorf("pending projected edits = %d, want 1", m.queue.editsPending)
	}
}

func TestPlayNowBlocksIndexEditsUntilQueueRefresh(t *testing.T) {
	m := New(Deps{})
	m.focus = focusResults
	m.results.tracks = []youtube.Track{{URL: "D", Title: "D"}}
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, play := m.update(keyPress("enter"))
	m = got.(Model)
	if play == nil || !m.queue.insertPending || m.queue.editsPending != 1 {
		t.Fatal("play-now did not reserve an insertion refresh")
	}
	m.focus = focusQueue
	for _, key := range []string{"d", "K", "J", "enter"} {
		got, cmd := m.update(keyPress(key))
		m = got.(Model)
		if cmd != nil || len(m.queue.entries) != 2 || m.queue.entries[1].Filename != "B" {
			t.Fatalf("%q edited a stale queue index", key)
		}
	}
	got, refresh := m.update(queueActionDoneMsg{requestID: m.activeRequest, projected: true})
	m = got.(Model)
	if refresh == nil || !m.queue.insertPending {
		t.Fatal("insertion was unblocked before mpv queue refresh")
	}
	got, _ = m.update(queueRefreshMsg{revision: m.queue.revision, entries: []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "D"}, {Filename: "B"}}, pos: 1})
	m = got.(Model)
	if m.queue.insertPending || len(m.queue.entries) != 3 || m.queue.entries[1].Filename != "D" {
		t.Fatalf("insertion refresh = %+v; pending = %v", m.queue.entries, m.queue.insertPending)
	}
	got, edit := m.update(keyPress("d"))
	m = got.(Model)
	if edit == nil || len(m.queue.entries) != 2 || m.queue.entries[1].Filename != "B" {
		t.Fatalf("delete did not target refreshed index: %+v", m.queue.entries)
	}
}

func TestOpposingQueueMovesIgnoreOldAndIntermediateEvents(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 2
	for _, key := range []string{"K", "J"} {
		got, cmd := m.update(keyPress(key))
		if cmd == nil {
			t.Fatalf("%s command = nil", key)
		}
		m = got.(Model)
	}
	if m.queueCur != 2 || m.queue.entries[2].Filename != "C" {
		t.Fatalf("projection = %+v cursor %d, want original order with C selected", m.queue.entries, m.queueCur)
	}
	for _, snapshot := range []string{
		`[{"filename":"A"},{"filename":"B"},{"filename":"C"}]`, // before either move
		`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`, // after only K
	} {
		m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(snapshot)})
		if m.queue.entries[2].Filename != "C" || m.queueCur != 2 {
			t.Fatalf("stale event %s changed projected selection: %+v cursor %d", snapshot, m.queue.entries, m.queueCur)
		}
	}
	for _, id := range []uint64{1, 2} {
		got, _ := m.update(queueActionDoneMsg{requestID: id, projected: true})
		m = got.(Model)
	}
	// Both mpv commands have now finished, but older property events can still
	// be queued for the TUI. Equality with the final order is not a barrier.
	for _, snapshot := range []string{
		`[{"filename":"A"},{"filename":"B"},{"filename":"C"}]`,
		`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`,
	} {
		m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(snapshot)})
		if m.queue.entries[2].Filename != "C" || m.queueCur != 2 {
			t.Fatalf("late event %s changed projected selection: %+v cursor %d", snapshot, m.queue.entries, m.queueCur)
		}
	}
	got, _ := m.update(queueRefreshMsg{revision: m.queue.revision, entries: []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}, pos: -1})
	m = got.(Model)
	if m.queue.entries[2].Filename != "C" || m.queueCur != 2 {
		t.Errorf("authoritative refresh = %+v cursor %d, want C selected", m.queue.entries, m.queueCur)
	}
	got, _ = m.update(keyPress("K"))
	m = got.(Model)
	if m.queue.entries[1].Filename != "C" || m.queueCur != 1 {
		t.Errorf("next move targeted wrong track: queue=%+v cursor=%d", m.queue.entries, m.queueCur)
	}
}

func TestPlaylistEventBurstSchedulesOneAuthoritativeRead(t *testing.T) {
	m := New(Deps{})
	m.queue.authoritative = true
	event := mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"}]`)}
	commands := 0
	for range 200 {
		if cmd := m.applyProperty(event); cmd != nil {
			commands++
		}
	}
	if commands != 1 {
		t.Fatalf("200 events scheduled %d debounce timers, want 1", commands)
	}
	got, cmd := m.update(queueDebounceMsg{version: 1})
	m = got.(Model)
	if cmd == nil || !m.queue.debouncePending || m.queue.refreshPending {
		t.Fatal("changed event generation did not extend debounce")
	}
	got, cmd = m.update(queueDebounceMsg{version: m.queue.eventVersion})
	m = got.(Model)
	if cmd == nil || !m.queue.refreshPending {
		t.Fatal("settled burst did not schedule one authoritative read")
	}
}

func TestRapidQueueDeletesAdvanceToNextTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 0

	for range 2 {
		got, cmd := m.update(keyPress("d"))
		if cmd == nil {
			t.Fatal("delete command = nil")
		}
		m = got.(Model)
	}
	if len(m.queue.entries) != 1 || m.queue.entries[0].Filename != "C" {
		t.Errorf("queue = %+v, want only C", m.queue.entries)
	}
}

func TestStalePlaylistEventDoesNotRestoreDeletedEntry(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{
		{Filename: "A"},
		{Filename: "B"},
		{Filename: "C"},
	}
	m.queueCur = 0
	got, cmd := m.update(keyPress("d"))
	if cmd == nil {
		t.Fatal("delete command = nil")
	}
	m = got.(Model)

	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":1,"filename":"A"},{"id":2,"filename":"B"},{"id":3,"filename":"C"}]`)})
	if len(m.queue.entries) != 2 || m.queue.entries[0].Filename != "B" {
		t.Fatalf("stale event restored deleted A: %+v", m.queue.entries)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":2,"filename":"B"},{"id":4,"filename":"D"},{"id":3,"filename":"C"}]`)})
	if len(m.queue.entries) != 2 || m.queue.entries[0].Filename != "B" {
		t.Errorf("event bypassed authoritative refresh: queue=%+v", m.queue.entries)
	}
	got, _ = m.update(queueActionDoneMsg{requestID: m.activeRequest, projected: true})
	m = got.(Model)
	got, _ = m.update(queueRefreshMsg{revision: m.queue.revision, entries: []mpv.PlaylistEntry{{Filename: "B"}, {Filename: "D"}, {Filename: "C"}}, pos: -1})
	m = got.(Model)
	if len(m.queue.entries) != 3 || m.queue.entries[1].Filename != "D" {
		t.Errorf("authoritative refresh missed insert: queue=%+v", m.queue.entries)
	}
}

func TestProjectedDeleteDistinguishesDuplicateURLs(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "same"}, {Filename: "same"}}
	got, _ := m.update(keyPress("d"))
	m = got.(Model)
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":1,"filename":"same"},{"id":2,"filename":"same"}]`)})
	if len(m.queue.entries) != 1 || m.queue.entries[0].Filename != "same" {
		t.Errorf("stale duplicate event restored removed entry: %+v", m.queue.entries)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":2,"filename":"same"},{"id":3,"filename":"same"}]`)})
	if len(m.queue.entries) != 1 {
		t.Errorf("duplicate event bypassed authoritative refresh: queue=%+v", m.queue.entries)
	}
}

func TestQueueRefreshPreservesEntryTitlesWithDuplicateURLs(t *testing.T) {
	m := New(Deps{})
	m.queue.authoritative = true
	entries := []mpv.PlaylistEntry{
		{Filename: "same", Title: "first title", Current: false},
		{Filename: "same", Title: "second title", Current: true, Playing: true},
	}
	got, _ := m.update(queueRefreshMsg{entries: entries, pos: 1})
	m = got.(Model)
	if len(m.queue.entries) != 2 || m.queue.entries[0].Title != "first title" || m.queue.entries[1].Title != "second title" {
		t.Fatalf("refreshed entries = %+v, want distinct titles", m.queue.entries)
	}
	if title := displayTitle(m.queue.entries[1], youtube.Track{}); title != "second title" {
		t.Errorf("display title = %q, want second title", title)
	}
	if m.queue.pos != 1 || !m.queue.entries[1].Playing {
		t.Errorf("position = %d, playing = %v, want second entry playing", m.queue.pos, m.queue.entries[1].Playing)
	}
}

func TestFailedQueueActionReleasesNextWrite(t *testing.T) {
	q := newQueueSync(nil)
	first := q.reserve()
	second := q.reserve()
	want := errors.New("first append failed")
	order := make(chan string, 2)
	secondDone := make(chan tea.Msg, 1)
	go func() {
		secondDone <- queueAction(second, 2, "second queued", func(context.Context) error {
			order <- "second"
			return nil
		})()
	}()
	firstMsg := queueAction(first, 1, "first queued", func(context.Context) error {
		order <- "first"
		return want
	})().(queueActionDoneMsg)
	if !errors.Is(firstMsg.err, want) {
		t.Fatalf("first error = %v, want %v", firstMsg.err, want)
	}
	select {
	case msg := <-secondDone:
		if err := msg.(queueActionDoneMsg).err; err != nil {
			t.Fatalf("second error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second action remained blocked after first failed")
	}
	for _, want := range []string{"first", "second"} {
		if got := <-order; got != want {
			t.Fatalf("write order = %q, want %q", got, want)
		}
	}
}

func shuffleModel(player Player, pos int, names ...string) Model {
	m := New(Deps{Player: player})
	m.focus = focusQueue
	m.rng = rand.New(rand.NewPCG(1, 2))
	for _, n := range names {
		m.queue.entries = append(m.queue.entries, mpv.PlaylistEntry{Filename: n})
	}
	m.queue.pos = pos
	m.player.idle = pos < 0
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
	view := filenames(m.queue.entries)
	if view[0] != "A" || view[1] != "B" {
		t.Fatalf("view = %v, want A, B in place", view)
	}
	if tail := slices.Sorted(slices.Values(view[2:])); !slices.Equal(tail, []string{"C", "D", "E"}) {
		t.Fatalf("view tail = %v, want a permutation of C, D, E", view[2:])
	}
	if cur, _, ok := m.current(); !ok || cur.Filename != "B" || m.queue.pos != 1 {
		t.Fatalf("current = %+v (pos %d), want B at 1", cur, m.queue.pos)
	}
	if m.queue.editsPending != 1 || m.queue.projection == nil {
		t.Fatal("shuffle was not projected through the queue-sync write path")
	}
	msg := cmd().(queueActionDoneMsg)
	if msg.err != nil || !msg.projected {
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
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"B"},{"filename":"C"},{"filename":"D"},{"filename":"E"}]`)})
	if !slices.Equal(filenames(m.queue.entries), view) {
		t.Fatalf("stale event replaced shuffled view: %v", filenames(m.queue.entries))
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
		view := filenames(m.queue.entries)
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
		if sel := m.queue.entries[m.queueCur].Filename; sel != "D" {
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
		if cmd != nil || len(player.calls) != 0 || m.queue.editsPending != 0 {
			t.Fatalf("pos %d %v: shuffle issued a write", tt.pos, tt.names)
		}
		if !slices.Equal(filenames(m.queue.entries), filenames(shuffleModel(nil, tt.pos, tt.names...).queue.entries)) {
			t.Fatalf("pos %d %v: queue changed to %v", tt.pos, tt.names, filenames(m.queue.entries))
		}
		if m.status == "" {
			t.Fatalf("pos %d %v: no status message", tt.pos, tt.names)
		}
	}
}

// queue-repeat-shuffle R5: a failed reorder reports the error and resyncs the queue from mpv.
func TestShuffleErrorResyncsFromMPV(t *testing.T) {
	want := errors.New("playlist-move failed")
	original := []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}, {Filename: "D"}}
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
	if !slices.Equal(filenames(m.queue.entries), filenames(original)) || m.queue.projection != nil {
		t.Fatalf("queue after resync = %v, want mpv's %v", filenames(m.queue.entries), filenames(original))
	}
}

// queue-repeat-shuffle R5: playlist-pos only follows moves once mpv reports them, so Z waits until
// earlier edits are confirmed instead of shuffling from a stale current index.
func TestShuffleWaitsForPendingEdits(t *testing.T) {
	player := &spyPlayer{}
	m := shuffleModel(player, 0, "A", "B", "C", "D", "E")
	m.queueCur = 0
	got, _ := m.update(keyPress("J"))
	m = got.(Model)
	moved := filenames(m.queue.entries)

	got, cmd := m.update(keyPress("Z"))
	m = got.(Model)
	if cmd != nil || len(player.orders) != 0 || !slices.Equal(filenames(m.queue.entries), moved) {
		t.Fatalf("Z during a pending move issued a reorder: cmd %v, orders %v, view %v", cmd != nil, player.orders, filenames(m.queue.entries))
	}

	got, _ = m.update(queueActionDoneMsg{projected: true})
	m = got.(Model)
	got, cmd = m.update(keyPress("Z"))
	m = got.(Model)
	if cmd != nil || len(player.orders) != 0 || !slices.Equal(filenames(m.queue.entries), moved) {
		t.Fatalf("Z before the move was read back issued a reorder: cmd %v, orders %v, view %v", cmd != nil, player.orders, filenames(m.queue.entries))
	}
}

func TestShuffleKeyOnlyInQueuePane(t *testing.T) {
	for _, f := range []focus{focusResults, focusPlaylists, focusPlaylistTracks} {
		player := &spyPlayer{}
		m := shuffleModel(player, 0, "A", "B", "C", "D")
		m.focus = f
		got, cmd := m.update(keyPress("Z"))
		m = got.(Model)
		if cmd != nil || len(player.calls) != 0 || !slices.Equal(filenames(m.queue.entries), []string{"A", "B", "C", "D"}) {
			t.Fatalf("focus %d: Z acted outside the Queue pane", f)
		}
	}
}

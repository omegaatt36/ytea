package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

// Methods it does not override panic on the nil embedded interface.
type fakePlayer struct {
	Player
	calls    []string
	playlist []domain.PlaylistEntry
	err      error
}

func (p *fakePlayer) record(call string) error {
	p.calls = append(p.calls, call)
	return p.err
}

func (p *fakePlayer) Stop(context.Context) error { return p.record("stop") }
func (p *fakePlayer) Playlist(context.Context) ([]domain.PlaylistEntry, int, error) {
	return p.playlist, -1, p.err
}

func entries(names ...string) []domain.PlaylistEntry {
	out := make([]domain.PlaylistEntry, len(names))
	for i, n := range names {
		out[i] = domain.PlaylistEntry{Filename: n}
	}
	return out
}

func names(es []domain.PlaylistEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Filename
	}
	return out
}

func playlistEvent(files ...string) PlayerEvent {
	return QueueChanged{Entries: entries(files...)}
}

func assertQueue(t *testing.T, q Queue, want ...string) {
	t.Helper()
	got := names(q.Entries)
	if len(got) != len(want) {
		t.Fatalf("queue = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("queue = %v, want %v", got, want)
		}
	}
}

func TestStaleEventsDoNotRollBackProjectedMoves(t *testing.T) {
	q := NewQueue(nil)
	q.Entries = entries("A", "B", "C")
	q.Move(1, 2, 1)
	q.Move(2, 1, 0)
	assertQueue(t, q, "C", "A", "B")

	// mpv may report the first move after both were projected. That older
	// event must not roll back the second one.
	q.ApplyEvent(playlistEvent("A", "C", "B"))
	assertQueue(t, q, "C", "A", "B")
	q.ApplyEvent(playlistEvent("C", "A", "B"))
	if !q.Projected() {
		t.Error("projection cleared before command completion")
	}
}

func TestClearProjectsEmptyAndIgnoresStalePlaylist(t *testing.T) {
	player := &fakePlayer{}
	q := NewQueue(player)
	q.Entries, q.Pos = entries("A", "B"), 0

	clear := q.Clear(7)
	if len(q.Entries) != 0 || q.Pos != -1 || q.editsPending != 1 || !q.Projected() {
		t.Fatalf("clear projection = queue %v, pos %d, edits %d, projected %v", names(q.Entries), q.Pos, q.editsPending, q.Projected())
	}
	q.ApplyEvent(playlistEvent("A", "B"))
	assertQueue(t, q)

	done, ok := clear().(ActionDone)
	if !ok || done.RequestID != 7 || done.Action != ActionClear || done.Err != nil {
		t.Fatalf("clear result = %+v", done)
	}
	refresh := q.Update(done)
	if refresh == nil {
		t.Fatal("clear completion scheduled no authoritative refresh")
	}
	q.Update(refresh())
	if len(q.Entries) != 0 || q.Pos != -1 || q.Projected() {
		t.Fatalf("clear refresh = queue %v, pos %d, projected %v", names(q.Entries), q.Pos, q.Projected())
	}
	if want := []string{"stop"}; len(player.calls) != 1 || player.calls[0] != want[0] {
		t.Errorf("player calls = %v, want %v", player.calls, want)
	}
}

func TestWritesReserveSlotsBehindProjectedEdits(t *testing.T) {
	q := NewQueue(nil)
	q.Entries = entries("A", "B")
	q.Move(1, 1, 0)
	moveDone := q.tail
	q.PlayIndex(2, 0)
	if q.tail == moveDone {
		t.Fatal("play selection was not reserved behind the projected move")
	}
	if q.editsPending != 1 {
		t.Errorf("pending projected edits = %d, want 1", q.editsPending)
	}
}

func TestPlayNowHoldsInsertUntilQueueRefresh(t *testing.T) {
	q := NewQueue(nil)
	q.Entries = entries("A", "B")
	q.PlayNow(1, domain.Track{URL: "D", Title: "D"})
	if !q.InsertPending || q.editsPending != 1 {
		t.Fatal("play-now did not reserve an insertion refresh")
	}
	refresh := q.Update(ActionDone{projected: true})
	if refresh == nil || !q.InsertPending {
		t.Fatal("insertion was unblocked before the mpv queue was read back")
	}
	q.Update(Refreshed{revision: q.revision, entries: entries("A", "D", "B"), pos: 1})
	if q.InsertPending {
		t.Error("refresh did not release the insertion")
	}
	assertQueue(t, q, "A", "D", "B")
}

func TestOpposingMovesIgnoreOldAndIntermediateEvents(t *testing.T) {
	q := NewQueue(nil)
	q.Entries = entries("A", "B", "C")
	q.Move(1, 2, 1)
	q.Move(2, 1, 2)
	assertQueue(t, q, "A", "B", "C")

	for _, snapshot := range [][]string{
		{"A", "B", "C"}, // before either move
		{"A", "C", "B"}, // after only the first
	} {
		q.ApplyEvent(playlistEvent(snapshot...))
		assertQueue(t, q, "A", "B", "C")
	}
	q.Update(ActionDone{projected: true})
	q.Update(ActionDone{projected: true})
	// Both mpv commands have finished, but older events can still be in
	// flight. Equality with the final order is not a barrier.
	for _, snapshot := range [][]string{
		{"A", "B", "C"},
		{"A", "C", "B"},
	} {
		q.ApplyEvent(playlistEvent(snapshot...))
		assertQueue(t, q, "A", "B", "C")
	}
	q.Update(Refreshed{revision: q.revision, entries: entries("A", "B", "C"), pos: -1})
	assertQueue(t, q, "A", "B", "C")
	q.Move(3, 2, 1)
	assertQueue(t, q, "A", "C", "B")
}

func TestPlaylistEventBurstSchedulesOneAuthoritativeRead(t *testing.T) {
	q := NewQueue(nil)
	q.authoritative = true
	event := playlistEvent("A")
	timers := 0
	for range 200 {
		if q.ApplyEvent(event) != nil {
			timers++
		}
	}
	if timers != 1 {
		t.Fatalf("200 events scheduled %d debounce timers, want 1", timers)
	}
	if cmd := q.Update(Debounced{version: 1}); cmd == nil || !q.debouncePending || q.refreshPending {
		t.Fatal("changed event generation did not extend debounce")
	}
	if cmd := q.Update(Debounced{version: q.eventVersion}); cmd == nil || !q.refreshPending {
		t.Fatal("settled burst did not schedule one authoritative read")
	}
}

func TestStalePlaylistEventDoesNotRestoreDeletedEntry(t *testing.T) {
	q := NewQueue(nil)
	q.Entries = entries("A", "B", "C")
	q.Remove(1, 0)
	assertQueue(t, q, "B", "C")

	q.ApplyEvent(playlistEvent("A", "B", "C"))
	assertQueue(t, q, "B", "C")
	q.ApplyEvent(playlistEvent("B", "D", "C"))
	assertQueue(t, q, "B", "C")

	q.Update(ActionDone{projected: true})
	q.Update(Refreshed{revision: q.revision, entries: entries("B", "D", "C"), pos: -1})
	assertQueue(t, q, "B", "D", "C")
}

func TestProjectedDeleteDistinguishesDuplicateURLs(t *testing.T) {
	q := NewQueue(nil)
	q.Entries = entries("same", "same")
	q.Remove(1, 0)
	q.ApplyEvent(playlistEvent("same", "same"))
	assertQueue(t, q, "same")
	q.ApplyEvent(playlistEvent("same", "same"))
	assertQueue(t, q, "same")
}

func TestRefreshPreservesEntryTitlesWithDuplicateURLs(t *testing.T) {
	q := NewQueue(nil)
	q.authoritative = true
	q.Update(Refreshed{
		entries: []domain.PlaylistEntry{
			{Filename: "same", Title: "first title"},
			{Filename: "same", Title: "second title", Current: true, Playing: true},
		},
		pos: 1,
	})
	if len(q.Entries) != 2 || q.Entries[0].Title != "first title" || q.Entries[1].Title != "second title" {
		t.Fatalf("refreshed entries = %+v, want distinct titles", q.Entries)
	}
	if q.Pos != 1 || !q.Entries[1].Playing {
		t.Errorf("position = %d, playing = %v, want second entry playing", q.Pos, q.Entries[1].Playing)
	}
}

func TestRefreshFailureIsReported(t *testing.T) {
	want := errors.New("ipc closed")
	q := NewQueue(nil)
	q.authoritative = true
	cmd := q.Update(Refreshed{err: want})
	if cmd == nil {
		t.Fatal("failed refresh reported nothing")
	}
	if failed, ok := cmd().(Failed); !ok || !errors.Is(failed.Err, want) {
		t.Fatalf("failed refresh reported %+v, want %v", failed, want)
	}
}

func TestFailedActionReleasesNextWrite(t *testing.T) {
	q := NewQueue(nil)
	first, second := q.Reserve(), q.Reserve()
	want := errors.New("first append failed")
	order := make(chan string, 2)
	secondDone := make(chan Msg, 1)
	go func() {
		secondDone <- action(second, ActionDone{RequestID: 2}, func(context.Context) error {
			order <- "second"
			return nil
		})()
	}()
	firstMsg := action(first, ActionDone{RequestID: 1}, func(context.Context) error {
		order <- "first"
		return want
	})().(ActionDone)
	if !errors.Is(firstMsg.Err, want) {
		t.Fatalf("first error = %v, want %v", firstMsg.Err, want)
	}
	select {
	case msg := <-secondDone:
		if err := msg.(ActionDone).Err; err != nil {
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

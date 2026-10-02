package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

func messagesOf[T any](cmd tea.Cmd) []T {
	var out []T
	for _, msg := range runCmd(cmd) {
		if v, ok := msg.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestSearchEnterImportsYouTubeLink(t *testing.T) {
	player := &spyPlayer{}
	m := New(Deps{Player: player, Searcher: searchStub{tracks: []domain.Track{
		{ID: "x1", Title: "one", URL: "https://www.youtube.com/watch?v=x1"},
	}}})
	m.input.SetValue("https://youtu.be/x1")

	got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	gm := got.(Model)
	if !gm.core.Busy() {
		t.Error("busy = false, want the import in flight")
	}
	if !strings.Contains(gm.status, "importing") {
		t.Errorf("status = %q, want an importing note", gm.status)
	}
	if len(player.calls) != 0 {
		t.Fatalf("calls = %v, want nothing appended before the lookup resolves", player.calls)
	}

	lookups := messagesOf[service.LookupDone](cmd)
	if len(lookups) != 1 {
		t.Fatalf("lookups = %v, want one", lookups)
	}
	got, write := gm.update(lookups[0])
	gm = got.(Model)
	if write == nil {
		t.Fatal("resolved import was not queued")
	}
	done := messagesOf[service.AppendDone](write)
	if len(done) != 1 || !equalStrings(player.calls, "append https://www.youtube.com/watch?v=x1") {
		t.Fatalf("calls = %v, want the resolved track appended", player.calls)
	}
	got, cmd = gm.update(done[0])
	gm = got.(Model)
	if gm.core.Busy() {
		t.Error("busy = true, want the fetch finished")
	}
	if _, ok := gm.core.Tracks["https://www.youtube.com/watch?v=x1"]; !ok {
		t.Errorf("tracks = %v, want the resolved track remembered", gm.core.Tracks)
	}
	if got := gm.status; got != "1 track queued" {
		t.Errorf("status = %q, want 1 track queued", got)
	}
	if cmd != nil {
		t.Error("cmd != nil, want the write already completed")
	}
}

func equalStrings(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAppendDoneReportsOutcome(t *testing.T) {
	m := New(Deps{Searcher: searchStub{}})
	m.input.SetValue("https://youtu.be/x1")
	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	id := m.core.Active
	tracks := []domain.Track{
		{ID: "a", Title: "one", URL: "https://www.youtube.com/watch?v=a"},
		{ID: "b", Title: "two", URL: "https://www.youtube.com/watch?v=b"},
	}

	got, cmd := m.update(service.AppendDone{RequestID: id, Tracks: tracks})
	gm := got.(Model)
	if gm.core.Busy() {
		t.Error("busy = true, want the fetch finished")
	}
	if got := gm.status; got != "2 tracks queued" {
		t.Errorf("status = %q, want 2 tracks queued", got)
	}
	if cmd != nil {
		t.Error("cmd != nil, want the write already completed")
	}

	got, _ = m.update(service.AppendDone{RequestID: id, Tracks: tracks, LimitHit: true})
	if got := got.(Model).status; !strings.Contains(got, "import limit: 200") {
		t.Errorf("status = %q, want import-limit hint", got)
	}

	got, cmd = m.update(service.AppendDone{RequestID: id, Err: errors.New("boom")})
	gm = got.(Model)
	if !gm.statusErr || gm.status != "boom" {
		t.Errorf("status = %q err = %v, want the error surfaced", gm.status, gm.statusErr)
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want none for a failed fetch", cmd)
	}
}

func TestRadioKeySeedsFromCurrentTrack(t *testing.T) {
	m := playingModel(graphicsNone)
	m.focus = focusResults

	got, cmd := m.Update(tea.KeyPressMsg{Code: 'r'})
	gm := got.(Model)
	if !gm.core.Busy() {
		t.Error("busy = false, want the radio fetch in flight")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want a radio fetch")
	}
	if got := gm.status; got != "fetching radio…" {
		t.Errorf("status = %q, want fetching radio", got)
	}

	m = New(Deps{})
	m.focus = focusResults
	got, cmd = m.Update(tea.KeyPressMsg{Code: 'r'})
	gm = got.(Model)
	if gm.core.Busy() {
		t.Error("busy = true, want no fetch for an idle player")
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want none", cmd)
	}
}

func TestStaleCompletionPreservesActiveSpinnerAndStatus(t *testing.T) {
	m := New(Deps{})
	old := pendingSearch(&m, "old")
	newest := pendingSearch(&m, "newest")
	m.setStatus("searching newest…")

	for _, msg := range []tea.Msg{
		service.SearchDone{RequestID: old, Err: errors.New("old search failed")},
		service.AppendDone{RequestID: old, Err: errors.New("old import failed")},
		service.ActionDone{RequestID: old, Err: errors.New("old append failed")},
	} {
		got, _ := m.update(msg)
		m = got.(Model)
		if !m.core.Busy() || m.status != "searching newest…" || m.statusErr || m.core.Active != newest {
			t.Fatalf("after %T: busy = %v, status = %q, error = %v", msg, m.core.Busy(), m.status, m.statusErr)
		}
	}
}

func TestQueueActionDoesNotDiscardPendingSearch(t *testing.T) {
	m := New(Deps{})
	searchID := pendingSearch(&m, "song")
	queueID := m.core.NextRequest()
	m.setStatus("queueing a track…")

	got, _ := m.update(service.SearchDone{RequestID: searchID, Query: "song", Tracks: []domain.Track{{ID: "song", URL: "song"}}})
	m = got.(Model)
	if len(m.core.Search.Tracks) != 1 || m.core.Search.Tracks[0].ID != "song" {
		t.Errorf("results = %+v, want pending search applied", m.core.Search.Tracks)
	}
	if m.core.Busy() || m.status != "queueing a track…" || m.core.Active != queueID {
		t.Errorf("busy = %v, status = %q, active = %d", m.core.Busy(), m.status, m.core.Active)
	}
}

func TestQueueWriteErrorIsReportedAfterWrite(t *testing.T) {
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = graphicsNone
	id := m.core.NextRequest()
	want := errors.New("mpv rejected append")
	tracks := []domain.Track{{ID: "x", Title: "resolved first", URL: "x"}, {ID: "y", Title: "resolved second", URL: "y"}}
	m.core.Queue.Entries, m.core.Queue.Pos, m.core.Playback.Idle = []domain.PlaylistEntry{{Filename: "x"}}, 0, false
	// mpv accepted the first entry before rejecting the next one.
	got, _ := m.update(service.AppendDone{RequestID: id, Tracks: tracks, Err: want})
	m = got.(Model)
	if !m.statusErr || m.status != want.Error() {
		t.Errorf("status = %q, error = %v, want write error", m.status, m.statusErr)
	}
	_, playing, ok := m.core.Current()
	if !ok || playing.Title != "resolved first" {
		t.Errorf("current track = %+v, available = %v, want metadata for accepted entry", playing, ok)
	}
	if m.thumb.video != "x" {
		t.Errorf("thumbnail target = %q, want accepted track x after metadata arrives", m.thumb.video)
	}
}

func TestLinkImportSwitchesToQueueTab(t *testing.T) {
	m := New(Deps{})
	m.input.SetValue("https://www.youtube.com/playlist?list=PL123")
	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m = got.(Model); !atPane(m, focusQueue) {
		t.Errorf("focus after link import = %v, want queue", m.focus)
	}
}

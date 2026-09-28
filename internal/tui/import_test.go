package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestSearchEnterImportsYouTubeLink(t *testing.T) {
	player := &spyPlayer{}
	m := New(Deps{Player: player, Searcher: searchStub{tracks: []youtube.Track{
		{ID: "x1", Title: "one", URL: "https://www.youtube.com/watch?v=x1"},
	}}})
	m.input.SetValue("https://youtu.be/x1")

	got, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	gm := got.(Model)
	if !gm.searching {
		t.Error("searching = false, want the import in flight")
	}
	if !strings.Contains(gm.status, "importing") {
		t.Errorf("status = %q, want an importing note", gm.status)
	}

	if len(gm.imports) != 1 || gm.queue.tail != nil {
		t.Fatalf("imports = %+v, want a pending lookup without a queue slot", gm.imports)
	}

	lm, ok := fetchQueue(gm.deps.Searcher, youtube.Link{URL: "https://www.youtube.com/watch?v=x1"}, gm.activeRequest)().(lookupDoneMsg)
	if !ok {
		t.Fatal("fetchQueue did not return a lookupDoneMsg")
	}
	got, write := gm.update(lm)
	gm = got.(Model)
	if write == nil || len(gm.imports) != 0 {
		t.Fatalf("write = %v, imports = %+v, want the resolved import queued", write != nil, gm.imports)
	}
	qm, ok := write().(queueDoneMsg)
	if !ok || !slices.Equal(player.calls, []string{"append https://www.youtube.com/watch?v=x1"}) {
		t.Fatalf("calls = %v, want the resolved track appended", player.calls)
	}
	got, cmd := gm.update(qm)
	gm = got.(Model)
	if gm.searching {
		t.Error("searching = true, want the fetch finished")
	}
	if _, ok := gm.tracks["https://www.youtube.com/watch?v=x1"]; !ok {
		t.Errorf("tracks = %v, want the resolved track remembered", gm.tracks)
	}
	if got := gm.status; got != "1 track queued" {
		t.Errorf("status = %q, want 1 track queued", got)
	}
	if cmd != nil {
		t.Error("cmd != nil, want the write already completed")
	}
}

func TestQueueDoneQueuesTracks(t *testing.T) {
	m := New(Deps{})
	m.searching = true
	tracks := []youtube.Track{
		{ID: "a", Title: "one", URL: "https://www.youtube.com/watch?v=a"},
		{ID: "b", Title: "two", URL: "https://www.youtube.com/watch?v=b"},
	}

	got, cmd := m.update(queueDoneMsg{tracks: tracks})
	gm := got.(Model)
	if gm.searching {
		t.Error("searching = true, want the fetch finished")
	}
	if got := gm.status; got != "2 tracks queued" {
		t.Errorf("status = %q, want 2 tracks queued", got)
	}
	for _, tr := range tracks {
		if _, ok := gm.tracks[tr.URL]; !ok {
			t.Errorf("tracks missing %q", tr.URL)
		}
	}
	if cmd != nil {
		t.Error("cmd != nil, want the write already completed")
	}

	got, cmd = m.update(queueDoneMsg{err: errors.New("boom")})
	gm = got.(Model)
	if !gm.statusErr || gm.status != "boom" {
		t.Errorf("status = %q err = %v, want the error surfaced", gm.status, gm.statusErr)
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want none for a failed fetch", cmd)
	}
}

func TestFetchQueueCapsMixes(t *testing.T) {
	tracks := []youtube.Track{{ID: "seed", URL: "https://www.youtube.com/watch?v=seed"}}
	for i := range radioLimit + 4 {
		id := fmt.Sprintf("t%d", i)
		tracks = append(tracks, youtube.Track{ID: id, URL: "https://www.youtube.com/watch?v=" + id})
	}

	link := youtube.Link{URL: "https://www.youtube.com/watch?v=seed&list=RDseed", Mix: true}
	qm, ok := fetchQueue(searchStub{tracks: tracks}, link, 1)().(lookupDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want lookupDoneMsg", qm)
	}
	if len(qm.tracks) != radioLimit {
		t.Errorf("tracks = %d, want %d", len(qm.tracks), radioLimit)
	}
	if qm.tracks[0].ID != "seed" {
		t.Errorf("first track = %q, want the seed, which is what the link plays", qm.tracks[0].ID)
	}

	link = youtube.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	qm, ok = fetchQueue(searchStub{tracks: tracks}, link, 2)().(lookupDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want lookupDoneMsg", qm)
	}
	if len(qm.tracks) != radioLimit+5 {
		t.Errorf("tracks = %d, want the whole playlist", len(qm.tracks))
	}
}

func TestFetchQueueCapsLargePlaylistAndShowsLimit(t *testing.T) {
	tracks := make([]youtube.Track, youtube.MaxPlaylistItems+5)
	for i := range tracks {
		tracks[i] = youtube.Track{ID: fmt.Sprint(i), URL: fmt.Sprintf("https://www.youtube.com/watch?v=%d", i)}
	}
	appended := 0
	link := youtube.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	lookup, ok := fetchQueue(searchStub{tracks: tracks}, link, 1)().(lookupDoneMsg)
	if !ok {
		t.Fatal("fetchQueue did not return a lookupDoneMsg")
	}
	qm := queueImport(func(got []youtube.Track) error {
		appended = len(got)
		return nil
	}, lookup, queueTask{done: make(chan struct{})})().(queueDoneMsg)
	if len(qm.tracks) != youtube.MaxPlaylistItems || appended != youtube.MaxPlaylistItems || !qm.limitHit {
		t.Fatalf("queued %d tracks, appended %d, limitHit %v", len(qm.tracks), appended, qm.limitHit)
	}
	m := New(Deps{})
	m.activeRequest = 1
	updated, _ := m.update(qm)
	if got := updated.(Model).status; !strings.Contains(got, "import limit: 200") {
		t.Errorf("status = %q, want import-limit hint", got)
	}
}

func TestRadioKeySeedsFromCurrentTrack(t *testing.T) {
	m := playingModel(graphicsNone)
	m.focus = focusResults

	got, cmd := m.Update(tea.KeyPressMsg{Code: 'r'})
	gm := got.(Model)
	if !gm.searching {
		t.Error("searching = false, want the radio fetch in flight")
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
	if gm.searching {
		t.Error("searching = true, want no fetch for an idle player")
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want none", cmd)
	}
}

func TestFetchRadioDropsSeedAndCaps(t *testing.T) {
	tracks := []youtube.Track{{ID: "seed", URL: "https://www.youtube.com/watch?v=seed"}}
	for i := range radioLimit + 1 {
		id := fmt.Sprintf("t%d", i)
		tracks = append(tracks, youtube.Track{ID: id, URL: "https://www.youtube.com/watch?v=" + id})
	}

	msg := fetchRadio(searchStub{tracks: tracks}, "seed", 1)()
	qm, ok := msg.(lookupDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want lookupDoneMsg", msg)
	}
	if qm.err != nil {
		t.Fatalf("err = %v", qm.err)
	}
	if len(qm.tracks) != radioLimit {
		t.Errorf("tracks = %d, want %d", len(qm.tracks), radioLimit)
	}
	for _, tr := range qm.tracks {
		if tr.ID == "seed" {
			t.Error("seed still queued, want it dropped from the mix")
		}
	}
}

func TestSearchCompletionKeepsNewestRequest(t *testing.T) {
	m := New(Deps{})
	first := m.nextRequest()
	m.searching = true
	second := m.nextRequest()
	m.searchRequest = second
	m.spinnerRequest = second
	m.searching = true
	m.setStatus("searching newest…")

	newest := youtube.Track{ID: "new", URL: "new"}
	got, _ := m.update(searchDoneMsg{requestID: second, query: "newest", tracks: []youtube.Track{newest}})
	m = got.(Model)
	got, _ = m.update(searchDoneMsg{requestID: first, query: "older", tracks: []youtube.Track{{ID: "old", URL: "old"}}})
	m = got.(Model)
	if len(m.results.tracks) != 1 || m.results.tracks[0].ID != newest.ID {
		t.Errorf("results = %+v, want only newest", m.results.tracks)
	}
	if m.status != "1 result for “newest”" || m.searching {
		t.Errorf("status = %q, searching = %v, want newest completion", m.status, m.searching)
	}
	if _, ok := m.tracks["old"]; ok {
		t.Error("older result was added to track metadata")
	}
}

func TestStaleCompletionPreservesActiveSpinnerAndStatus(t *testing.T) {
	m := New(Deps{})
	old := m.nextRequest()
	newest := m.nextRequest()
	m.searchRequest = newest
	m.spinnerRequest = newest
	m.searching = true
	m.setStatus("searching newest…")

	for _, msg := range []tea.Msg{
		searchDoneMsg{requestID: old, err: errors.New("old search failed")},
		queueDoneMsg{requestID: old, err: errors.New("old import failed")},
		queueActionDoneMsg{requestID: old, err: errors.New("old append failed")},
	} {
		got, _ := m.update(msg)
		m = got.(Model)
		if !m.searching || m.status != "searching newest…" || m.statusErr || m.activeRequest != newest {
			t.Fatalf("after %T: searching = %v, status = %q, error = %v", msg, m.searching, m.status, m.statusErr)
		}
	}
}

func TestQueueActionDoesNotDiscardPendingSearch(t *testing.T) {
	m := New(Deps{})
	searchID := m.nextRequest()
	m.searchRequest, m.spinnerRequest, m.searching = searchID, searchID, true
	queueID := m.nextRequest()
	m.setStatus("queueing a track…")

	got, _ := m.update(searchDoneMsg{requestID: searchID, query: "song", tracks: []youtube.Track{{ID: "song", URL: "song"}}})
	m = got.(Model)
	if len(m.results.tracks) != 1 || m.results.tracks[0].ID != "song" {
		t.Errorf("results = %+v, want pending search applied", m.results.tracks)
	}
	if m.searching || m.status != "queueing a track…" || m.activeRequest != queueID {
		t.Errorf("searching = %v, status = %q, active = %d", m.searching, m.status, m.activeRequest)
	}
}

func TestOverlappingImportsWriteInTriggerOrder(t *testing.T) {
	player := appendRecorder{writes: make(chan string, 2)}
	m := New(Deps{Player: player})
	first, second := m.nextRequest(), m.nextRequest()
	m.imports = []pendingImport{{requestID: first}, {requestID: second}}

	got, cmd := m.update(lookupDoneMsg{requestID: second, tracks: []youtube.Track{{URL: "second"}}})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("second import was queued before first resolved")
	}
	got, cmd = m.update(lookupDoneMsg{requestID: first, tracks: []youtube.Track{{URL: "first"}}})
	m = got.(Model)
	if cmd == nil || len(m.imports) != 0 {
		t.Fatalf("pending imports = %+v, want both queued", m.imports)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("cmd = %v, want both writes batched", batch)
	}
	// Start the writes in reverse: their reserved slots still keep trigger order.
	done := make(chan tea.Msg, 2)
	for _, write := range slices.Backward(batch) {
		go func() { done <- write() }()
	}
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-player.writes:
			if got != want {
				t.Fatalf("write = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q write", want)
		}
	}
	for range 2 {
		if err := (<-done).(queueDoneMsg).err; err != nil {
			t.Fatalf("import error = %v", err)
		}
	}
}

func TestQueueWriteErrorIsReportedAfterWrite(t *testing.T) {
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = graphicsNone
	id := m.nextRequest()
	task := m.queue.reserve()
	want := errors.New("mpv rejected append")
	tracks := []youtube.Track{{ID: "x", Title: "resolved first", URL: "x"}, {ID: "y", Title: "resolved second", URL: "y"}}
	m.queue.entries, m.queue.pos, m.player.idle = []mpv.PlaylistEntry{{Filename: "x"}}, 0, false
	accepted := ""
	msg := queueImport(func(tracks []youtube.Track) error {
		accepted = tracks[0].URL // mpv accepted this entry before rejecting the next one.
		return want
	}, lookupDoneMsg{requestID: id, tracks: tracks}, task)()
	got, _ := m.update(msg)
	m = got.(Model)
	if !m.statusErr || m.status != want.Error() {
		t.Errorf("status = %q, error = %v, want write error", m.status, m.statusErr)
	}
	if accepted != "x" || m.tracks["x"].Title != "resolved first" || m.tracks["y"].Title != "resolved second" {
		t.Errorf("accepted = %q, metadata = %+v, want resolved tracks retained after partial write", accepted, m.tracks)
	}
	_, playing, ok := m.current()
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

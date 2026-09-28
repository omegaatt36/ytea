package tui

import (
	"encoding/json"
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

func TestApplyPlaylistPos(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "index", data: "2", want: 2},
		{name: "none selected", data: "-1", want: -1},
		{name: "unavailable", data: "null", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.applyProperty(mpv.Event{Name: "property-change", Prop: mpv.PropPlaylistPos, Data: json.RawMessage(tt.data)})
			if m.queue.pos != tt.want {
				t.Errorf("pos = %d, want %d", m.queue.pos, tt.want)
			}
		})
	}
}

func TestApplyAFTracksNormalize(t *testing.T) {
	m := New(Deps{})
	on := `[{"label":"norm","name":"lavfi"}]`
	m.applyProperty(mpv.Event{Prop: mpv.PropAF, Data: json.RawMessage(on)})
	if !m.normalize {
		t.Fatal("normalize = false after norm filter added, want true")
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropAF, Data: json.RawMessage(`[]`)})
	if m.normalize {
		t.Fatal("normalize = true after filters cleared, want false")
	}
}

func TestWaitMPVDropsTimePosWithinShownSecond(t *testing.T) {
	timePos := func(data string) mpv.Event {
		return mpv.Event{Name: "property-change", Prop: mpv.PropTimePos, Data: json.RawMessage(data)}
	}
	tests := []struct {
		name   string
		shown  time.Duration
		events []mpv.Event
		want   tea.Msg
	}{
		{
			name:   "same second is dropped",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.2"), timePos("12.4"), timePos("12.6")},
			want:   mpvEventMsg(timePos("12.6")),
		},
		{
			name:   "backward seek into a new second",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("3.1")},
			want:   mpvEventMsg(timePos("3.1")),
		},
		{
			name:   "unavailable",
			shown:  0,
			events: []mpv.Event{timePos("null")},
			want:   mpvEventMsg(timePos("null")),
		},
		{
			name:   "other events pass",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.1"), {Name: "playback-restart"}},
			want:   mpvEventMsg(mpv.Event{Name: "playback-restart"}),
		},
		{
			name:   "closed after dropped events",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.1")},
			want:   mpvClosedMsg{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan mpv.Event, len(tt.events))
			for _, ev := range tt.events {
				ch <- ev
			}
			close(ch)
			got := waitMPV(ch, tt.shown)()
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("waitMPV() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
	if len(m.results) != 1 || m.results[0].ID != newest.ID {
		t.Errorf("results = %+v, want only newest", m.results)
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
	if len(m.results) != 1 || m.results[0].ID != "song" {
		t.Errorf("results = %+v, want pending search applied", m.results)
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
	m.queue.entries, m.queue.pos, m.idle = []mpv.PlaylistEntry{{Filename: "x"}}, 0, false
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

// queue-repeat-shuffle R1, R2: L asks mpv for the next mode; only mpv's loop-* reports change the display.
func TestRepeatKeyCyclesThroughMPV(t *testing.T) {
	for _, f := range []focus{focusResults, focusQueue, focusPlaylists} {
		player := &spyPlayer{}
		m := New(Deps{Player: player})
		m.focus = f
		got, cmd := m.update(keyPress("L"))
		m = got.(Model)
		if cmd == nil {
			t.Fatalf("focus %d: L returned no command", f)
		}
		cmd()
		if !slices.Equal(player.repeats, []mpv.Repeat{mpv.RepeatAll}) {
			t.Fatalf("focus %d: SetRepeat calls = %v, want [all]", f, player.repeats)
		}
		if strings.Contains(m.modeLine(), "repeat") {
			t.Fatalf("focus %d: display changed before mpv reported: %q", f, m.modeLine())
		}
	}

	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.focus = focusResults
	steps := []struct {
		prop, value string
		display     string
		next        mpv.Repeat
	}{
		{mpv.PropLoopPlaylist, `"inf"`, "repeat all", mpv.RepeatOne},
		{mpv.PropLoopFile, `"inf"`, "repeat one", mpv.RepeatOff},
		{mpv.PropLoopPlaylist, `false`, "repeat one", mpv.RepeatOff},
		{mpv.PropLoopFile, `false`, "", mpv.RepeatAll},
	}
	for _, step := range steps {
		m.applyProperty(mpv.Event{Name: "property-change", Prop: step.prop, Data: json.RawMessage(step.value)})
		line := m.modeLine()
		if step.display == "" && strings.Contains(line, "repeat") || step.display != "" && !strings.Contains(line, step.display) {
			t.Fatalf("after %s=%s audio line = %q, want %q", step.prop, step.value, line, step.display)
		}
		got, cmd := m.update(keyPress("L"))
		m = got.(Model)
		cmd()
		if last := player.repeats[len(player.repeats)-1]; last != step.next {
			t.Fatalf("after %s=%s L requested %v, want %v", step.prop, step.value, last, step.next)
		}
	}
}

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/thumbnail"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "unknown", in: 0, want: "--:--"},
		{name: "minutes", in: 3*time.Minute + 5*time.Second, want: "3:05"},
		{name: "rounds", in: 59*time.Second + 600*time.Millisecond, want: "1:00"},
		{name: "hours", in: 8*time.Hour + 28*time.Second, want: "8:00:28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.in); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNextThumbIDStaysInPaletteRange(t *testing.T) {
	tests := []struct {
		prev, want int
	}{
		{prev: 0, want: 16},
		{prev: 16, want: 17},
		{prev: 254, want: 255},
		{prev: 255, want: 16},
	}
	for _, tt := range tests {
		if got := nextThumbID(tt.prev); got != tt.want {
			t.Errorf("nextThumbID(%d) = %d, want %d", tt.prev, got, tt.want)
		}
	}
}

func TestApplyPlaylistPos(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "index", data: "2", want: 2},
		{name: "none selected", data: "-1", want: -1},
		// null must not decode to 0 and mark the first entry as playing.
		{name: "unavailable", data: "null", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.applyProperty(mpv.Event{Name: "property-change", Prop: mpv.PropPlaylistPos, Data: json.RawMessage(tt.data)})
			if m.pos != tt.want {
				t.Errorf("pos = %d, want %d", m.pos, tt.want)
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

func TestRenderSpectrumSize(t *testing.T) {
	m := New(Deps{})
	m.levels = []float64{0, 0.25, 0.5, 1}
	got := m.renderSpectrum(40, vizRows)
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w != 40 || h != vizRows {
		t.Errorf("renderSpectrum() size = %dx%d, want 40x%d", w, h, vizRows)
	}
}

func TestRenderFitsWindow(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 100, 30
	m.showViz = true
	got := m.render()
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w > 100 || h > 30 {
		t.Errorf("render() size = %dx%d, want within 100x30", w, h)
	}
}

func TestPasteGoesToSearch(t *testing.T) {
	m := New(Deps{})
	m.focus = focusResults
	m.input.Blur()

	got, _ := m.Update(tea.PasteMsg{Content: "joe hisaishi"})
	gm := got.(Model)
	if gm.focus != focusSearch {
		t.Errorf("focus = %v, want focusSearch", gm.focus)
	}
	if v := gm.input.Value(); v != "joe hisaishi" {
		t.Errorf("input = %q, want %q", v, "joe hisaishi")
	}
}

func TestGraphicsProbe(t *testing.T) {
	reply := func(id int, payload string) uv.KittyGraphicsEvent {
		return uv.KittyGraphicsEvent{Options: kitty.Options{ID: id}, Payload: []byte(payload)}
	}
	plainOK := reply(thumbnail.QueryID, "OK")
	placeholderOK := reply(thumbnail.PlaceholderQueryID, "OK")
	placeholderRejected := reply(thumbnail.PlaceholderQueryID, "ENOTSUPPORTED:unicode placeholders are not supported")
	da1 := uv.PrimaryDeviceAttributesEvent{62, 22}

	tests := []struct {
		name   string
		events []tea.Msg
		want   graphicsSupport
	}{
		{name: "Ghostty: both probes OK", events: []tea.Msg{plainOK, placeholderOK, da1}, want: graphicsPlaceholder},
		{name: "Zellij 0.45: placeholders rejected", events: []tea.Msg{plainOK, placeholderRejected, da1}, want: graphicsDirect},
		{name: "Zellij 0.44 or tmux: only DA1", events: []tea.Msg{da1}, want: graphicsNone},
		{name: "replies after DA1 are ignored", events: []tea.Msg{da1, plainOK, placeholderOK}, want: graphicsNone},
		{name: "undecided until DA1", events: []tea.Msg{plainOK, placeholderOK}, want: graphicsUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m tea.Model = New(Deps{Thumbnails: true})
			for _, ev := range tt.events {
				m, _ = m.Update(ev)
			}
			if got := m.(Model).graphics; got != tt.want {
				t.Errorf("graphics = %v, want %v", got, tt.want)
			}
		})
	}
}

// devicePickerModel returns a model listing mpv's output devices.
func devicePickerModel(device string) Model {
	m := New(Deps{})
	m.width, m.height = 80, 30
	m.applyProperty(mpv.Event{Prop: mpv.PropAudioDevice, Data: json.RawMessage(device)})
	m.devices = []mpv.AudioDevice{
		{Name: "auto", Description: "Autoselect device"},
		{Name: "pipewire/DX5", Description: "DX5 II Headphones"},
	}
	return m
}

func TestDevicePickerMarksCurrent(t *testing.T) {
	tests := []struct {
		name      string
		device    string // mpv's audio-device property, JSON-encoded
		wantIndex int
	}{
		{name: "explicit device", device: `"pipewire/DX5"`, wantIndex: 1},
		{name: "mpv's auto", device: `"auto"`, wantIndex: 0},
		{name: "before the first event", device: `null`, wantIndex: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := devicePickerModel(tt.device)
			if !m.isCurrentDevice(m.devices[tt.wantIndex]) {
				t.Errorf("device %d not marked as current for %s", tt.wantIndex, tt.device)
			}
			if got := m.currentDeviceName(); got != m.devices[tt.wantIndex].Name {
				t.Errorf("currentDeviceName() = %q, want %q", got, m.devices[tt.wantIndex].Name)
			}
			if _, ok := m.currentDevice(); !ok {
				t.Errorf("currentDevice() = not found for %s", tt.device)
			}
		})
	}
}

func TestRenderDevices(t *testing.T) {
	m := devicePickerModel(`"pipewire/DX5"`)
	m.deviceCur = 1

	got := ansi.Strip(m.renderDevices(80, 20))
	for _, want := range []string{"● ", "DX5 II Headphones", "Autoselect device"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderDevices() = %q, want %q", got, want)
		}
	}

	m.devices = nil
	if got := ansi.Strip(m.renderDevices(80, 20)); !strings.Contains(got, "no output devices") {
		t.Errorf("renderDevices() = %q, want an empty-list hint", got)
	}
}

func TestPasteKeysReadClipboard(t *testing.T) {
	// Zellij forwards ctrl+shift+v as a kitty-protocol key event rather than a paste.
	ctrlShiftV := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}
	if got := ctrlShiftV.String(); got != "ctrl+shift+v" {
		t.Fatalf("key string = %q, want ctrl+shift+v", got)
	}

	tests := []struct {
		name  string
		focus focus
	}{
		{name: "from search", focus: focusSearch},
		{name: "from results", focus: focusResults},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.focus = tt.focus

			got, cmd := m.Update(ctrlShiftV)
			gm := got.(Model)
			if gm.focus != focusSearch {
				t.Errorf("focus = %v, want focusSearch", gm.focus)
			}
			if cmd == nil {
				t.Error("Update() cmd = nil, want clipboard read")
			}
			if v := gm.input.Value(); v != "" {
				t.Errorf("input = %q, want the key not typed as text", v)
			}
		})
	}
}

// playingModel returns a model mid-track with its thumbnail fetch in flight.
func playingModel(g graphicsSupport) Model {
	const url = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{Thumbnails: true})
	m.graphics = g
	m.width, m.height = 100, 30
	m.idle, m.pos = false, 0
	m.queue = []mpv.PlaylistEntry{{Filename: url}}
	m.tracks[url] = youtube.Track{ID: "abc", Title: "Song", URL: url}
	m.thumbVideo = "abc"
	return m
}

func TestApplyThumbPicksRendering(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 18))

	tests := []struct {
		name      string
		graphics  graphicsSupport
		wantKitty bool
	}{
		{name: "kitty placeholders", graphics: graphicsPlaceholder, wantKitty: true},
		{name: "kitty direct placement, e.g. Zellij 0.45", graphics: graphicsDirect, wantKitty: true},
		{name: "half-block fallback", graphics: graphicsNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cmd := playingModel(tt.graphics).applyThumb(thumbMsg{videoID: "abc", img: img})
			gm := got.(Model)

			if gotKitty := gm.thumbID != 0; gotKitty != tt.wantKitty {
				t.Errorf("kitty image = %v, want %v", gotKitty, tt.wantKitty)
			}
			if gotArt := gm.thumbArt != ""; gotArt == tt.wantKitty {
				t.Errorf("half-block art = %v, want %v", gotArt, !tt.wantKitty)
			}
			// Only kitty modes write escape sequences out of band.
			if (cmd != nil) != tt.wantKitty {
				t.Errorf("cmd = %v, want out-of-band transmit only for kitty", cmd)
			}
			if w := lipgloss.Width(gm.renderNowPlaying()); w != 100 {
				t.Errorf("now playing width = %d, want 100", w)
			}
		})
	}
}

func TestThumbOriginIsInsideNowPlayingBox(t *testing.T) {
	for _, viz := range []bool{false, true} {
		m := playingModel(graphicsDirect)
		m.thumbID, m.showViz = 16, viz

		at := m.thumbOrigin()
		lines := strings.Split(ansi.Strip(m.render()), "\n")
		// The box's top-left corner sits one cell up and left of the thumbnail.
		if corner := []rune(lines[at.Y-1])[at.X-1]; corner != '╭' {
			t.Errorf("viz=%v: cell above-left of thumbOrigin %v = %q, want box corner", viz, at, corner)
		}
		if above := []rune(lines[at.Y-1])[at.X]; above != '─' {
			t.Errorf("viz=%v: cell above thumbOrigin = %q, want top border", viz, above)
		}
	}
}

func TestSyncPlacement(t *testing.T) {
	m := playingModel(graphicsDirect)
	m.thumbID = 16

	if cmd := m.syncPlacement(); cmd == nil {
		t.Fatal("first sync: cmd = nil, want placement scheduled")
	}
	if cmd := m.syncPlacement(); cmd != nil {
		t.Error("unchanged layout: cmd != nil, want no re-placement")
	}

	m.showViz = !m.showViz // moves the now-playing box
	if cmd := m.syncPlacement(); cmd == nil {
		t.Error("layout moved: cmd = nil, want re-placement")
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !next.(Model).placed {
		t.Error("after resize: placed = false, want a placement rescheduled by Update")
	}

	if _, cmd := m.update(placeMsg{id: 99, at: m.placedAt}); cmd != nil {
		t.Errorf("placeMsg for a replaced image: cmd = %v, want nil", cmd)
	}
	if _, cmd := m.update(placeMsg{id: 16, at: m.placedAt}); cmd == nil {
		t.Error("placeMsg for the current image: cmd = nil, want Put")
	}
}

// searchStub answers Lookup with canned tracks.
type searchStub struct {
	tracks []youtube.Track
}

func (s searchStub) Search(ctx context.Context, query string, limit int) ([]youtube.Track, error) {
	return nil, errors.New("unexpected search")
}

func (s searchStub) Lookup(ctx context.Context, url string, limit int) ([]youtube.Track, error) {
	return s.tracks, nil
}

func TestSearchEnterImportsYouTubeLink(t *testing.T) {
	m := New(Deps{Searcher: searchStub{tracks: []youtube.Track{
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

	task := queueTask{done: make(chan struct{})}
	qm := fetchQueue(gm.deps.Searcher, func([]youtube.Track) error { return nil }, youtube.Link{URL: "https://www.youtube.com/watch?v=x1"}, gm.activeRequest, task)()
	resolved, ok := qm.(queueDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want queueDoneMsg", qm)
	}
	got, cmd := gm.update(resolved)
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
	qm, ok := fetchQueue(searchStub{tracks: tracks}, func([]youtube.Track) error { return nil }, link, 1, queueTask{done: make(chan struct{})})().(queueDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want queueDoneMsg", qm)
	}
	if len(qm.tracks) != radioLimit {
		t.Errorf("tracks = %d, want %d", len(qm.tracks), radioLimit)
	}
	if qm.tracks[0].ID != "seed" {
		t.Errorf("first track = %q, want the seed, which is what the link plays", qm.tracks[0].ID)
	}

	link = youtube.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	qm, ok = fetchQueue(searchStub{tracks: tracks}, func([]youtube.Track) error { return nil }, link, 2, queueTask{done: make(chan struct{})})().(queueDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want queueDoneMsg", qm)
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
	msg := fetchQueue(searchStub{tracks: tracks}, func(got []youtube.Track) error {
		appended = len(got)
		return nil
	}, link, 1, queueTask{done: make(chan struct{})})()
	qm, ok := msg.(queueDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want queueDoneMsg", msg)
	}
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

	msg := fetchRadio(searchStub{tracks: tracks}, func([]youtube.Track) error { return nil }, "seed", 1, queueTask{done: make(chan struct{})})()
	qm, ok := msg.(queueDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want queueDoneMsg", msg)
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

// lookupGateStub lets a test finish independent lookups in reverse order.
type lookupGateStub struct {
	started  chan string
	returned chan string
	finish   map[string]chan struct{}
}

func (s lookupGateStub) Search(context.Context, string, int) ([]youtube.Track, error) {
	return nil, errors.New("unexpected search")
}

func (s lookupGateStub) Lookup(_ context.Context, url string, _ int) ([]youtube.Track, error) {
	s.started <- url
	<-s.finish[url]
	s.returned <- url
	return []youtube.Track{{ID: url, URL: url}}, nil
}

func TestOverlappingImportsWriteInTriggerOrder(t *testing.T) {
	stub := lookupGateStub{
		started:  make(chan string, 2),
		returned: make(chan string, 2),
		finish:   map[string]chan struct{}{"first": make(chan struct{}), "second": make(chan struct{})},
	}
	m := New(Deps{})
	first := m.reserveQueue()
	second := m.reserveQueue()
	writes := make(chan string, 2)
	appendQueue := func(tracks []youtube.Track) error {
		writes <- tracks[0].URL
		return nil
	}
	firstDone := make(chan tea.Msg, 1)
	secondDone := make(chan tea.Msg, 1)
	go func() { firstDone <- fetchQueue(stub, appendQueue, youtube.Link{URL: "first"}, 1, first)() }()
	go func() { secondDone <- fetchQueue(stub, appendQueue, youtube.Link{URL: "second"}, 2, second)() }()
	for range 2 {
		select {
		case <-stub.started:
		case <-time.After(time.Second):
			t.Fatal("lookups did not start")
		}
	}
	close(stub.finish["second"])
	select {
	case got := <-stub.returned:
		if got != "second" {
			t.Fatalf("lookup returned %q, want second before first is released", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second lookup did not return")
	}
	select {
	case write := <-writes:
		t.Fatalf("second import wrote %q before first resolved", write)
	default:
	}
	close(stub.finish["first"])
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-writes:
			if got != want {
				t.Fatalf("write = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q write", want)
		}
	}
	for _, done := range []<-chan tea.Msg{firstDone, secondDone} {
		select {
		case msg := <-done:
			if err := msg.(queueDoneMsg).err; err != nil {
				t.Fatalf("import error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("import command did not finish")
		}
	}
}

func TestRapidQueueMovesKeepSelectedTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 2

	for range 2 {
		got, cmd := m.handleQueueKey("K")
		if cmd == nil {
			t.Fatal("move command = nil")
		}
		m = got.(Model)
	}
	if m.queueCur != 0 {
		t.Errorf("cursor = %d, want 0", m.queueCur)
	}
	want := []string{"C", "A", "B"}
	for i, entry := range m.queue {
		if entry.Filename != want[i] {
			t.Fatalf("queue[%d] = %q, want %q", i, entry.Filename, want[i])
		}
	}
	// mpv may report the first move after both keys were handled. That older
	// event must not roll back the locally projected second move.
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`)})
	if m.queue[0].Filename != "C" {
		t.Errorf("stale playlist event replaced projected queue: %+v", m.queue)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"C"},{"filename":"A"},{"filename":"B"}]`)})
	if m.queueProjection == nil {
		t.Error("projection cleared before command completion")
	}
}

func TestEmptyQueueNavigationCannotDelete(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	for _, key := range []string{"down", "j", "G", "d", "J", "enter"} {
		got, cmd := m.handleQueueKey(key)
		m = got.(Model)
		if cmd != nil || m.queueCur < 0 || len(m.queue) != 0 {
			t.Fatalf("after %q: cursor=%d queue=%+v cmd=%v", key, m.queueCur, m.queue, cmd != nil)
		}
	}
}

func TestClearQueueProjectsEmptyAndIgnoresStalePlaylist(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	m.pos, m.idle = 0, false
	m.timePos, m.duration = 10*time.Second, time.Minute
	got, cmd := m.handleQueueKey("C")
	m = got.(Model)
	if cmd == nil || len(m.queue) != 0 || m.queueCur != 0 || m.pos != -1 || !m.idle || m.timePos != 0 || m.duration != 0 {
		t.Fatalf("clear projection = queue %+v, cursor %d, pos %d, idle %v, time %v/%v", m.queue, m.queueCur, m.pos, m.idle, m.timePos, m.duration)
	}
	if m.queueEditsPending != 1 || m.queueProjection == nil {
		t.Fatal("clear did not reserve an authoritative queue refresh")
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"B"}]`)})
	if len(m.queue) != 0 {
		t.Fatal("stale playlist event repopulated cleared queue")
	}
	got, refresh := m.update(queueActionDoneMsg{requestID: m.activeRequest, status: "queue cleared", projected: true})
	m = got.(Model)
	if refresh == nil || m.status != "queue cleared" {
		t.Fatalf("clear completion = status %q, refresh %v", m.status, refresh != nil)
	}
	got, _ = m.update(queueRefreshMsg{revision: m.queueRevision, entries: nil, pos: -1})
	m = got.(Model)
	if len(m.queue) != 0 || m.pos != -1 || m.queueProjection != nil {
		t.Fatalf("clear refresh = queue %+v, pos %d, projection %v", m.queue, m.pos, m.queueProjection)
	}
}

func TestClearQueueFollowsPendingImportEvenWhenDisplayEmpty(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	importTask := m.reserveQueue()
	got, cmd := m.handleQueueKey("C")
	m = got.(Model)
	if cmd == nil || m.queueTail == importTask.done {
		t.Fatal("clear did not reserve a slot after the pending import")
	}
}

func TestQueueEnterReservesSlotAfterProjectedMove(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, move := m.handleQueueKey("K")
	m = got.(Model)
	if move == nil || m.queueCur != 0 {
		t.Fatal("move did not project B to index 0")
	}
	moveDone := m.queueTail
	got, play := m.handleQueueKey("enter")
	m = got.(Model)
	if play == nil || m.queueTail == moveDone {
		t.Fatal("play selection was not reserved behind the projected move")
	}
	if m.queueEditsPending != 1 {
		t.Errorf("pending projected edits = %d, want 1", m.queueEditsPending)
	}
}

func TestPlayNowBlocksIndexEditsUntilQueueRefresh(t *testing.T) {
	m := New(Deps{})
	m.focus = focusResults
	m.results = []youtube.Track{{URL: "D", Title: "D"}}
	m.queue = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, play := m.handleResultKey("enter")
	m = got.(Model)
	if play == nil || !m.queueInsertPending || m.queueEditsPending != 1 {
		t.Fatal("play-now did not reserve an insertion refresh")
	}
	m.focus = focusQueue
	for _, key := range []string{"d", "K", "J", "enter"} {
		got, cmd := m.handleQueueKey(key)
		m = got.(Model)
		if cmd != nil || len(m.queue) != 2 || m.queue[1].Filename != "B" {
			t.Fatalf("%q edited a stale queue index", key)
		}
	}
	got, refresh := m.update(queueActionDoneMsg{requestID: m.activeRequest, projected: true})
	m = got.(Model)
	if refresh == nil || !m.queueInsertPending {
		t.Fatal("insertion was unblocked before mpv queue refresh")
	}
	got, _ = m.update(queueRefreshMsg{revision: m.queueRevision, entries: []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "D"}, {Filename: "B"}}, pos: 1})
	m = got.(Model)
	if m.queueInsertPending || len(m.queue) != 3 || m.queue[1].Filename != "D" {
		t.Fatalf("insertion refresh = %+v; pending = %v", m.queue, m.queueInsertPending)
	}
	got, edit := m.handleQueueKey("d")
	m = got.(Model)
	if edit == nil || len(m.queue) != 2 || m.queue[1].Filename != "B" {
		t.Fatalf("delete did not target refreshed index: %+v", m.queue)
	}
}

func TestOpposingQueueMovesIgnoreOldAndIntermediateEvents(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 2
	for _, key := range []string{"K", "J"} {
		got, cmd := m.handleQueueKey(key)
		if cmd == nil {
			t.Fatalf("%s command = nil", key)
		}
		m = got.(Model)
	}
	if m.queueCur != 2 || m.queue[2].Filename != "C" {
		t.Fatalf("projection = %+v cursor %d, want original order with C selected", m.queue, m.queueCur)
	}
	for _, snapshot := range []string{
		`[{"filename":"A"},{"filename":"B"},{"filename":"C"}]`, // before either move
		`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`, // after only K
	} {
		m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(snapshot)})
		if m.queue[2].Filename != "C" || m.queueCur != 2 {
			t.Fatalf("stale event %s changed projected selection: %+v cursor %d", snapshot, m.queue, m.queueCur)
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
		if m.queue[2].Filename != "C" || m.queueCur != 2 {
			t.Fatalf("late event %s changed projected selection: %+v cursor %d", snapshot, m.queue, m.queueCur)
		}
	}
	got, _ := m.update(queueRefreshMsg{revision: m.queueRevision, entries: []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}, pos: -1})
	m = got.(Model)
	if m.queue[2].Filename != "C" || m.queueCur != 2 {
		t.Errorf("authoritative refresh = %+v cursor %d, want C selected", m.queue, m.queueCur)
	}
	got, _ = m.handleQueueKey("K")
	m = got.(Model)
	if m.queue[1].Filename != "C" || m.queueCur != 1 {
		t.Errorf("next move targeted wrong track: queue=%+v cursor=%d", m.queue, m.queueCur)
	}
}

func TestPlaylistEventBurstSchedulesOneAuthoritativeRead(t *testing.T) {
	m := New(Deps{})
	m.queueAuthoritative = true
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
	if cmd == nil || !m.queueDebouncePending || m.queueRefreshPending {
		t.Fatal("changed event generation did not extend debounce")
	}
	got, cmd = m.update(queueDebounceMsg{version: m.queueEventVersion})
	m = got.(Model)
	if cmd == nil || !m.queueRefreshPending {
		t.Fatal("settled burst did not schedule one authoritative read")
	}
}

func TestRapidQueueDeletesAdvanceToNextTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 0

	for range 2 {
		got, cmd := m.handleQueueKey("d")
		if cmd == nil {
			t.Fatal("delete command = nil")
		}
		m = got.(Model)
	}
	if len(m.queue) != 1 || m.queue[0].Filename != "C" {
		t.Errorf("queue = %+v, want only C", m.queue)
	}
}

func TestStalePlaylistEventDoesNotRestoreDeletedEntry(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{
		{Filename: "A"},
		{Filename: "B"},
		{Filename: "C"},
	}
	m.queueCur = 0
	got, cmd := m.handleQueueKey("d")
	if cmd == nil {
		t.Fatal("delete command = nil")
	}
	m = got.(Model)

	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":1,"filename":"A"},{"id":2,"filename":"B"},{"id":3,"filename":"C"}]`)})
	if len(m.queue) != 2 || m.queue[0].Filename != "B" {
		t.Fatalf("stale event restored deleted A: %+v", m.queue)
	}
	// An unrelated insert can coexist with the projected deletion.
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":2,"filename":"B"},{"id":4,"filename":"D"},{"id":3,"filename":"C"}]`)})
	if len(m.queue) != 2 || m.queue[0].Filename != "B" {
		t.Errorf("event bypassed authoritative refresh: queue=%+v", m.queue)
	}
	got, _ = m.update(queueActionDoneMsg{requestID: m.activeRequest, projected: true})
	m = got.(Model)
	got, _ = m.update(queueRefreshMsg{revision: m.queueRevision, entries: []mpv.PlaylistEntry{{Filename: "B"}, {Filename: "D"}, {Filename: "C"}}, pos: -1})
	m = got.(Model)
	if len(m.queue) != 3 || m.queue[1].Filename != "D" {
		t.Errorf("authoritative refresh missed insert: queue=%+v", m.queue)
	}
}

func TestProjectedDeleteDistinguishesDuplicateURLs(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue = []mpv.PlaylistEntry{{Filename: "same"}, {Filename: "same"}}
	got, _ := m.handleQueueKey("d")
	m = got.(Model)
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":1,"filename":"same"},{"id":2,"filename":"same"}]`)})
	if len(m.queue) != 1 || m.queue[0].Filename != "same" {
		t.Errorf("stale duplicate event restored removed entry: %+v", m.queue)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":2,"filename":"same"},{"id":3,"filename":"same"}]`)})
	if len(m.queue) != 1 {
		t.Errorf("duplicate event bypassed authoritative refresh: queue=%+v", m.queue)
	}
}

func TestQueueWriteErrorIsReportedAfterWrite(t *testing.T) {
	m := New(Deps{Thumbnails: true})
	m.graphics = graphicsNone
	id := m.nextRequest()
	task := m.reserveQueue()
	want := errors.New("mpv rejected append")
	tracks := []youtube.Track{{ID: "x", Title: "resolved first", URL: "x"}, {ID: "y", Title: "resolved second", URL: "y"}}
	m.queue, m.pos, m.idle = []mpv.PlaylistEntry{{Filename: "x"}}, 0, false
	accepted := ""
	msg := fetchQueue(searchStub{tracks: tracks}, func(tracks []youtube.Track) error {
		accepted = tracks[0].URL // mpv accepted this entry before rejecting the next one.
		return want
	}, youtube.Link{URL: "x"}, id, task)()
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
	if m.thumbVideo != "x" {
		t.Errorf("thumbnail target = %q, want accepted track x after metadata arrives", m.thumbVideo)
	}
}

func TestQueueRefreshPreservesEntryTitlesWithDuplicateURLs(t *testing.T) {
	m := New(Deps{})
	m.queueAuthoritative = true
	entries := []mpv.PlaylistEntry{
		{Filename: "same", Title: "first title", Current: false},
		{Filename: "same", Title: "second title", Current: true, Playing: true},
	}
	got, _ := m.update(queueRefreshMsg{entries: entries, pos: 1})
	m = got.(Model)
	if len(m.queue) != 2 || m.queue[0].Title != "first title" || m.queue[1].Title != "second title" {
		t.Fatalf("refreshed entries = %+v, want distinct titles", m.queue)
	}
	if title := displayTitle(m.queue[1], youtube.Track{}); title != "second title" {
		t.Errorf("display title = %q, want second title", title)
	}
	if m.pos != 1 || !m.queue[1].Playing {
		t.Errorf("position = %d, playing = %v, want second entry playing", m.pos, m.queue[1].Playing)
	}
}

func TestRestoredMetadataTitlesUnplayedQueueEntries(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{InitialTracks: map[string]youtube.Track{
		url: {URL: url, ID: "abc", Title: "Saved song", Channel: "Artist"},
	}})
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"https://www.youtube.com/watch?v=abc"}]`)})
	if got := m.queueLine(0, m.queue[0], 60); !strings.Contains(got, "Saved song") || strings.Contains(got, url) {
		t.Errorf("restored queue line = %q, want saved title", got)
	}
}

func TestFailedQueueActionReleasesNextWrite(t *testing.T) {
	m := New(Deps{})
	first := m.reserveQueue()
	second := m.reserveQueue()
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

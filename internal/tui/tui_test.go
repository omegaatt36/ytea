package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/omegaatt36/ytea/internal/library"
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

func TestCursorFollowsFocusedInput(t *testing.T) {
	const typed = "lofi beats"
	tests := []struct {
		name  string
		setup func(m *Model)
	}{
		{name: "search", setup: func(m *Model) {
			m.input.SetValue(typed)
			m.input.CursorEnd()
		}},
		{name: "playlist name", setup: func(m *Model) {
			m.overlay = overlayName
			m.input.Blur()
			m.nameInput.Focus()
			m.nameInput.SetValue(typed)
			m.nameInput.CursorEnd()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, _ := New(Deps{}).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m := next.(Model)
			tt.setup(&m)

			c := m.View().Cursor
			if c == nil {
				t.Fatal("View().Cursor = nil, want the input's cursor")
			}
			lines := strings.Split(m.render(), "\n")
			if c.Y < 0 || c.Y >= len(lines) {
				t.Fatalf("cursor row %d outside the %d rendered rows", c.Y, len(lines))
			}
			if got := ansi.Strip(ansi.Cut(lines[c.Y], c.X-len(typed), c.X)); got != typed {
				t.Errorf("cells before cursor (%d,%d) = %q, want %q", c.X, c.Y, got, typed)
			}
		})
	}
}

func atPane(m Model, f focus) bool {
	return m.overlay == overlayNone && m.focus == f
}

func TestNoCursorOrBlinkOutsideInputs(t *testing.T) {
	m := New(Deps{})
	if cmd := m.focusSearch(); cmd != nil {
		t.Error("focusing search returned a command, want none: the terminal blinks the cursor")
	}
	m.focus = focusResults
	m.input.Blur()
	if c := m.View().Cursor; c != nil {
		t.Errorf("View().Cursor = %+v with a list focused, want nil", c)
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

type spectrumStub struct{}

func (spectrumStub) Levels() <-chan []float64 { return nil }

func TestFooterListsVizOnlyWithSpectrum(t *testing.T) {
	for _, tc := range []struct {
		name string
		tap  Spectrum
		want bool
	}{
		{"without spectrum", nil, false},
		{"with spectrum", spectrumStub{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(Deps{Tap: tc.tap})
			m.width = 500
			if got := strings.Contains(m.renderFooter(), "v viz"); got != tc.want {
				t.Errorf("footer contains %q = %v, want %v", "v viz", got, tc.want)
			}
		})
	}
}

func TestLocalPlaylistFlow(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := youtube.Track{ID: "one", Title: "First", URL: "https://www.youtube.com/watch?v=one"}
	second := youtube.Track{ID: "two", Title: "Second", URL: "https://www.youtube.com/watch?v=two"}
	m := New(Deps{Library: store})
	m.focus = focusResults
	m.results = []youtube.Track{first, second}
	got, _ := m.handleResultKey("s")
	m = got.(Model)
	if m.overlay != overlayName {
		t.Fatalf("save without lists: overlay=%v, want name input", m.overlay)
	}
	m.nameInput.SetValue("Favorites")
	got, _ = m.handlePlaylistNameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	if !atPane(m, focusResults) || len(store.Playlists()) != 1 || len(store.Playlists()[0].Tracks) != 1 {
		t.Fatalf("created playlist and saved song: focus=%v overlay=%v playlists=%+v", m.focus, m.overlay, store.Playlists())
	}
	m.resultCur = 1
	got, _ = m.handleResultKey("s")
	m = got.(Model)
	if m.overlay != overlayPicker {
		t.Fatalf("save with existing list: overlay=%v, want picker", m.overlay)
	}
	got, _ = m.handlePlaylistKey("enter")
	m = got.(Model)
	if !atPane(m, focusResults) || len(store.Playlists()[0].Tracks) != 2 {
		t.Fatalf("saved second song: focus=%v overlay=%v playlists=%+v", m.focus, m.overlay, store.Playlists())
	}
	m.cycleTab(false)
	m.cycleTab(false)
	if !atPane(m, focusPlaylists) {
		t.Fatalf("two tabs from results: focus=%v overlay=%v", m.focus, m.overlay)
	}
	got, _ = m.handlePlaylistKey("enter")
	m = got.(Model)
	if !atPane(m, focusPlaylistTracks) {
		t.Fatalf("enter list: focus=%v overlay=%v", m.focus, m.overlay)
	}
	m.playlistTrackCur = 1
	got, _ = m.handlePlaylistKey("d")
	m = got.(Model)
	if len(store.Playlists()[0].Tracks) != 1 || store.Playlists()[0].Tracks[0].URL != first.URL {
		t.Fatalf("after removal = %+v", store.Playlists())
	}
	m.width, m.height = 80, 24
	if w, h := lipgloss.Width(m.render()), lipgloss.Height(m.render()); w > 80 || h > 24 {
		t.Errorf("playlist render size = %dx%d", w, h)
	}
}

type spyPlaylistPlayer struct {
	*mpv.Player
	calls []string
	err   error
}

func (p *spyPlaylistPlayer) PlayNow(_ context.Context, url string) error {
	p.calls = append(p.calls, "play "+url)
	return p.err
}

func (p *spyPlaylistPlayer) Append(_ context.Context, url string) error {
	p.calls = append(p.calls, "append "+url)
	return p.err
}

func (p *spyPlaylistPlayer) AppendAll(_ context.Context, urls []string) error {
	p.calls = append(p.calls, "append "+strings.Join(urls, ","))
	return p.err
}

func (p *spyPlaylistPlayer) Stop(context.Context) error {
	p.calls = append(p.calls, "stop")
	return p.err
}

func TestPlaylistTrackKeyRunsPlayerCommand(t *testing.T) {
	for _, tt := range []struct {
		name       string
		key        rune
		wantCall   string
		wantStatus string
		projected  bool
		err        error
	}{
		{name: "play", key: tea.KeyEnter, wantCall: "play second", wantStatus: "playing", projected: true},
		{name: "append", key: 'a', wantCall: "append second", wantStatus: "queued"},
		{name: "append failure", key: 'a', wantCall: "append second", err: errors.New("mpv rejected append")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := library.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tracks := []youtube.Track{{URL: "first", Title: "First"}, {URL: "second", Title: "Second"}}
			if _, err := store.CreateWithTracks("Favorites", tracks); err != nil {
				t.Fatal(err)
			}
			player := &spyPlaylistPlayer{err: tt.err}
			m := New(Deps{Library: store, Player: player})
			m.focus = focusPlaylistTracks
			m.playlistTrackCur = 1

			got, cmd := m.update(tea.KeyPressMsg{Code: tt.key})
			m = got.(Model)
			if cmd == nil {
				t.Fatal("playlist key did not schedule a player command")
			}
			if len(player.calls) != 0 {
				t.Fatalf("player was called before the command ran: %v", player.calls)
			}
			if m.tracks["second"].Title != "Second" {
				t.Fatalf("selected track metadata was lost: %+v", m.tracks["second"])
			}

			result := cmd()
			done, ok := result.(queueActionDoneMsg)
			if !ok {
				t.Fatalf("command result has type %T, want queueActionDoneMsg", result)
			}
			if len(player.calls) != 1 || player.calls[0] != tt.wantCall {
				t.Fatalf("player calls = %v, want [%s]", player.calls, tt.wantCall)
			}
			if done.projected != tt.projected || !errors.Is(done.err, tt.err) {
				t.Fatalf("command result = %+v, want projected=%v error=%v", done, tt.projected, tt.err)
			}
			got, _ = m.update(done)
			m = got.(Model)
			if tt.err != nil {
				if !m.statusErr || m.status != tt.err.Error() {
					t.Fatalf("failed command status = %q (error=%v)", m.status, m.statusErr)
				}
			} else if m.statusErr || !strings.Contains(m.status, tt.wantStatus) {
				t.Fatalf("command status = %q (error=%v), want %q", m.status, m.statusErr, tt.wantStatus)
			}
		})
	}
}

func TestPlaylistNamePasteStaysInNameInput(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusPlaylists
	got, _ := m.handlePlaylistKey("c")
	m = got.(Model)
	got, _ = m.Update(tea.PasteMsg{Content: "Morning"})
	m = got.(Model)
	if m.overlay != overlayName || m.nameInput.Value() != "Morning" || m.input.Value() != "" {
		t.Errorf("paste routing: overlay=%v name=%q search=%q", m.overlay, m.nameInput.Value(), m.input.Value())
	}
}

func TestSaveQueueAsPlaylist(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusQueue
	firstURL := "https://www.youtube.com/watch?v=one"
	secondURL := "https://www.youtube.com/watch?v=two"
	m.queue.entries = []mpv.PlaylistEntry{
		{Filename: firstURL, Title: "First"},
		{Filename: secondURL, Title: "Second"},
		{Filename: firstURL, Title: "First"},
	}
	got, _ := m.handleQueueKey("S")
	m = got.(Model)
	if m.overlay != overlayName {
		t.Fatalf("save queue: overlay=%v, want name input", m.overlay)
	}
	m.nameInput.SetValue("Imported")
	got, _ = m.handlePlaylistNameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	playlists := store.Playlists()
	if !atPane(m, focusPlaylists) || len(playlists) != 1 || len(playlists[0].Tracks) != 2 || playlists[0].Tracks[0].URL != firstURL || playlists[0].Tracks[1].URL != secondURL {
		t.Fatalf("saved queue = %+v, focus=%v overlay=%v", playlists, m.focus, m.overlay)
	}
}

func TestDeletePlaylistNeedsSecondPress(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("Keep"); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusPlaylists
	got, _ := m.handlePlaylistKey("D")
	m = got.(Model)
	if len(store.Playlists()) != 1 || m.deletePlaylistPending != 0 {
		t.Fatalf("first D deleted playlist: %+v", store.Playlists())
	}
	got, _ = m.handlePlaylistKey("D")
	m = got.(Model)
	if len(store.Playlists()) != 0 || m.deletePlaylistPending != -1 {
		t.Fatalf("second D did not delete playlist: %+v", store.Playlists())
	}
}

func TestPasteGoesToSearch(t *testing.T) {
	m := New(Deps{})
	m.focus = focusResults
	m.input.Blur()

	got, _ := m.Update(tea.PasteMsg{Content: "joe hisaishi"})
	gm := got.(Model)
	if !atPane(gm, focusSearch) {
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
			if got := m.(Model).thumb.graphics; got != tt.want {
				t.Errorf("graphics = %v, want %v", got, tt.want)
			}
		})
	}
}

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
			if !atPane(gm, focusSearch) {
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

func playingModel(g graphicsSupport) Model {
	const url = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = g
	m.width, m.height = 100, 30
	m.idle, m.queue.pos = false, 0
	m.queue.entries = []mpv.PlaylistEntry{{Filename: url}}
	m.tracks[url] = youtube.Track{ID: "abc", Title: "Song", URL: url}
	m.thumb.video = "abc"
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
			got, cmd := playingModel(tt.graphics).update(thumbMsg{videoID: "abc", img: img})
			gm := got.(Model)

			if gotKitty := gm.thumb.id != 0; gotKitty != tt.wantKitty {
				t.Errorf("kitty image = %v, want %v", gotKitty, tt.wantKitty)
			}
			if gotArt := gm.thumb.art != ""; gotArt == tt.wantKitty {
				t.Errorf("half-block art = %v, want %v", gotArt, !tt.wantKitty)
			}
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
		m.thumb.id, m.showViz = 16, viz

		at := m.thumbOrigin()
		lines := strings.Split(ansi.Strip(m.render()), "\n")
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
	m.thumb.id = 16

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
	if !next.(Model).thumb.placed {
		t.Error("after resize: placed = false, want a placement rescheduled by Update")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height}); cmd == nil {
		t.Error("same-size resize: cmd = nil, want re-placement since the terminal drops placed images")
	}

	if _, cmd := m.update(placeMsg{id: 99, at: m.thumb.placedAt}); cmd != nil {
		t.Errorf("placeMsg for a replaced image: cmd = %v, want nil", cmd)
	}
	if _, cmd := m.update(placeMsg{id: 16, at: m.thumb.placedAt}); cmd == nil {
		t.Error("placeMsg for the current image: cmd = nil, want Put")
	}
}

type searchStub struct {
	tracks []youtube.Track
}

func (s searchStub) Search(_ context.Context, _ string, _ int) ([]youtube.Track, error) {
	return nil, errors.New("unexpected search")
}

func (s searchStub) Lookup(_ context.Context, _ string, _ int) ([]youtube.Track, error) {
	return s.tracks, nil
}

func TestSearchEnterImportsYouTubeLink(t *testing.T) {
	player := &spyPlaylistPlayer{}
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

type appendRecorder struct {
	*mpv.Player
	writes chan string
}

func (p appendRecorder) AppendAll(_ context.Context, urls []string) error {
	p.writes <- urls[0]
	return nil
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

func TestRapidQueueMovesKeepSelectedTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
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
		got, cmd := m.handleQueueKey(key)
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
	m.queue.pos, m.idle = 0, false
	m.timePos, m.duration = 10*time.Second, time.Minute
	got, cmd := m.handleQueueKey("C")
	m = got.(Model)
	if cmd == nil || len(m.queue.entries) != 0 || m.queueCur != 0 || m.queue.pos != -1 || !m.idle || m.timePos != 0 || m.duration != 0 {
		t.Fatalf("clear projection = queue %+v, cursor %d, pos %d, idle %v, time %v/%v", m.queue.entries, m.queueCur, m.queue.pos, m.idle, m.timePos, m.duration)
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
	player := &spyPlaylistPlayer{}
	m := New(Deps{Player: player})
	m.input.SetValue("https://youtu.be/x1")
	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	importID := m.activeRequest

	m.focus = focusQueue
	got, clear := m.handleQueueKey("C")
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
	got, move := m.handleQueueKey("K")
	m = got.(Model)
	if move == nil || m.queueCur != 0 {
		t.Fatal("move did not project B to index 0")
	}
	moveDone := m.queue.tail
	got, play := m.handleQueueKey("enter")
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
	m.results = []youtube.Track{{URL: "D", Title: "D"}}
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, play := m.handleResultKey("enter")
	m = got.(Model)
	if play == nil || !m.queue.insertPending || m.queue.editsPending != 1 {
		t.Fatal("play-now did not reserve an insertion refresh")
	}
	m.focus = focusQueue
	for _, key := range []string{"d", "K", "J", "enter"} {
		got, cmd := m.handleQueueKey(key)
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
	got, edit := m.handleQueueKey("d")
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
		got, cmd := m.handleQueueKey(key)
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
	got, _ = m.handleQueueKey("K")
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
		got, cmd := m.handleQueueKey("d")
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
	got, cmd := m.handleQueueKey("d")
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
	got, _ := m.handleQueueKey("d")
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

func TestRestoredMetadataTitlesUnplayedQueueEntries(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{InitialTracks: map[string]youtube.Track{
		url: {URL: url, ID: "abc", Title: "Saved song", Channel: "Artist"},
	}})
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"https://www.youtube.com/watch?v=abc"}]`)})
	if got := m.queueLine(0, m.queue.entries[0], 60); !strings.Contains(got, "Saved song") || strings.Contains(got, url) {
		t.Errorf("restored queue line = %q, want saved title", got)
	}
}

func TestFailedQueueActionReleasesNextWrite(t *testing.T) {
	m := New(Deps{})
	first := m.queue.reserve()
	second := m.queue.reserve()
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

func cellAt(t *testing.T, m Model, text string) image.Point {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.render()), "\n") {
		if before, _, ok := strings.Cut(line, text); ok {
			return image.Pt(lipgloss.Width(before), y)
		}
	}
	t.Fatalf("%q not rendered", text)
	return image.Point{}
}

func click(m Model, at image.Point) Model {
	next, _ := m.Update(tea.MouseClickMsg{X: at.X, Y: at.Y, Button: tea.MouseLeft})
	return next.(Model)
}

func wheel(m Model, at image.Point, button tea.MouseButton) Model {
	next, _ := m.Update(tea.MouseWheelMsg{X: at.X, Y: at.Y, Button: button})
	return next.(Model)
}

func mouseModel() Model {
	m := New(Deps{})
	m.width, m.height = 100, 30
	for i := range 40 {
		m.results = append(m.results, youtube.Track{Title: fmt.Sprintf("song %02d", i)})
	}
	for i := range 3 {
		m.queue.entries = append(m.queue.entries, mpv.PlaylistEntry{Filename: fmt.Sprintf("u%d", i), Title: fmt.Sprintf("queued %d", i)})
	}
	return m
}

func TestMouseEnablesCellMotionOnly(t *testing.T) {
	if got := New(Deps{}).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want cell motion so hovering never renders", got)
	}
}

func TestClickSelectsRowUnderPointer(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.resultCur = 35

	m = click(m, cellAt(t, m, "song 30"))
	if !atPane(m, focusResults) || m.resultCur != 30 {
		t.Errorf("click song 30: focus=%v resultCur=%d, want results/30", m.focus, m.resultCur)
	}

	m = click(m, cellAt(t, m, "queued 2"))
	if !atPane(m, focusQueue) || m.queueCur != 2 {
		t.Errorf("click queued 2: focus=%v queueCur=%d, want queue/2", m.focus, m.queueCur)
	}

	m = click(m, cellAt(t, m, "queued 2").Add(image.Pt(0, 3)))
	if !atPane(m, focusQueue) || m.queueCur != 2 {
		t.Errorf("click below queue: focus=%v queueCur=%d, want queue/2", m.focus, m.queueCur)
	}
}

func TestClickHeader(t *testing.T) {
	m := mouseModel()
	if !atPane(m, focusSearch) {
		t.Fatalf("new model focus = %v, want search", m.focus)
	}

	m = click(m, cellAt(t, m, "Queue"))
	if !atPane(m, focusQueue) || m.input.Focused() {
		t.Errorf("click Queue tab: focus=%v input focused=%v, want queue and blurred input", m.focus, m.input.Focused())
	}
	m = click(m, cellAt(t, m, "Playlists"))
	if !atPane(m, focusPlaylists) {
		t.Errorf("click Playlists tab: focus=%v", m.focus)
	}
	m = click(m, cellAt(t, m, "Playlists").Sub(image.Pt(1, 0))) // the gap between tabs
	if !atPane(m, focusPlaylists) {
		t.Errorf("click between tabs: focus=%v, want unchanged", m.focus)
	}
	m = click(m, cellAt(t, m, " / "))
	if !atPane(m, focusSearch) || !m.input.Focused() {
		t.Errorf("click search box: focus=%v input focused=%v", m.focus, m.input.Focused())
	}
}

func TestWheelMovesCursorOfPaneUnderPointer(t *testing.T) {
	m := mouseModel()
	at := cellAt(t, m, "queued 0")

	m = wheel(m, at, tea.MouseWheelDown)
	if !atPane(m, focusQueue) || m.queueCur != 1 || m.input.Focused() {
		t.Errorf("wheel down over queue: focus=%v queueCur=%d input focused=%v", m.focus, m.queueCur, m.input.Focused())
	}
	for range 5 {
		m = wheel(m, at, tea.MouseWheelDown)
	}
	if m.queueCur != 2 {
		t.Errorf("wheel past end: queueCur=%d, want 2", m.queueCur)
	}

	m = wheel(m, cellAt(t, m, "song 00"), tea.MouseWheelUp)
	if !atPane(m, focusResults) || m.resultCur != 0 {
		t.Errorf("wheel up over results top: focus=%v resultCur=%d", m.focus, m.resultCur)
	}
}

func TestMouseInPlaylistPicker(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := store.CreateWithTracks(name, []youtube.Track{{Title: name + " song", URL: "https://www.youtube.com/watch?v=" + name}}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(Deps{Library: store})
	m.width, m.height = 100, 30
	m.focus = focusResults
	m.results = []youtube.Track{{Title: "pick me", URL: "https://www.youtube.com/watch?v=pick"}}
	got, _ := m.handleResultKey("s")
	m = got.(Model)

	m = click(m, cellAt(t, m, "Beta ("))
	if m.overlay != overlayPicker || m.playlistCur != 1 {
		t.Errorf("click Beta in picker: overlay=%v playlistCur=%d, want picker/1", m.overlay, m.playlistCur)
	}
	m = click(m, cellAt(t, m, "Beta song"))
	if m.overlay != overlayPicker {
		t.Errorf("click tracks pane in picker: overlay=%v, want picker kept", m.overlay)
	}
}

func TestMouseIgnoredWhileNamingPlaylist(t *testing.T) {
	m := mouseModel()
	m.overlay = overlayName
	m.input.Blur()
	m = click(m, image.Pt(1, 0))
	if m.overlay != overlayName {
		t.Errorf("click during name input: overlay=%v, want name input kept", m.overlay)
	}
}

type streamStub struct {
	*mpv.Player
	info mpv.StreamInfo
}

func (p streamStub) StreamInfo(context.Context) (mpv.StreamInfo, error) { return p.info, nil }

func TestInfoPanel(t *testing.T) {
	const watch = "https://www.youtube.com/watch?v=abc"
	info := mpv.StreamInfo{
		Path:   watch,
		Opened: "edl://!no_clip;%99%https://rr1.googlevideo.com/videoplayback?itag=251&mime=audio%2Fwebm&clen=4000000&dur=200",
		Codec:  "Opus (Opus Interactive Audio Codec)",
	}
	m := New(Deps{Player: streamStub{info: info}})
	m.width, m.height = 120, 40
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watch}}
	m.queue.pos, m.idle = 0, false
	m.tracks[watch] = youtube.Track{ID: "abc", Title: "Song", URL: watch}

	got, cmd := m.update(tea.KeyPressMsg{Code: 'i'})
	m = got.(Model)
	got, _ = m.update(cmd())
	m = got.(Model)
	if m.overlay != overlayInfo {
		t.Fatalf("overlay = %v, want info panel", m.overlay)
	}
	body := ansi.Strip(strings.Join(m.infoLines(), "\n"))
	for _, want := range []string{watch, "Opus (Opus", "itag 251 · audio/webm", "160 kbps average", "3.8 MiB"} {
		if !strings.Contains(body, want) {
			t.Errorf("info lines missing %q:\n%s", want, body)
		}
	}

	m.queue.entries = []mpv.PlaylistEntry{{Filename: "https://www.youtube.com/watch?v=next"}}
	if body := strings.Join(m.infoLines(), "\n"); strings.Contains(body, "itag") {
		t.Errorf("info shows the previous track's stream:\n%s", body)
	}
	if cmd := m.applyEvent(mpv.Event{Name: "file-loaded"}); cmd == nil {
		t.Error("file-loaded did not refresh the open info panel")
	}

	got, _ = m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m = got.(Model); !atPane(m, focusQueue) {
		t.Errorf("after esc: focus=%v overlay=%v, want queue", m.focus, m.overlay)
	}
}

func overlayModel(t *testing.T, origin focus) Model {
	t.Helper()
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const watch = "https://www.youtube.com/watch?v=abc"
	if _, err := store.CreateWithTracks("Keep", []youtube.Track{{Title: "Kept", URL: watch}}); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store, Player: streamStub{info: mpv.StreamInfo{Path: watch}}})
	m.width, m.height = 120, 40
	m.input.Blur()
	m.focus = origin
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watch, Title: "Song"}}
	m.queue.pos, m.idle = 0, false
	m.tracks[watch] = youtube.Track{ID: "abc", Title: "Song", URL: watch}
	m.devices = []mpv.AudioDevice{{Name: "auto", Description: "Autoselect device"}}
	return m
}

func press(t *testing.T, m Model, k tea.KeyPressMsg) Model {
	t.Helper()
	got, cmd := m.update(k)
	m = got.(Model)
	if k.Code == 'i' && cmd != nil {
		got, _ = m.update(cmd())
		m = got.(Model)
	}
	return m
}

func TestDevicePickerCloseRestoresOriginPane(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'o'}, {Code: 'q'}, {Code: tea.KeyEnter}} {
		t.Run(k.String(), func(t *testing.T) {
			m := overlayModel(t, focusQueue)
			got, _ := m.update(devicesMsg{devices: m.devices, open: true})
			m = got.(Model)
			if m.overlay != overlayDevices {
				t.Fatalf("overlay = %v, want device picker", m.overlay)
			}
			if m = press(t, m, k); !atPane(m, focusQueue) {
				t.Errorf("after %s: focus=%v overlay=%v, want queue", k, m.focus, m.overlay)
			}
		})
	}
}

func TestInfoCloseRestoresOriginPane(t *testing.T) {
	for name, origin := range map[string]focus{"queue": focusQueue, "playlists": focusPlaylists} {
		for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'i'}, {Code: 'q'}} {
			t.Run(name+"/"+k.String(), func(t *testing.T) {
				m := press(t, overlayModel(t, origin), tea.KeyPressMsg{Code: 'i'})
				if m.overlay != overlayInfo {
					t.Fatalf("overlay = %v, want info", m.overlay)
				}
				got, _ := m.update(k)
				if m = got.(Model); !atPane(m, origin) {
					t.Errorf("after %s: focus=%v overlay=%v, want %v", k, m.focus, m.overlay, origin)
				}
			})
		}
	}
}

func TestPlaylistPickerCloseRestoresOriginPane(t *testing.T) {
	m := press(t, overlayModel(t, focusQueue), tea.KeyPressMsg{Code: 's'})
	if m.overlay != overlayPicker {
		t.Fatalf("overlay = %v, want picker", m.overlay)
	}
	if m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}); !atPane(m, focusQueue) {
		t.Errorf("after esc: focus=%v overlay=%v, want queue", m.focus, m.overlay)
	}
}

func TestPlaylistNameCancelRestoresOriginPane(t *testing.T) {
	tests := []struct {
		name   string
		origin focus
		opens  []rune
	}{
		{name: "create from playlists", origin: focusPlaylists, opens: []rune{'c'}},
		{name: "create from picker opened in queue", origin: focusQueue, opens: []rune{'s', 'c'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := overlayModel(t, tt.origin)
			for _, r := range tt.opens {
				m = press(t, m, tea.KeyPressMsg{Code: r})
			}
			if m.overlay != overlayName {
				t.Fatalf("overlay = %v, want name input", m.overlay)
			}
			if m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}); !atPane(m, tt.origin) {
				t.Errorf("after esc: focus=%v overlay=%v, want %v", m.focus, m.overlay, tt.origin)
			}
		})
	}
}

func TestReplacedOverlayKeepsFirstOriginPane(t *testing.T) {
	m := press(t, overlayModel(t, focusQueue), tea.KeyPressMsg{Code: 's'})
	if m = press(t, m, tea.KeyPressMsg{Code: 'i'}); m.overlay != overlayInfo {
		t.Fatalf("overlay = %v, want info replacing picker", m.overlay)
	}
	if m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}); !atPane(m, focusQueue) {
		t.Errorf("after esc: focus=%v overlay=%v, want queue", m.focus, m.overlay)
	}
}

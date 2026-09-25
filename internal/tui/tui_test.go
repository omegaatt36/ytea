package tui

import (
	"encoding/json"
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

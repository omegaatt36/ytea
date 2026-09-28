package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/mpv"
)

func TestHyperlinkAnsi(t *testing.T) {
	url := "https://www.youtube.com/watch?v=abc"
	hl := ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()
	stripped := ansi.Strip(hl)
	if stripped != url {
		t.Errorf("stripped = %q, want %q", stripped, url)
	}
	width := ansi.StringWidth(hl)
	if width != len(url) {
		t.Errorf("width = %d, want %d", width, len(url))
	}
	bg := "hello world background line"
	rendered := lipgloss.NewCompositor(
		lipgloss.NewLayer(bg),
		lipgloss.NewLayer(hl).X(0).Y(0).Z(1),
	).Render()
	if !strings.Contains(rendered, url) {
		t.Errorf("rendered does not contain url: %q", rendered)
	}
	fitted := fit(hl, 45)
	if !strings.Contains(fitted, url) {
		t.Errorf("fit missing url: %q", fitted)
	}
	if ansi.StringWidth(fitted) != 45 {
		t.Errorf("fit width = %d, want 45", ansi.StringWidth(fitted))
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
			if got := m.player.deviceName(); got != m.devices[tt.wantIndex].Name {
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

func TestInfoPanel(t *testing.T) {
	m := overlayModel(t, focusQueue)
	m.deps.Player.(*spyPlayer).info = mpv.StreamInfo{
		Path:   watchURL,
		Opened: "edl://!no_clip;%99%https://rr1.googlevideo.com/videoplayback?itag=251&mime=audio%2Fwebm&clen=4000000&dur=200",
		Codec:  "Opus (Opus Interactive Audio Codec)",
	}
	if m = openInfo(t, m); m.overlay != overlayInfo {
		t.Fatalf("overlay = %v, want info panel", m.overlay)
	}
	rawLines := strings.Join(m.infoLines(), "\n")
	wantLink := ansi.SetHyperlink(watchURL) + watchURL + ansi.ResetHyperlink()
	if !strings.Contains(rawLines, wantLink) {
		t.Errorf("info lines missing hyperlink %q:\n%s", wantLink, rawLines)
	}
	body := ansi.Strip(rawLines)
	for _, want := range []string{watchURL, "Opus (Opus", "itag 251 · audio/webm", "160 kbps average", "3.8 MiB"} {
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
}

// Closing a dialog returns to the pane it was opened from, even when another
// dialog replaced it in between.
func TestOverlayCloseRestoresOriginPane(t *testing.T) {
	keys := func(ks ...string) func(*testing.T, Model) Model {
		return func(t *testing.T, m Model) Model {
			for _, k := range ks {
				m = press(t, m, keyPress(k))
			}
			return m
		}
	}
	openDevices := func(_ *testing.T, m Model) Model {
		got, _ := m.update(devicesMsg{devices: m.devices, open: true})
		return got.(Model)
	}
	tests := []struct {
		name   string
		origin focus
		open   func(*testing.T, Model) Model
		want   overlay
		closes []string
	}{
		{"device picker", focusQueue, openDevices, overlayDevices, []string{"esc", "o", "q", "enter"}},
		{"info from queue", focusQueue, openInfo, overlayInfo, []string{"esc", "i", "q"}},
		{"info from playlists", focusPlaylists, openInfo, overlayInfo, []string{"esc", "i", "q"}},
		{"playlist picker", focusQueue, keys("s"), overlayPicker, []string{"esc"}},
		{"name from playlists", focusPlaylists, keys("c"), overlayName, []string{"esc"}},
		{"name from picker opened in queue", focusQueue, keys("s", "c"), overlayName, []string{"esc"}},
		{"info replacing picker", focusQueue, func(t *testing.T, m Model) Model { return openInfo(t, keys("s")(t, m)) }, overlayInfo, []string{"esc"}},
	}
	for _, tt := range tests {
		for _, k := range tt.closes {
			t.Run(tt.name+"/"+k, func(t *testing.T) {
				m := tt.open(t, overlayModel(t, tt.origin))
				if m.overlay != tt.want {
					t.Fatalf("overlay = %v, want %v", m.overlay, tt.want)
				}
				if m = press(t, m, keyPress(k)); !atPane(m, tt.origin) {
					t.Errorf("after %s: focus=%v overlay=%v, want %v", k, m.focus, m.overlay, tt.origin)
				}
			})
		}
	}
}

func TestInfoOpenURL(t *testing.T) {
	for _, key := range []string{"o", "enter"} {
		t.Run(key, func(t *testing.T) {
			var opened string
			m := overlayModel(t, focusQueue)
			m.deps.OpenURL = func(u string) error {
				opened = u
				return nil
			}
			m = openInfo(t, m)
			if m.overlay != overlayInfo {
				t.Fatalf("overlay = %v, want info panel", m.overlay)
			}
			got, cmd := m.update(keyPress(key))
			m = got.(Model)
			if cmd == nil {
				t.Fatal("cmd = nil, want openURL cmd")
			}
			msg := cmd()
			if msg != nil {
				t.Errorf("cmd() = %v, want nil msg", msg)
			}
			if opened != watchURL {
				t.Errorf("opened = %q, want %q", opened, watchURL)
			}
		})
	}
}

func TestInfoRenderContainsHyperlink(t *testing.T) {
	m := overlayModel(t, focusQueue)
	m.width, m.height = 100, 30
	m = openInfo(t, m)
	out := m.render()
	if !strings.Contains(out, "]8;") {
		t.Fatalf("m.render() does not contain OSC 8 hyperlink: %q", out)
	}
}

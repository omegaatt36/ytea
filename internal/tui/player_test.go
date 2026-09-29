package tui

import (
	"encoding/json"
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestPlayerStateApply(t *testing.T) {
	playing := playerState{timePos: 7 * time.Second, duration: time.Minute, volume: 100}
	tests := []struct {
		prop, data  string
		from        playerState
		want        func(playerState) playerState
		trackChange bool
	}{
		{prop: mpv.PropTimePos, data: "12.5", want: func(p playerState) playerState { p.timePos = 12500 * time.Millisecond; return p }},
		{prop: mpv.PropDuration, data: "200", want: func(p playerState) playerState { p.duration = 200 * time.Second; return p }},
		{prop: mpv.PropPause, data: "true", want: func(p playerState) playerState { p.paused = true; return p }},
		{prop: mpv.PropVolume, data: "55", want: func(p playerState) playerState { p.volume = 55; return p }},
		{prop: mpv.PropCodec, data: `"opus"`, want: func(p playerState) playerState { p.codec = "opus"; return p }},
		{prop: mpv.PropAudioParams, data: `{"format":"floatp","samplerate":48000,"channels":"stereo"}`, want: func(p playerState) playerState {
			p.params = mpv.AudioParams{Format: "floatp", SampleRate: 48000, Channels: "stereo"}
			return p
		}},
		{prop: mpv.PropAudioDevice, data: `"pipewire/DX5"`, want: func(p playerState) playerState { p.device = "pipewire/DX5"; return p }},
		{prop: mpv.PropAF, data: `[{"label":"norm","name":"lavfi"}]`, want: func(p playerState) playerState { p.normalize = true; return p }},
		{prop: mpv.PropAF, data: `[]`, from: playerState{normalize: true}, want: func(p playerState) playerState { p.normalize = false; return p }},
		{prop: mpv.PropLoopPlaylist, data: `"inf"`, want: func(p playerState) playerState { p.loopPlaylist = true; return p }},
		{prop: mpv.PropLoopFile, data: `"inf"`, want: func(p playerState) playerState { p.loopFile = true; return p }},
		{prop: mpv.PropIdle, data: "true", trackChange: true, want: func(p playerState) playerState {
			p.idle, p.timePos, p.duration = true, 0, 0
			return p
		}},
		{prop: mpv.PropIdle, data: "false", from: playerState{idle: true}, trackChange: true, want: func(p playerState) playerState { p.idle = false; return p }},
		{prop: mpv.PropPlaylistPos, data: "3", trackChange: true, want: func(p playerState) playerState { p.timePos = 0; return p }},
	}
	for _, tt := range tests {
		t.Run(tt.prop+"="+tt.data, func(t *testing.T) {
			from := playing
			if tt.from != (playerState{}) {
				from = tt.from
			}
			got := from
			if changed := got.apply(mpv.Event{Prop: tt.prop, Data: json.RawMessage(tt.data)}); changed != tt.trackChange {
				t.Errorf("apply reported a track change = %v, want %v", changed, tt.trackChange)
			}
			if want := tt.want(from); got != want {
				t.Errorf("state = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRenderSpectrumSize(t *testing.T) {
	m := New(Deps{})
	m.levels = []float64{0, 0.25, 0.5, 1}
	got := m.renderSpectrum(40, detailRows)
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w != 40 || h != detailRows {
		t.Errorf("renderSpectrum() size = %dx%d, want 40x%d", w, h, detailRows)
	}
}

func TestRenderVUShowsTwinNeedleDials(t *testing.T) {
	m := New(Deps{})
	m.vu = [2]float64{0, 1}
	for _, width := range []int{26, 40, 46} {
		got := m.renderVU(width, detailRows)
		if w, h := lipgloss.Width(got), lipgloss.Height(got); w != width || h != detailRows {
			t.Errorf("renderVU(%d) size = %dx%d, want %dx%d", width, w, h, width, detailRows)
		}
	}
	got := ansi.Strip(m.renderVU(40, detailRows))
	if !strings.Contains(got, "L VU") || !strings.Contains(got, "R VU") || !strings.Contains(got, "−20") || !strings.Contains(got, "+3") || !strings.Contains(got, "●") {
		t.Errorf("renderVU() lacks analog dial details: %q", got)
	}
	if scale := ansi.Strip(analogDial(17, 0.5, 'L')[1]); !strings.Contains(scale, "−10") || !strings.Contains(scale, "0") {
		t.Errorf("17-column dial lacks intermediate scale marks: %q", scale)
	}
	if scale := ansi.Strip(analogDial(13, 0.5, 'L')[1]); !strings.Contains(scale, "−20") || !strings.Contains(scale, "+3") {
		t.Errorf("13-column dial lacks endpoint scale marks: %q", scale)
	}
	for _, width := range []int{18, 19} {
		base := ansi.Strip(analogDial(width, 0.5, 'L')[4])
		if pivot := slices.Index([]rune(base), '●'); pivot != (width-1)/2 {
			t.Errorf("%d-column dial pivot at %d, want %d", width, pivot, (width-1)/2)
		}
	}
	if strings.Contains(m.renderVU(40, detailRows), "\x1b[43m") {
		t.Error("analog VU uses a solid yellow face")
	}
	quiet, loud := analogDial(19, 0, 'L'), analogDial(19, 1, 'L')
	if strings.Join(quiet[2:4], "\n") == strings.Join(loud[2:4], "\n") {
		t.Error("analog needle stays in place across the full level range")
	}
	compact := ansi.Strip(m.renderVU(40, detailRows-1))
	if !strings.Contains(compact, "L VU") || !strings.Contains(compact, "R VU") || !strings.Contains(compact, "▲") {
		t.Errorf("compact VU lost its analog needle: %q", compact)
	}
}

func TestVUDoesNotResizePlayer(t *testing.T) {
	m := New(Deps{Tap: spectrumStub{}})
	m.vizMode = vizVU
	for _, size := range []image.Point{{80, 18}, {80, 24}, {100, 30}, {120, 40}} {
		m.width, m.height = size.X, size.Y
		want := playerRows
		if m.height >= 20 {
			want++
		}
		if got := m.screen().player.Dy(); got != want {
			t.Errorf("%dx%d VU player height = %d, want %d", size.X, size.Y, got, want)
		}
		lines := strings.Split(m.render(), "\n")
		if len(lines) != m.height {
			t.Errorf("render height = %d, want %d", len(lines), m.height)
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > m.width {
				t.Errorf("row %d width = %d, exceeds %d", i, w, m.width)
			}
		}
		m.vizMode = vizSpectrum
		if got := m.screen().player.Dy(); got != want {
			t.Errorf("%dx%d spectrum player height = %d, want %d", size.X, size.Y, got, want)
		}
		m.vizMode = vizVU
	}
	m.height = 18
	if got := m.screen().player.Dy(); got != playerRows {
		t.Errorf("small-window player height = %d, want %d", got, playerRows)
	}
	m.height = 24
	m.fullHelp = true
	if got := m.screen().player.Dy(); got != playerRows {
		t.Errorf("help player height = %d, want %d", got, playerRows)
	}
}

func TestPlayerSettingsStayOnBottomDetailRow(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 100, 30
	m.player.volume = 70
	lines := strings.Split(ansi.Strip(m.renderPlayer()), "\n")
	if !strings.Contains(lines[detailRows], "vol 70%") {
		t.Errorf("last detail row = %q, want volume settings", lines[detailRows])
	}
}

func TestPlayerDropsFillerRunesTerminalsDrawAsNothing(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 100, 30
	m.player.idle = false
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watchURL}}
	m.queue.pos = 0
	m.tracks[watchURL] = youtube.Track{Title: strings.Repeat("\u3164", 37), Channel: strings.Repeat("\u3164", 49) + "김"}
	lines := strings.Split(ansi.Strip(m.renderPlayer()), "\n")
	if strings.ContainsRune(strings.Join(lines, ""), '\u3164') {
		t.Fatalf("player renders hangul fillers:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[1], watchURL) {
		t.Errorf("title row = %q, want the URL in place of a blank title", lines[1])
	}
	if !strings.Contains(lines[2], "김") {
		t.Errorf("channel row = %q, want the visible part of the channel", lines[2])
	}
}

func TestVULevelUsesDecibelScale(t *testing.T) {
	for _, width := range []int{12, 19, 24} {
		low, zero, high := vuPosition(width, 0.05), vuPosition(width, 0.5), vuPosition(width, 0.707)
		if low >= zero || zero >= high {
			t.Errorf("dial width %d: needle at −20/0/+3 dB = %d/%d/%d, want increasing positions", width, low, zero, high)
		}
	}
}

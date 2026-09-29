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

func TestRenderVUMetersSizeAndNeedles(t *testing.T) {
	m := New(Deps{})
	m.vu = [2]float64{0, 1}
	for _, width := range []int{20, 40, 46} {
		got := m.renderVU(width, vuRows)
		if w, h := lipgloss.Width(got), lipgloss.Height(got); w != width || h != vuRows {
			t.Errorf("renderVU(%d) size = %dx%d, want %dx%d", width, w, h, width, vuRows)
		}
	}
	got := m.renderVU(40, vuRows)
	if !strings.Contains(got, "L VU") || !strings.Contains(got, "R VU") || !strings.Contains(got, "−20") || !strings.Contains(got, "+3") {
		t.Errorf("renderVU() has no meter markings: %q", got)
	}
	if !strings.Contains(ansi.Strip(got), "●") || !strings.ContainsAny(ansi.Strip(got), "╱╲│") {
		t.Errorf("renderVU() has no pivot or needle: %q", ansi.Strip(got))
	}
	quiet, loud := vuDialLarge(19, 0, 'L'), vuDialLarge(19, 1, 'L')
	if strings.Join(quiet[2:7], "\n") == strings.Join(loud[2:7], "\n") {
		t.Error("VU needle stayed in place across the full level range")
	}
}

func TestVUExpandsPlayerWhenWindowHasRoom(t *testing.T) {
	m := New(Deps{Tap: spectrumStub{}})
	m.vizMode = vizVU
	for _, size := range []image.Point{{80, 24}, {100, 30}, {120, 40}} {
		m.width, m.height = size.X, size.Y
		if got := m.screen().player.Dy(); got != vuRows+3 {
			t.Errorf("%dx%d VU player height = %d, want %d", size.X, size.Y, got, vuRows+3)
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

func TestVUNeedleUsesDecibelScale(t *testing.T) {
	position := func(level float64) int {
		row, _, _ := strings.Cut(ansi.Strip(vuDial(20, level, 'L')[2]), "\n")
		return slices.Index([]rune(row), '▲')
	}
	if low, zero, high := position(0.05), position(0.5), position(0.707); low > 2 || zero < 15 || zero >= high {
		t.Errorf("VU needle positions for −20/0/+3 dB = %d/%d/%d, want left/near right/right", low, zero, high)
	}
}

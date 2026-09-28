package tui

import (
	"encoding/json"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

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

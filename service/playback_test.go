package service

import (
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

func TestPlaybackApply(t *testing.T) {
	playing := Playback{TimePos: 7 * time.Second, Duration: time.Minute, Volume: 100}
	tests := []struct {
		name        string
		ev          PlayerEvent
		from        Playback
		want        func(Playback) Playback
		trackChange bool
	}{
		{name: "position", ev: PositionChanged{Pos: 12500 * time.Millisecond}, want: func(p Playback) Playback { p.TimePos = 12500 * time.Millisecond; return p }},
		{name: "duration", ev: DurationChanged{Duration: 200 * time.Second}, want: func(p Playback) Playback { p.Duration = 200 * time.Second; return p }},
		{name: "pause", ev: PauseChanged{Paused: true}, want: func(p Playback) Playback { p.Paused = true; return p }},
		{name: "volume", ev: VolumeChanged{Volume: 55}, want: func(p Playback) Playback { p.Volume = 55; return p }},
		{name: "codec", ev: CodecChanged{Codec: "opus"}, want: func(p Playback) Playback { p.Codec = "opus"; return p }},
		{name: "params", ev: ParamsChanged{Params: domain.AudioParams{Format: "floatp", SampleRate: 48000, Channels: "stereo"}}, want: func(p Playback) Playback {
			p.Params = domain.AudioParams{Format: "floatp", SampleRate: 48000, Channels: "stereo"}
			return p
		}},
		{name: "device", ev: DeviceChanged{Device: "pipewire/DX5"}, want: func(p Playback) Playback { p.Device = "pipewire/DX5"; return p }},
		{name: "normalize on", ev: NormalizeChanged{On: true}, want: func(p Playback) Playback { p.Normalize = true; return p }},
		{name: "normalize off", ev: NormalizeChanged{}, from: Playback{Normalize: true}, want: func(p Playback) Playback { p.Normalize = false; return p }},
		{name: "loop playlist", ev: LoopPlaylistChanged{On: true}, want: func(p Playback) Playback { p.LoopPlaylist = true; return p }},
		{name: "loop file", ev: LoopFileChanged{On: true}, want: func(p Playback) Playback { p.LoopFile = true; return p }},
		{name: "idle", ev: IdleChanged{Idle: true}, trackChange: true, want: func(p Playback) Playback {
			p.Idle, p.TimePos, p.Duration = true, 0, 0
			return p
		}},
		{name: "leave idle", ev: IdleChanged{}, from: Playback{Idle: true}, trackChange: true, want: func(p Playback) Playback { p.Idle = false; return p }},
		{name: "queue position", ev: QueuePosChanged{Pos: 3}, trackChange: true, want: func(p Playback) Playback { p.TimePos = 0; return p }},
		{name: "queue contents", ev: QueueChanged{}, want: func(p Playback) Playback { return p }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := playing
			if tt.from != (Playback{}) {
				from = tt.from
			}
			got := from
			if changed := got.Apply(tt.ev); changed != tt.trackChange {
				t.Errorf("apply reported a track change = %v, want %v", changed, tt.trackChange)
			}
			if want := tt.want(from); got != want {
				t.Errorf("state = %+v, want %+v", got, want)
			}
		})
	}
}

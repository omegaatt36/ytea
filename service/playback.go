package service

import (
	"time"

	"github.com/omegaatt36/ytea/domain"
)

type Playback struct {
	TimePos, Duration time.Duration
	Paused, Idle      bool
	Volume            float64
	Codec             string
	Params            domain.AudioParams
	Device            string
	Normalize         bool
	LoopPlaylist      bool
	LoopFile          bool
}

func (p *Playback) Apply(ev PlayerEvent) bool {
	switch ev := ev.(type) {
	case PositionChanged:
		p.TimePos = ev.Pos
	case DurationChanged:
		p.Duration = ev.Duration
	case PauseChanged:
		p.Paused = ev.Paused
	case IdleChanged:
		if ev.Idle {
			p.Stop()
		} else {
			p.Idle = false
		}
		return true
	case VolumeChanged:
		p.Volume = ev.Volume
	case CodecChanged:
		p.Codec = ev.Codec
	case ParamsChanged:
		p.Params = ev.Params
	case DeviceChanged:
		p.Device = ev.Device
	case NormalizeChanged:
		p.Normalize = ev.On
	case LoopPlaylistChanged:
		p.LoopPlaylist = ev.On
	case LoopFileChanged:
		p.LoopFile = ev.On
	case QueuePosChanged:
		p.TimePos = 0
		return true
	}
	return false
}

func (p *Playback) Stop() {
	p.Idle = true
	p.TimePos, p.Duration = 0, 0
}

func (p Playback) Repeat() domain.Repeat {
	return domain.RepeatFrom(p.LoopPlaylist, p.LoopFile)
}

func (p Playback) DeviceName() string {
	if p.Device == "" {
		return "auto"
	}
	return p.Device
}

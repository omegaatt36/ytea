package mpv

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

type playlistEntry struct {
	Filename string `json:"filename"`
	Title    string `json:"title"`
	Current  bool   `json:"current"`
	Playing  bool   `json:"playing"`
}

type audioDevice struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type audioParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"samplerate"`
	Channels   string `json:"channels"`
}

func decodePlaylist(data json.RawMessage) []domain.PlaylistEntry {
	wire := Decode[[]playlistEntry](data)
	if wire == nil {
		return nil
	}
	entries := make([]domain.PlaylistEntry, len(wire))
	for i, e := range wire {
		entries[i] = domain.PlaylistEntry(e)
	}
	return entries
}

func decodeDevices(data json.RawMessage) []domain.AudioDevice {
	wire := Decode[[]audioDevice](data)
	if wire == nil {
		return nil
	}
	devices := make([]domain.AudioDevice, len(wire))
	for i, d := range wire {
		devices[i] = domain.AudioDevice(d)
	}
	return devices
}

func (p *Player) Events() <-chan service.PlayerEvent {
	p.eventsOnce.Do(func() {
		p.events = make(chan service.PlayerEvent)
		p.eventsDone = make(chan struct{})
		go func() {
			defer close(p.eventsDone)
			defer close(p.events)
			for {
				select {
				case <-p.client.stop:
					return
				case ev, ok := <-p.client.Events():
					if !ok {
						return
					}
					if translated, ok := translate(ev); ok {
						select {
						case p.events <- translated:
						case <-p.client.stop:
							return
						}
					}
				}
			}
		}()
	})
	return p.events
}

func translate(ev Event) (service.PlayerEvent, bool) {
	switch ev.Name {
	case "property-change":
		return translateProperty(ev)
	case "playback-restart":
		return service.PlaybackRestarted{}, true
	case "file-loaded":
		return service.FileLoaded{}, true
	case "end-file":
		if ev.Reason == "error" {
			return service.PlaybackFailed{Reason: ev.FileError}, true
		}
	}
	return nil, false
}

func translateProperty(ev Event) (service.PlayerEvent, bool) {
	switch ev.Prop {
	case PropTimePos:
		return service.PositionChanged{Pos: seconds(Decode[float64](ev.Data))}, true
	case PropDuration:
		return service.DurationChanged{Duration: seconds(Decode[float64](ev.Data))}, true
	case PropPause:
		return service.PauseChanged{Paused: Decode[bool](ev.Data)}, true
	case PropIdle:
		return service.IdleChanged{Idle: Decode[bool](ev.Data)}, true
	case PropVolume:
		return service.VolumeChanged{Volume: Decode[float64](ev.Data)}, true
	case PropCodec:
		return service.CodecChanged{Codec: Decode[string](ev.Data)}, true
	case PropAudioParams:
		return service.ParamsChanged{Params: domain.AudioParams(Decode[audioParams](ev.Data))}, true
	case PropAudioDevice:
		return service.DeviceChanged{Device: Decode[string](ev.Data)}, true
	case PropAF:
		return service.NormalizeChanged{On: slices.ContainsFunc(Decode[[]Filter](ev.Data), func(f Filter) bool {
			return f.Label == NormalizeLabel
		})}, true
	case PropLoopPlaylist:
		return service.LoopPlaylistChanged{On: LoopOn(ev.Data)}, true
	case PropLoopFile:
		return service.LoopFileChanged{On: LoopOn(ev.Data)}, true
	case PropPlaylist:
		return service.QueueChanged{Entries: decodePlaylist(ev.Data)}, true
	case PropPlaylistPos:
		// null (unavailable) would decode to 0 and wrongly mark the first entry as playing.
		pos := -1
		if string(ev.Data) != "null" {
			pos = Decode[int](ev.Data)
		}
		return service.QueuePosChanged{Pos: pos}, true
	}
	return nil, false
}

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

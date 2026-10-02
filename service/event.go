package service

import (
	"time"

	"github.com/omegaatt36/ytea/domain"
)

// Player adapters translate their wire events into these, so nothing here depends
// on the player's protocol.
type PlayerEvent interface {
	Msg
	playerEvent()
}

type (
	// PositionChanged carries the playback position; zero while unavailable.
	PositionChanged struct{ Pos time.Duration }
	// DurationChanged carries the current track's length; zero while unknown.
	DurationChanged     struct{ Duration time.Duration }
	PauseChanged        struct{ Paused bool }
	IdleChanged         struct{ Idle bool }
	VolumeChanged       struct{ Volume float64 }
	CodecChanged        struct{ Codec string }
	ParamsChanged       struct{ Params domain.AudioParams }
	DeviceChanged       struct{ Device string }
	NormalizeChanged    struct{ On bool }
	LoopPlaylistChanged struct{ On bool }
	LoopFileChanged     struct{ On bool }
	QueueChanged        struct{ Entries []domain.PlaylistEntry }
	// QueuePosChanged carries the playing index, -1 when nothing plays.
	QueuePosChanged struct{ Pos int }
	// PlaybackRestarted fires after every seek and track start.
	PlaybackRestarted struct{}
	// FileLoaded fires once a track's media is open.
	FileLoaded     struct{}
	PlaybackFailed struct{ Reason string }
)

func (PositionChanged) playerEvent()     {}
func (DurationChanged) playerEvent()     {}
func (PauseChanged) playerEvent()        {}
func (IdleChanged) playerEvent()         {}
func (VolumeChanged) playerEvent()       {}
func (CodecChanged) playerEvent()        {}
func (ParamsChanged) playerEvent()       {}
func (DeviceChanged) playerEvent()       {}
func (NormalizeChanged) playerEvent()    {}
func (LoopPlaylistChanged) playerEvent() {}
func (LoopFileChanged) playerEvent()     {}
func (QueueChanged) playerEvent()        {}
func (QueuePosChanged) playerEvent()     {}
func (PlaybackRestarted) playerEvent()   {}
func (FileLoaded) playerEvent()          {}
func (PlaybackFailed) playerEvent()      {}

func (PositionChanged) serviceMsg()     {}
func (DurationChanged) serviceMsg()     {}
func (PauseChanged) serviceMsg()        {}
func (IdleChanged) serviceMsg()         {}
func (VolumeChanged) serviceMsg()       {}
func (CodecChanged) serviceMsg()        {}
func (ParamsChanged) serviceMsg()       {}
func (DeviceChanged) serviceMsg()       {}
func (NormalizeChanged) serviceMsg()    {}
func (LoopPlaylistChanged) serviceMsg() {}
func (LoopFileChanged) serviceMsg()     {}
func (QueueChanged) serviceMsg()        {}
func (QueuePosChanged) serviceMsg()     {}
func (PlaybackRestarted) serviceMsg()   {}
func (FileLoaded) serviceMsg()          {}
func (PlaybackFailed) serviceMsg()      {}

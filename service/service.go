// Package service is the front-end independent core: it mirrors what mpv is
// doing and turns user intents into mpv commands. A front end drives it and
// renders what it reports.
//
// State changes are deterministic. Work that must leave the caller, such as an
// mpv command, is returned as a Cmd; running it yields a Msg that goes back
// into Update.
package service

import (
	"context"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

type Msg interface{ serviceMsg() }

type Cmd func() Msg

type Failed struct{ Err error }

func (Failed) serviceMsg() {}

const (
	cmdTimeout     = 5 * time.Second
	playAllTimeout = 2 * time.Minute
)

type Player interface {
	Events() <-chan PlayerEvent
	AudioDevices(context.Context) ([]domain.AudioDevice, error)
	SetAudioDevice(context.Context, string) error
	StreamInfo(context.Context) (domain.StreamInfo, error)
	TogglePause(context.Context) error
	SetPause(context.Context, bool) error
	SetVolume(context.Context, float64) error
	Seek(context.Context, time.Duration) error
	SeekTo(context.Context, time.Duration) error
	SeekPercent(context.Context, float64) error
	Next(context.Context) error
	Prev(context.Context) error
	AddVolume(context.Context, int) error
	SetNormalize(context.Context, bool) error
	PlayNow(context.Context, string) error
	Append(context.Context, string) error
	AppendAll(context.Context, []string) error
	PlayAll(ctx context.Context, urls []string, start int) error
	PlayIndex(context.Context, int) error
	Remove(context.Context, int) error
	Move(context.Context, int, int) error
	Reorder(ctx context.Context, pos int, current string, order []int) error
	SetRepeat(context.Context, domain.Repeat) error
	Stop(context.Context) error
	Playlist(context.Context) ([]domain.PlaylistEntry, int, error)
}

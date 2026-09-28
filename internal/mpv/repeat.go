package mpv

import (
	"context"
	"encoding/json"
)

// Repeat is the playlist repeat mode, carried by mpv's loop-playlist and loop-file.
// The zero value behaves as RepeatOff.
type Repeat string

const (
	RepeatOff Repeat = "off"
	RepeatAll Repeat = "all"
	RepeatOne Repeat = "one"
)

func (r Repeat) String() string {
	return string(r)
}

// ParseRepeat is the inverse of String; unknown values are RepeatOff.
func ParseRepeat(s string) Repeat {
	switch r := Repeat(s); r {
	case RepeatOff, RepeatAll, RepeatOne:
		return r
	}
	return RepeatOff
}

// Next is the mode after r in the cycle off → all → one → off.
func (r Repeat) Next() Repeat {
	switch r {
	case RepeatAll:
		return RepeatOne
	case RepeatOne:
		return RepeatOff
	case RepeatOff:
	}
	return RepeatAll
}

// SetRepeat sets loop-file and loop-playlist for r. Mode one turns
// loop-playlist off so leaving it never falls back to all.
func (p *Player) SetRepeat(ctx context.Context, r Repeat) error {
	loopFile, loopPlaylist := "no", "no"
	switch r {
	case RepeatAll:
		loopPlaylist = "inf"
	case RepeatOne:
		loopFile = "inf"
	case RepeatOff:
	}
	// loop-file first: all → one passes through all+one, which reads as one, never off.
	if _, err := p.client.Command(ctx, "set_property", PropLoopFile, loopFile); err != nil {
		return err
	}
	_, err := p.client.Command(ctx, "set_property", PropLoopPlaylist, loopPlaylist)
	return err
}

// LoopOn reports whether a loop-playlist or loop-file value repeats at all.
// mpv reports false for no, "inf" for inf and yes, "force", or a count.
func LoopOn(data json.RawMessage) bool {
	switch v := Decode[any](data).(type) {
	case bool:
		return v
	case string:
		return v != "no"
	case float64:
		return v > 0
	default:
		return false
	}
}

// RepeatFrom derives the mode from whether loop-playlist and loop-file are on.
// loop-file wins because it keeps mpv on the current track.
func RepeatFrom(loopPlaylist, loopFile bool) Repeat {
	switch {
	case loopFile:
		return RepeatOne
	case loopPlaylist:
		return RepeatAll
	default:
		return RepeatOff
	}
}

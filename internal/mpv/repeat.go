package mpv

import (
	"context"
	"encoding/json"
)

// Repeat is the playlist repeat mode, carried by mpv's loop-playlist and loop-file.
type Repeat int

const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
)

func (r Repeat) String() string {
	switch r {
	case RepeatAll:
		return "all"
	case RepeatOne:
		return "one"
	default:
		return "off"
	}
}

// ParseRepeat is the inverse of String; unknown values are RepeatOff.
func ParseRepeat(s string) Repeat {
	switch s {
	case "all":
		return RepeatAll
	case "one":
		return RepeatOne
	default:
		return RepeatOff
	}
}

// Next is the mode after r in the cycle off → all → one → off.
func (r Repeat) Next() Repeat {
	return (r + 1) % 3
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

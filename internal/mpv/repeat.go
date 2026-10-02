package mpv

import (
	"context"
	"encoding/json"

	"github.com/omegaatt36/ytea/domain"
)

// SetRepeat sets loop-file and loop-playlist for r. Mode one turns
// loop-playlist off so leaving it never falls back to all.
func (p *Player) SetRepeat(ctx context.Context, r domain.Repeat) error {
	loopFile, loopPlaylist := "no", "no"
	switch r {
	case domain.RepeatAll:
		loopPlaylist = "inf"
	case domain.RepeatOne:
		loopFile = "inf"
	case domain.RepeatOff:
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

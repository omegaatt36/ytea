package mpv

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Reorder rearranges the playlist into order with playlist-move. Each move
// pulls an entry up to its final index from further down, so entries already
// in place, such as the current one ahead of a tail shuffle, are never moved.
// order was computed with current playing at pos (-1 and "" for nothing);
// if mpv has advanced or the playlist changed since, nothing is moved.
func (p *Player) Reorder(ctx context.Context, pos int, current string, order []int) error {
	seen := make([]bool, len(order))
	for _, v := range order {
		if v < 0 || v >= len(order) || seen[v] {
			return fmt.Errorf("reorder %v: not a permutation", order)
		}
		seen[v] = true
	}
	entries, playing, err := p.Playlist(ctx)
	if err != nil {
		return fmt.Errorf("reorder: %w", err)
	}
	if len(entries) != len(order) || playing != pos || pos >= 0 && entries[pos].Filename != current {
		return errors.New("reorder: queue changed since the shuffle was computed")
	}
	at := make([]int, len(order))
	for i := range at {
		at[i] = i
	}
	for i, want := range order {
		j := slices.Index(at[i:], want) + i
		if j == i {
			continue
		}
		if _, err := p.client.Command(ctx, "playlist-move", j, i); err != nil {
			return err
		}
		at = slices.Insert(slices.Delete(at, j, j+1), i, want)
	}
	return nil
}

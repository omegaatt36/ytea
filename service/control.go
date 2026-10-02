package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

var (
	// ErrEditsPending says the queue holds edits mpv has not confirmed, so an
	// operation that depends on the current index must wait.
	ErrEditsPending = errors.New("queue edits pending")
	// ErrNothingToShuffle says fewer than two tracks follow the current one.
	ErrNothingToShuffle = errors.New("nothing to shuffle")
)

func (c Core) command(fn func(context.Context, Player) error) Cmd {
	p := c.deps.Player
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		if err := fn(ctx, p); err != nil {
			return Failed{err}
		}
		return nil
	}
}

func (c Core) TogglePause() Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.TogglePause(ctx) })
}

func (c Core) Seek(offset time.Duration) Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.Seek(ctx, offset) })
}

func (c Core) SeekTo(pos time.Duration) Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.SeekTo(ctx, pos) })
}

func (c Core) SeekPercent(percent float64) Cmd {
	if !c.Seekable() {
		return nil
	}
	return c.command(func(ctx context.Context, p Player) error { return p.SeekPercent(ctx, percent) })
}

func (c Core) Next() Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.Next(ctx) })
}

func (c Core) Prev() Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.Prev(ctx) })
}

func (c Core) AddVolume(delta int) Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.AddVolume(ctx, delta) })
}

func (c Core) ToggleNormalize() Cmd {
	on := !c.Playback.Normalize
	return c.command(func(ctx context.Context, p Player) error { return p.SetNormalize(ctx, on) })
}

func (c Core) CycleRepeat() Cmd {
	next := c.Playback.Repeat().Next()
	return c.command(func(ctx context.Context, p Player) error { return p.SetRepeat(ctx, next) })
}

// SetAudioDevice routes mpv itself, rather than the system mixer, so the
// choice sticks across tracks and survives stream re-creation.
func (c Core) SetAudioDevice(name string) Cmd {
	return c.command(func(ctx context.Context, p Player) error { return p.SetAudioDevice(ctx, name) })
}

func (c Core) Seekable() bool {
	_, t, ok := c.Current()
	return ok && !t.Live && c.Playback.Duration > 0
}

func (c *Core) PlayIndex(i int) Cmd {
	if c.Queue.InsertPending || i < 0 || i >= len(c.Queue.Entries) {
		return nil
	}
	return c.Queue.PlayIndex(c.NextRequest(), i)
}

func (c *Core) RemoveEntry(i int) Cmd {
	if c.Queue.InsertPending || i < 0 || i >= len(c.Queue.Entries) {
		return nil
	}
	return c.Queue.Remove(c.NextRequest(), i)
}

// Imports still resolving take a slot once resolved, so they land after the clear.
func (c *Core) ClearQueue() Cmd {
	cmd := c.Queue.Clear(c.NextRequest())
	c.Playback.Stop()
	return cmd
}

// MoveEntry walks the entry one slot at a time, since the projected queue only swaps neighbours.
func (c *Core) MoveEntry(from, to int) []Cmd {
	n := len(c.Queue.Entries)
	if c.Queue.InsertPending || from == to || from < 0 || from >= n || to < 0 || to >= n {
		return nil
	}
	step := 1
	if to < from {
		step = -1
	}
	var cmds []Cmd
	for i := from; i != to; i += step {
		cmds = append(cmds, c.Queue.Move(c.NextRequest(), i, i+step))
	}
	return cmds
}

// order[i] is the entry now at i, so a cursor can follow its track.
func (c *Core) Shuffle(rng *rand.Rand) (cmd Cmd, order []int, err error) {
	// The current index is only trustworthy once earlier edits are read back from mpv.
	if c.Queue.Projected() {
		return nil, nil, ErrEditsPending
	}
	n := len(c.Queue.Entries)
	after, current := -1, ""
	if e, _, ok := c.Current(); ok {
		after, current = c.Queue.Pos, e.Filename
	}
	if n-after-1 < 2 {
		return nil, nil, ErrNothingToShuffle
	}
	order = tailShuffle(n, after, rng)
	return c.Queue.Reorder(c.NextRequest(), after, current, order), order, nil
}

// order[i] is the current index of the entry that should end up at i.
func tailShuffle(n, after int, r *rand.Rand) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	tail := order[min(max(after+1, 0), n):]
	r.Shuffle(len(tail), func(i, j int) { tail[i], tail[j] = tail[j], tail[i] })
	return order
}

func (c Core) QueueTracks() []domain.Track {
	tracks := make([]domain.Track, 0, len(c.Queue.Entries))
	seen := make(map[string]bool, len(c.Queue.Entries))
	for _, e := range c.Queue.Entries {
		if e.Filename == "" || seen[e.Filename] {
			continue
		}
		seen[e.Filename] = true
		tracks = append(tracks, c.EntryTrack(e))
	}
	return tracks
}

// ParseSeekTarget reads "50%", "90" (seconds), "1:23" or "1:02:03" as a
// position within a track of length total.
func ParseSeekTarget(s string, total time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if pct, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(strings.TrimSpace(pct), 64)
		if err != nil || v < 0 || v > 100 {
			return 0, fmt.Errorf("invalid percentage %q", s)
		}
		return time.Duration(float64(total) * v / 100), nil
	}
	parts := strings.Split(s, ":")
	if s == "" || len(parts) > 3 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	var secs float64
	for i, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		// Only the leading field may exceed its unit: "90" and "75:00" are fine, "1:75" is not.
		if err != nil || v < 0 || (i > 0 && v >= 60) {
			return 0, fmt.Errorf("invalid time %q", s)
		}
		secs = secs*60 + v
	}
	pos := time.Duration(secs * float64(time.Second))
	if pos > total {
		return 0, errors.New("past the end of " + FormatDuration(total))
	}
	return pos, nil
}

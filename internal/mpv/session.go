package mpv

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PlaybackState contains a playlist snapshot and the settings restored on startup.
type PlaybackState struct {
	URLs    []string
	Entries []PlaylistEntry
	Index   int
	Volume  float64
}

// Snapshot reads the authoritative playlist and playback settings from mpv.
func (p *Player) Snapshot(ctx context.Context) (PlaybackState, error) {
	entries, pos, err := p.Playlist(ctx)
	if err != nil {
		return PlaybackState{}, err
	}
	state := PlaybackState{URLs: make([]string, 0, len(entries)), Entries: entries, Index: pos}
	for _, entry := range entries {
		state.URLs = append(state.URLs, entry.Filename)
	}

	data, err := p.client.Command(ctx, "get_property", PropVolume)
	if err != nil {
		return PlaybackState{}, fmt.Errorf("read volume: %w", err)
	}
	state.Volume = Decode[float64](data)
	return state, nil
}

// Restore loads a playlist paused, selecting the previous track when present.
func (p *Player) Restore(ctx context.Context, state PlaybackState) (retErr error) {
	// The UI is not consuming events yet. A long restored playlist can otherwise
	// fill Client.events and block the socket reader before it reads replies.
	stopDrain := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-stopDrain:
				return
			default:
			}
			select {
			case <-stopDrain:
				return
			case _, ok := <-p.client.Events():
				if !ok {
					return
				}
			}
		}
	}()
	defer func() {
		close(stopDrain)
		<-drained
		// Re-observing sends fresh property values to initialize the TUI after
		// the restore events were intentionally discarded.
		refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		retErr = errors.Join(retErr, p.reobserve(refreshCtx))
	}()

	if err := p.SetVolume(ctx, state.Volume); err != nil {
		return fmt.Errorf("restore volume: %w", err)
	}
	if len(state.URLs) == 0 {
		return nil
	}
	if err := p.SetPause(ctx, true); err != nil {
		return fmt.Errorf("pause before restore: %w", err)
	}
	for _, url := range state.URLs {
		if _, err := p.client.Command(ctx, "loadfile", url, "append"); err != nil {
			return fmt.Errorf("restore track %q: %w", url, err)
		}
	}
	if state.Index >= 0 && state.Index < len(state.URLs) {
		if _, err := p.client.Command(ctx, "playlist-play-index", state.Index); err != nil {
			return fmt.Errorf("restore current track: %w", err)
		}
	}
	return nil
}

func (p *Player) reobserve(ctx context.Context) error {
	for i, prop := range observed {
		if _, err := p.client.Command(ctx, "unobserve_property", i+1); err != nil {
			return fmt.Errorf("unobserve %s after restore: %w", prop, err)
		}
		if _, err := p.client.Command(ctx, "observe_property", i+1, prop); err != nil {
			return fmt.Errorf("observe %s after restore: %w", prop, err)
		}
	}
	return nil
}

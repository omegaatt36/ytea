package service

import (
	"fmt"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

type HistoryStore interface {
	Entries() []domain.HistoryEntry
	Record(track domain.Track, at time.Time) error
	Remove(index int) error
}

type History struct {
	store   HistoryStore
	Entries []domain.HistoryEntry
	// last is the queue entry last recorded, so one play is recorded once.
	last string
}

type HistoryRecorded struct{}

func (HistoryRecorded) serviceMsg() {}

func (h History) Enabled() bool { return h.store != nil }

func (h *History) Reload() {
	if h.store != nil {
		h.Entries = h.store.Entries()
	}
}

func (h *History) Remove(index int) error {
	if h.store == nil {
		return nil
	}
	if err := h.store.Remove(index); err != nil {
		return err
	}
	h.Reload()
	return nil
}

// Records only once the track is audibly playing, so a session restored paused or
// a track that fails to load is not counted as played.
func (c *Core) RecordPlay() Cmd {
	store := c.History.store
	e, _, ok := c.Current()
	if store == nil || !ok || c.Playback.Paused || c.Playback.TimePos <= 0 || e.Filename == c.History.last {
		return nil
	}
	c.History.last = e.Filename
	t := c.EntryTrack(e)
	if t.Duration == 0 && !t.Live {
		t.Duration = c.Playback.Duration
	}
	return func() Msg {
		if err := store.Record(t, time.Now()); err != nil {
			return Failed{fmt.Errorf("record history: %w", err)}
		}
		return HistoryRecorded{}
	}
}

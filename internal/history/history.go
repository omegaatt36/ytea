// Package history remembers recently played tracks across sessions.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/trackfile"
)

const (
	filename   = "history.json"
	version    = 1
	maxBytes   = 2 << 20
	MaxEntries = 200
)

var ErrNotFound = errors.New("history entry not found")

type file struct {
	Version int         `json:"version"`
	Entries []fileEntry `json:"entries"`
}

type fileEntry struct {
	Track    trackfile.Track `json:"track"`
	PlayedAt time.Time       `json:"played_at"`
}

// Store owns the history file, newest play first. Mutations are persisted
// before they return.
type Store struct {
	mu      sync.Mutex
	dir     string
	entries []domain.HistoryEntry
}

// Open loads the history. A missing file starts an empty history.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	f, err := os.Open(filepath.Join(dir, filename))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open history: %w", err)
	}
	defer f.Close()
	var data file
	dec := json.NewDecoder(io.LimitReader(f, maxBytes+1))
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode history: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("history has trailing data or exceeds %d bytes", maxBytes)
	}
	if data.Version != version {
		return nil, fmt.Errorf("unsupported history version %d", data.Version)
	}
	for _, e := range data.Entries {
		if e.Track.URL != "" {
			s.entries = append(s.entries, domain.HistoryEntry{Track: e.Track.To(), PlayedAt: e.PlayedAt})
		}
	}
	if len(s.entries) > MaxEntries {
		s.entries = s.entries[:MaxEntries]
	}
	return s, nil
}

// Entries returns a copy, newest play first.
func (s *Store) Entries() []domain.HistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.entries)
}

// Record moves track to the top, replacing an earlier play of the same URL.
func (s *Store) Record(track domain.Track, at time.Time) error {
	if track.URL == "" {
		return errors.New("track URL is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]domain.HistoryEntry, 0, min(len(s.entries)+1, MaxEntries))
	next = append(next, domain.HistoryEntry{Track: track, PlayedAt: at})
	for _, e := range s.entries {
		if len(next) == MaxEntries {
			break
		}
		if e.Track.URL != track.URL {
			next = append(next, e)
		}
	}
	return s.commit(next)
}

// Remove forgets the entry at index.
func (s *Store) Remove(index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.entries) {
		return ErrNotFound
	}
	return s.commit(slices.Delete(slices.Clone(s.entries), index, index+1))
}

func (s *Store) commit(entries []domain.HistoryEntry) error {
	out := file{Version: version, Entries: make([]fileEntry, len(entries))}
	for i, e := range entries {
		out.Entries[i] = fileEntry{Track: trackfile.From(e.Track), PlayedAt: e.PlayedAt}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxBytes {
		return fmt.Errorf("history exceeds %d bytes", maxBytes)
	}
	f, err := os.CreateTemp(s.dir, ".history-*")
	if err != nil {
		return fmt.Errorf("create history file: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("set history permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write history: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync history: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close history: %w", err)
	}
	if err := os.Rename(f.Name(), filepath.Join(s.dir, filename)); err != nil {
		return fmt.Errorf("replace history: %w", err)
	}
	s.entries = entries
	return nil
}

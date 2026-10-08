// Package playlistignore stores exact YouTube playlist titles in configuration.
package playlistignore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

const (
	filename = "youtube-ignored-playlists.json"
	maxBytes = 8 << 20
)

// Store persists ignore additions before publishing them to readers.
type Store struct {
	mu    sync.Mutex
	dir   string
	names []string
}

// Open loads configuration, migrating legacy titles only when the file is missing.
func Open(dir string, legacy []string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("playlist ignore configuration directory is empty")
	}
	s := &Store{dir: dir, names: []string{}}
	f, err := os.Open(filepath.Join(dir, filename))
	if errors.Is(err, os.ErrNotExist) {
		if len(legacy) != 0 {
			if err := s.persist(slices.Clone(legacy)); err != nil {
				return nil, err
			}
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open ignored playlists: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read ignored playlists: %w", err)
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("ignored playlists exceed %d bytes", maxBytes)
	}
	// Pointers distinguish JSON null entries from exact empty titles.
	var names []*string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("decode ignored playlists: %w", err)
	}
	if names == nil {
		return nil, errors.New("ignored playlists must be a JSON array")
	}
	for _, name := range names {
		if name == nil {
			return nil, errors.New("ignored playlist titles must be strings")
		}
		s.names = append(s.names, *name)
	}
	return s, nil
}

// IgnoredYouTubePlaylists returns a copy of the exact titles to hide.
func (s *Store) IgnoredYouTubePlaylists() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.names)
}

// IgnoreYouTubePlaylist atomically persists a title, unless already present.
func (s *Store) IgnoreYouTubePlaylist(title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.names, title) {
		return nil
	}
	return s.persist(append(slices.Clone(s.names), title))
}

func (s *Store) persist(next []string) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ignored playlists: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxBytes {
		return fmt.Errorf("ignored playlists exceed %d bytes", maxBytes)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create ignored playlists directory: %w", err)
	}
	f, err := os.CreateTemp(s.dir, ".youtube-ignored-playlists-*")
	if err != nil {
		return fmt.Errorf("create ignored playlists file: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("set ignored playlists permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write ignored playlists: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync ignored playlists: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close ignored playlists: %w", err)
	}
	if err := os.Rename(f.Name(), filepath.Join(s.dir, filename)); err != nil {
		return fmt.Errorf("replace ignored playlists: %w", err)
	}
	s.names = next
	return nil
}

// Package library stores named, local playlists independently of the last session.
package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/omegaatt36/ytea/internal/youtube"
)

const (
	filename     = "playlists.json"
	version      = 1
	maxBytes     = 8 << 20
	maxPlaylists = 100
	maxTracks    = 5000
)

var (
	ErrDuplicate = errors.New("already exists")
	ErrNotFound  = errors.New("playlist or track not found")
)

// Playlist is a named, ordered collection of YouTube tracks.
type Playlist struct {
	Name   string          `json:"name"`
	Tracks []youtube.Track `json:"tracks"`
}

type state struct {
	Version   int        `json:"version"`
	Playlists []Playlist `json:"playlists"`
}

// Store owns the local playlist file. Mutations are persisted before they return.
type Store struct {
	mu   sync.Mutex
	dir  string
	data state
}

// Open loads a library. A missing file starts with an empty library.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, data: state{Version: version}}
	f, err := os.Open(filepath.Join(dir, filename))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open playlists: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat playlists: %w", err)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("playlists exceed %d bytes", maxBytes)
	}
	dec := json.NewDecoder(io.LimitReader(f, maxBytes+1))
	if err := dec.Decode(&s.data); err != nil {
		return nil, fmt.Errorf("decode playlists: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("playlists have trailing data or exceed %d bytes", maxBytes)
	}
	if err := validate(s.data); err != nil {
		return nil, err
	}
	return s, nil
}

// Playlists returns a copy safe for the UI to inspect or edit locally.
func (s *Store) Playlists() []Playlist {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.data.Playlists)
}

// Create adds an empty playlist and returns its index.
func (s *Store) Create(name string) (int, error) {
	return s.CreateWithTracks(name, nil)
}

// CreateWithTracks saves a new playlist and its initial tracks in one write.
func (s *Store) CreateWithTracks(name string, tracks []youtube.Track) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return -1, errors.New("playlist name is empty")
	}
	if utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "\r\n") {
		return -1, errors.New("playlist name is too long or contains a line break")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.data.Playlists {
		if strings.EqualFold(p.Name, name) {
			return -1, ErrDuplicate
		}
	}
	if len(s.data.Playlists) >= maxPlaylists {
		return -1, fmt.Errorf("limit of %d playlists reached", maxPlaylists)
	}
	index := len(s.data.Playlists)
	next := clone(s.data.Playlists)
	next = append(next, Playlist{Name: name, Tracks: append([]youtube.Track(nil), tracks...)})
	if err := s.commit(next); err != nil {
		return -1, err
	}
	return index, nil
}

// Add saves a track in the selected playlist, keeping its existing order.
func (s *Store) Add(index int, track youtube.Track) error {
	if track.URL == "" {
		return errors.New("track URL is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.data.Playlists) {
		return ErrNotFound
	}
	for _, existing := range s.data.Playlists[index].Tracks {
		if existing.URL == track.URL {
			return ErrDuplicate
		}
	}
	next := clone(s.data.Playlists)
	next[index].Tracks = append(next[index].Tracks, track)
	return s.commit(next)
}

// RemoveTrack removes one track without affecting the playback queue.
func (s *Store) RemoveTrack(playlistIndex, trackIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if playlistIndex < 0 || playlistIndex >= len(s.data.Playlists) || trackIndex < 0 || trackIndex >= len(s.data.Playlists[playlistIndex].Tracks) {
		return ErrNotFound
	}
	next := clone(s.data.Playlists)
	next[playlistIndex].Tracks = append(next[playlistIndex].Tracks[:trackIndex], next[playlistIndex].Tracks[trackIndex+1:]...)
	return s.commit(next)
}

// Delete removes a named playlist without affecting the playback queue.
func (s *Store) Delete(index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.data.Playlists) {
		return ErrNotFound
	}
	next := clone(s.data.Playlists)
	next = append(next[:index], next[index+1:]...)
	return s.commit(next)
}

func (s *Store) commit(playlists []Playlist) error {
	next := state{Version: version, Playlists: playlists}
	if err := validate(next); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode playlists: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxBytes {
		return fmt.Errorf("playlists exceed %d bytes", maxBytes)
	}
	f, err := os.CreateTemp(s.dir, ".playlists-*")
	if err != nil {
		return fmt.Errorf("create playlists file: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("set playlists permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write playlists: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync playlists: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close playlists: %w", err)
	}
	if err := os.Rename(f.Name(), filepath.Join(s.dir, filename)); err != nil {
		return fmt.Errorf("replace playlists: %w", err)
	}
	s.data = next
	return nil
}

func clone(playlists []Playlist) []Playlist {
	copyOf := make([]Playlist, len(playlists))
	for i, p := range playlists {
		copyOf[i] = Playlist{Name: p.Name, Tracks: append([]youtube.Track(nil), p.Tracks...)}
	}
	return copyOf
}

func validate(data state) error {
	if data.Version != version {
		return fmt.Errorf("unsupported playlists version %d", data.Version)
	}
	if len(data.Playlists) > maxPlaylists {
		return fmt.Errorf("more than %d playlists", maxPlaylists)
	}
	seen := make(map[string]bool, len(data.Playlists))
	total := 0
	for _, p := range data.Playlists {
		if p.Name == "" || utf8.RuneCountInString(p.Name) > 100 || strings.TrimSpace(p.Name) != p.Name || strings.ContainsAny(p.Name, "\r\n") {
			return errors.New("invalid playlist name")
		}
		name := strings.ToLower(p.Name)
		if seen[name] {
			return ErrDuplicate
		}
		seen[name] = true
		urls := make(map[string]bool, len(p.Tracks))
		for _, t := range p.Tracks {
			if t.URL == "" || urls[t.URL] {
				return errors.New("playlist has an empty or duplicate track URL")
			}
			urls[t.URL] = true
			total++
		}
	}
	if total > maxTracks {
		return fmt.Errorf("more than %d saved tracks", maxTracks)
	}
	return nil
}

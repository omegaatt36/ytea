// Package session stores the last local playback session.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

const (
	filename   = "session.json"
	version    = 1
	maxBytes   = 4 << 20
	maxEntries = 5000
)

// State is the playback state needed to restore a session.
type State struct {
	Version  int                 `json:"version"`
	URLs     []string            `json:"urls"`
	Index    int                 `json:"index"`
	Volume   float64             `json:"volume"`
	Metadata map[string]Metadata `json:"metadata,omitempty"`
}

// Metadata is the search information mpv does not retain for unplayed tracks.
type Metadata struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Channel string `json:"channel,omitempty"`
	Live    bool   `json:"live,omitempty"`
}

// Load reads a prior session. A missing file returns a zero State.
func Load(dir string) (State, error) {
	f, err := os.Open(filepath.Join(dir, filename))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return State{}, fmt.Errorf("stat session: %w", err)
	}
	if info.Size() > maxBytes {
		return State{}, fmt.Errorf("session exceeds %d bytes", maxBytes)
	}

	var state State
	dec := json.NewDecoder(io.LimitReader(f, maxBytes+1))
	if err := dec.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode session: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return State{}, fmt.Errorf("session has trailing data or exceeds %d bytes", maxBytes)
	}
	if err := state.validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// Save atomically replaces the last session with state.
func Save(dir string, state State) error {
	state.Version = version
	if err := state.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxBytes {
		return fmt.Errorf("session exceeds %d bytes", maxBytes)
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return fmt.Errorf("create session file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write session: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync session: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, filename)); err != nil {
		return fmt.Errorf("replace session: %w", err)
	}
	return nil
}

func (s State) validate() error {
	if s.Version != version {
		return fmt.Errorf("unsupported session version %d", s.Version)
	}
	if len(s.URLs) > maxEntries {
		return fmt.Errorf("session has more than %d tracks", maxEntries)
	}
	if s.Index < -1 || s.Index >= len(s.URLs) {
		return fmt.Errorf("invalid session track index %d", s.Index)
	}
	if math.IsNaN(s.Volume) || math.IsInf(s.Volume, 0) || s.Volume < 0 {
		return fmt.Errorf("invalid session volume %v", s.Volume)
	}
	for _, url := range s.URLs {
		if url == "" {
			return errors.New("session has an empty track URL")
		}
	}
	if len(s.Metadata) > len(s.URLs) {
		return errors.New("session has metadata for more tracks than URLs")
	}
	urls := make(map[string]bool, len(s.URLs))
	for _, url := range s.URLs {
		urls[url] = true
	}
	for url := range s.Metadata {
		if !urls[url] {
			return fmt.Errorf("session has metadata for unknown URL %q", url)
		}
	}
	return nil
}

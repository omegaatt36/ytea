package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestStorePersistsPlaylistsAndTracks(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	index, err := store.Create("  Evening  ")
	if err != nil || index != 0 {
		t.Fatalf("Create() = %d, %v", index, err)
	}
	track := youtube.Track{ID: "abc", Title: "Song", Channel: "Artist", URL: "https://www.youtube.com/watch?v=abc"}
	if err := store.Add(index, track); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(index, track); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate Add() = %v", err)
	}
	if _, err := store.Create("evening"); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate Create() = %v", err)
	}
	loaded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	playlists := loaded.Playlists()
	if len(playlists) != 1 || playlists[0].Name != "Evening" || len(playlists[0].Tracks) != 1 || playlists[0].Tracks[0] != track {
		t.Fatalf("reloaded playlists = %+v", playlists)
	}
	playlists[0].Tracks[0].Title = "changed"
	if got := loaded.Playlists()[0].Tracks[0].Title; got != "Song" {
		t.Errorf("mutated snapshot changed store: %q", got)
	}
	info, err := os.Stat(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("playlists file mode = %o", info.Mode().Perm())
	}
	if err := loaded.RemoveTrack(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Delete(0); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil || len(again.Playlists()) != 0 {
		t.Fatalf("after delete = %+v, %v", again, err)
	}
}

func TestFailedWriteKeepsPreviousState(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, filename), 0o700); err != nil {
		t.Fatal(err)
	}
	store := &Store{dir: dir, data: state{Version: version}}
	if _, err := store.Create("one"); err == nil {
		t.Fatal("Create() succeeded when destination is a directory")
	}
	if len(store.Playlists()) != 0 {
		t.Error("failed write changed in-memory playlists")
	}
}

func TestOpenRejectsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(`{"version":1,"playlists":[{"name":"X","tracks":[]},{"name":"x","tracks":[]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrDuplicate) {
		t.Errorf("Open() = %v, want duplicate error", err)
	}
}

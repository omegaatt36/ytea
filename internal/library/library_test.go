package library

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func TestStoreRenamesAndReorders(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tracks := []youtube.Track{{Title: "a", URL: "ua"}, {Title: "b", URL: "ub"}, {Title: "c", URL: "uc"}}
	for _, name := range []string{"One", "Two", "Three"} {
		if _, err := store.CreateWithTracks(name, tracks); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Rename(0, "two"); !errors.Is(err, ErrDuplicate) {
		t.Errorf("Rename() to another playlist's name = %v, want ErrDuplicate", err)
	}
	if err := store.Rename(0, "ONE"); err != nil {
		t.Errorf("Rename() changing only case = %v", err)
	}
	if err := store.Rename(0, " "); err == nil {
		t.Error("Rename() to blank: error = nil")
	}
	if err := store.Rename(3, "Four"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Rename() out of range = %v, want ErrNotFound", err)
	}
	if err := store.Move(0, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTrack(2, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTrack(2, 0, 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("MoveTrack() past the end = %v, want ErrNotFound", err)
	}

	loaded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names, titles []string
	for _, p := range loaded.Playlists() {
		names = append(names, p.Name)
	}
	for _, tr := range loaded.Playlists()[2].Tracks {
		titles = append(titles, tr.Title)
	}
	if want := []string{"Two", "Three", "ONE"}; !slices.Equal(names, want) {
		t.Errorf("playlists = %q, want %q", names, want)
	}
	if want := []string{"c", "a", "b"}; !slices.Equal(titles, want) {
		t.Errorf("moved playlist tracks = %q, want %q", titles, want)
	}
	if got := loaded.Playlists()[0].Tracks[0].Title; got != "a" {
		t.Errorf("MoveTrack() touched another playlist: first track %q", got)
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

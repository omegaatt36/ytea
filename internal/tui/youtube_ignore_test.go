package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/playlistignore"
)

func TestIgnoreYouTubePlaylistPersistsAndFiltersByExactTitle(t *testing.T) {
	dir := t.TempDir()
	store, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("Local"); err != nil {
		t.Fatal(err)
	}
	source := &spyAccountPlaylistSource{playlists: []domain.AccountPlaylist{
		{ID: "one", Title: "interview"},
		{ID: "two", Title: "interview"},
		{ID: "three", Title: "Interview"},
		{ID: "four", Title: "music"},
	}}
	load := func(store *library.Store) Model {
		ignored, err := playlistignore.Open(filepath.Join(dir, "config"), store.IgnoredYouTubePlaylists())
		if err != nil {
			t.Fatal(err)
		}
		m := New(Deps{Library: store, AccountPlaylists: source, AccountIgnores: ignored})
		m.focus = focusPlaylists
		m.playlistCur = len(m.core.Playlists.List)
		return accountPlaylistComplete(t, m, m.reloadAccount())
	}
	m := load(store)
	m.playlistTrackCur = 9
	m, _ = accountPlaylistKey(t, m, 'I')
	want := source.playlists[2:]
	if !reflect.DeepEqual(m.core.Account.Playlists, want) {
		t.Fatalf("after ignore = %+v, want %+v", m.core.Account.Playlists, want)
	}
	if m.playlistCur != 1 || m.playlistTrackCur != 0 {
		t.Fatalf("cursors = %d, %d", m.playlistCur, m.playlistTrackCur)
	}
	if len(store.IgnoredYouTubePlaylists()) != 0 {
		t.Fatal("shortcut wrote ignore names to local playlist state")
	}
	if _, err := store.Create("Another local"); err != nil {
		t.Fatal(err)
	}
	store, err = library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m = load(store)
	if !reflect.DeepEqual(m.core.Account.Playlists, want) {
		t.Fatalf("after restart = %+v, want %+v", m.core.Account.Playlists, want)
	}
	m.playlistCur = 0
	m, _ = accountPlaylistKey(t, m, 'I')
	if len(m.core.Playlists.List) != 2 {
		t.Fatal("ignore changed local playlists")
	}
}

func TestIgnoreYouTubePlaylistWriteFailureKeepsVisibleList(t *testing.T) {
	dir := t.TempDir()
	store, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, "config")
	ignored, err := playlistignore.Open(configDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store, AccountPlaylists: accountPlaylistFixture(), AccountIgnores: ignored})
	m.focus = focusPlaylists
	m = accountPlaylistComplete(t, m, m.reloadAccount())
	if err := os.WriteFile(configDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ = accountPlaylistKey(t, m, 'I')
	if len(m.core.Account.Playlists) != 1 {
		t.Fatal("failed save hid the playlist")
	}
	if !m.statusErr {
		t.Fatal("failed save did not report an error")
	}
}

func TestIgnoredYouTubePlaylistsLoadedBeforeAccountMapping(t *testing.T) {
	dir := t.TempDir()
	data := `{"version":1,"playlists":[],"youtube-ignored-playlists":["My uploads"]}`
	if err := os.WriteFile(filepath.Join(dir, "playlists.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := playlistignore.Open(filepath.Join(dir, "config"), store.IgnoredYouTubePlaylists())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store, AccountPlaylists: accountPlaylistFixture(), AccountIgnores: ignored})
	m.focus = focusPlaylists
	m = accountPlaylistComplete(t, m, m.reloadAccount())
	if len(m.core.Account.Playlists) != 0 || m.core.Account.Err != nil {
		t.Fatalf("all ignored: playlists = %+v, error = %v", m.core.Account.Playlists, m.core.Account.Err)
	}
	m, _ = accountPlaylistKey(t, m, 'I')
	if m.statusErr {
		t.Fatal("ignore on the empty placeholder should do nothing")
	}
}

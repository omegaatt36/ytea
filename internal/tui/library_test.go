package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestLocalPlaylistFlow(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := youtube.Track{ID: "one", Title: "First", URL: "https://www.youtube.com/watch?v=one"}
	second := youtube.Track{ID: "two", Title: "Second", URL: "https://www.youtube.com/watch?v=two"}
	m := New(Deps{Library: store})
	m.focus = focusResults
	m.results = []youtube.Track{first, second}
	got, _ := m.update(keyPress("s"))
	m = got.(Model)
	if m.overlay != overlayName {
		t.Fatalf("save without lists: overlay=%v, want name input", m.overlay)
	}
	m.nameInput.SetValue("Favorites")
	got, _ = m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	if !atPane(m, focusResults) || len(store.Playlists()) != 1 || len(store.Playlists()[0].Tracks) != 1 {
		t.Fatalf("created playlist and saved song: focus=%v overlay=%v playlists=%+v", m.focus, m.overlay, store.Playlists())
	}
	m.resultCur = 1
	got, _ = m.update(keyPress("s"))
	m = got.(Model)
	if m.overlay != overlayPicker {
		t.Fatalf("save with existing list: overlay=%v, want picker", m.overlay)
	}
	got, _ = m.update(keyPress("enter"))
	m = got.(Model)
	if !atPane(m, focusResults) || len(store.Playlists()[0].Tracks) != 2 {
		t.Fatalf("saved second song: focus=%v overlay=%v playlists=%+v", m.focus, m.overlay, store.Playlists())
	}
	m.cycleTab(false)
	m.cycleTab(false)
	if !atPane(m, focusPlaylists) {
		t.Fatalf("two tabs from results: focus=%v overlay=%v", m.focus, m.overlay)
	}
	got, _ = m.update(keyPress("enter"))
	m = got.(Model)
	if !atPane(m, focusPlaylistTracks) {
		t.Fatalf("enter list: focus=%v overlay=%v", m.focus, m.overlay)
	}
	m.playlistTrackCur = 1
	got, _ = m.update(keyPress("d"))
	m = got.(Model)
	if len(store.Playlists()[0].Tracks) != 1 || store.Playlists()[0].Tracks[0].URL != first.URL {
		t.Fatalf("after removal = %+v", store.Playlists())
	}
}

func TestPlaylistTrackKeyRunsPlayerCommand(t *testing.T) {
	for _, tt := range []struct {
		name       string
		key        rune
		wantCall   string
		wantStatus string
		projected  bool
		err        error
	}{
		{name: "play", key: tea.KeyEnter, wantCall: "play second", wantStatus: "playing", projected: true},
		{name: "append", key: 'a', wantCall: "append second", wantStatus: "queued"},
		{name: "append failure", key: 'a', wantCall: "append second", err: errors.New("mpv rejected append")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := library.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tracks := []youtube.Track{{URL: "first", Title: "First"}, {URL: "second", Title: "Second"}}
			if _, err := store.CreateWithTracks("Favorites", tracks); err != nil {
				t.Fatal(err)
			}
			player := &spyPlayer{err: tt.err}
			m := New(Deps{Library: store, Player: player})
			m.focus = focusPlaylistTracks
			m.playlistTrackCur = 1

			got, cmd := m.update(tea.KeyPressMsg{Code: tt.key})
			m = got.(Model)
			if cmd == nil {
				t.Fatal("playlist key did not schedule a player command")
			}
			if len(player.calls) != 0 {
				t.Fatalf("player was called before the command ran: %v", player.calls)
			}
			if m.tracks["second"].Title != "Second" {
				t.Fatalf("selected track metadata was lost: %+v", m.tracks["second"])
			}

			result := cmd()
			done, ok := result.(queueActionDoneMsg)
			if !ok {
				t.Fatalf("command result has type %T, want queueActionDoneMsg", result)
			}
			if len(player.calls) != 1 || player.calls[0] != tt.wantCall {
				t.Fatalf("player calls = %v, want [%s]", player.calls, tt.wantCall)
			}
			if done.projected != tt.projected || !errors.Is(done.err, tt.err) {
				t.Fatalf("command result = %+v, want projected=%v error=%v", done, tt.projected, tt.err)
			}
			got, _ = m.update(done)
			m = got.(Model)
			if tt.err != nil {
				if !m.statusErr || m.status != tt.err.Error() {
					t.Fatalf("failed command status = %q (error=%v)", m.status, m.statusErr)
				}
			} else if m.statusErr || !strings.Contains(m.status, tt.wantStatus) {
				t.Fatalf("command status = %q (error=%v), want %q", m.status, m.statusErr, tt.wantStatus)
			}
		})
	}
}

func TestSaveQueueAsPlaylist(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusQueue
	firstURL := "https://www.youtube.com/watch?v=one"
	secondURL := "https://www.youtube.com/watch?v=two"
	m.queue.entries = []mpv.PlaylistEntry{
		{Filename: firstURL, Title: "First"},
		{Filename: secondURL, Title: "Second"},
		{Filename: firstURL, Title: "First"},
	}
	got, _ := m.update(keyPress("S"))
	m = got.(Model)
	if m.overlay != overlayName {
		t.Fatalf("save queue: overlay=%v, want name input", m.overlay)
	}
	m.nameInput.SetValue("Imported")
	got, _ = m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	playlists := store.Playlists()
	if !atPane(m, focusPlaylists) || len(playlists) != 1 || len(playlists[0].Tracks) != 2 || playlists[0].Tracks[0].URL != firstURL || playlists[0].Tracks[1].URL != secondURL {
		t.Fatalf("saved queue = %+v, focus=%v overlay=%v", playlists, m.focus, m.overlay)
	}
}

func TestDeletePlaylistNeedsSecondPress(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("Keep"); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusPlaylists
	got, _ := m.update(keyPress("D"))
	m = got.(Model)
	if len(store.Playlists()) != 1 || m.deletePlaylistPending != 0 {
		t.Fatalf("first D deleted playlist: %+v", store.Playlists())
	}
	got, _ = m.update(keyPress("D"))
	m = got.(Model)
	if len(store.Playlists()) != 0 || m.deletePlaylistPending != -1 {
		t.Fatalf("second D did not delete playlist: %+v", store.Playlists())
	}
}

package tui

import (
	"image"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func playlistEditModel(focus focus) (Model, *spyLibrary, *spyPlayer) {
	lib := &spyLibrary{playlists: []library.Playlist{
		{Name: "One", Tracks: []youtube.Track{{Title: "alpha", URL: "ua"}, {Title: "bravo", URL: "ub"}, {Title: "charlie", URL: "uc"}}},
		{Name: "Two"},
	}}
	player := &spyPlayer{}
	m := New(Deps{Library: lib, Player: player})
	m.width, m.height = 100, 40
	m.input.Blur()
	m.focus = focus
	return m, lib, player
}

func playlistNames(ps []library.Playlist) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}

func TestRenamePlaylistPrefillsTheCurrentName(t *testing.T) {
	m, lib, _ := playlistEditModel(focusPlaylists)
	m = press(t, m, keyPress("e"))
	if m.overlay != overlayName || m.nameInput.Value() != "One" {
		t.Fatalf("e: overlay=%v value=%q, want the name dialog holding %q", m.overlay, m.nameInput.Value(), "One")
	}
	if !strings.Contains(rendered(m), "Rename playlist") {
		t.Error("dialog is not titled Rename playlist")
	}
	m.nameInput.SetValue("Evening")
	m = press(t, m, keyPress("enter"))
	if m.overlay != overlayNone || lib.playlists[0].Name != "Evening" || m.playlists[0].Name != "Evening" {
		t.Errorf("after rename: overlay=%v store=%q view=%q", m.overlay, lib.playlists[0].Name, m.playlists[0].Name)
	}
	if m.focus != focusPlaylists {
		t.Errorf("rename moved focus to %v", m.focus)
	}
}

func TestMovePlaylistsAndTracksWithKJ(t *testing.T) {
	m, lib, _ := playlistEditModel(focusPlaylists)
	m = press(t, m, keyPress("J"))
	if got := playlistNames(lib.playlists); !slices.Equal(got, []string{"Two", "One"}) || m.playlistCur != 1 {
		t.Fatalf("J: playlists %q cursor %d", got, m.playlistCur)
	}
	m = press(t, m, keyPress("J"))
	if m.playlistCur != 1 {
		t.Errorf("J on the last playlist moved the cursor to %d", m.playlistCur)
	}

	m = press(t, m, keyPress("enter"))
	m.playlistTrackCur = 2
	m = press(t, m, keyPress("K"))
	if got := titles(lib.playlists[1].Tracks); !slices.Equal(got, []string{"alpha", "charlie", "bravo"}) || m.playlistTrackCur != 1 {
		t.Errorf("K: tracks %q cursor %d", got, m.playlistTrackCur)
	}
}

func TestPlayAllReplacesTheQueue(t *testing.T) {
	m, _, player := playlistEditModel(focusPlaylists)
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "old"}}
	got, cmd := m.update(keyPress("P"))
	m = got.(Model)
	if got := filenames(m.queue.entries); !slices.Equal(got, []string{"ua", "ub", "uc"}) || !m.queue.insertPending {
		t.Fatalf("projected queue %q insertPending=%v", got, m.queue.insertPending)
	}
	runCmd(cmd)
	if !slices.Contains(player.calls, "play all ua,ub,uc from 0") {
		t.Errorf("player calls = %q", player.calls)
	}

	// In the tracks pane, P plays from the selected track on.
	m, _, player = playlistEditModel(focusPlaylistTracks)
	m.playlistTrackCur = 1
	got, cmd = m.update(keyPress("P"))
	m = got.(Model)
	runCmd(cmd)
	if !slices.Contains(player.calls, "play all ua,ub,uc from 1") || m.queueCur != 1 {
		t.Errorf("P from track 1: calls %q, queue cursor %d", player.calls, m.queueCur)
	}
}

func TestPlayAllOnAnEmptyPlaylistSaysSo(t *testing.T) {
	m, _, player := playlistEditModel(focusPlaylists)
	m.playlistCur = 1
	got, cmd := m.update(keyPress("P"))
	m = got.(Model)
	runCmd(cmd)
	if len(player.calls) != 0 || m.status != "playlist is empty" {
		t.Errorf("calls %q status %q", player.calls, m.status)
	}
}

func drag(m Model, from, to image.Point) (Model, tea.Cmd) {
	m = click(m, from)
	next, cmd := m.Update(tea.MouseMotionMsg{X: to.X, Y: to.Y, Button: tea.MouseLeft})
	next, _ = next.(Model).Update(tea.MouseReleaseMsg{X: to.X, Y: to.Y, Button: tea.MouseLeft})
	return next.(Model), cmd
}

func TestDragReordersPlaylistTracks(t *testing.T) {
	m, lib, _ := playlistEditModel(focusPlaylistTracks)
	m, _ = drag(m, cellAt(t, m, "alpha"), cellAt(t, m, "charlie"))
	want := []string{"bravo", "charlie", "alpha"}
	if got := titles(lib.playlists[0].Tracks); !slices.Equal(got, want) || m.playlistTrackCur != 2 {
		t.Errorf("drag alpha onto charlie: tracks %q cursor %d", got, m.playlistTrackCur)
	}
	if m.drag != paneNone {
		t.Error("release left the drag active")
	}
	// Motion after the release is plain hovering.
	at := cellAt(t, m, "bravo")
	next, _ := m.Update(tea.MouseMotionMsg{X: at.X, Y: at.Y, Button: tea.MouseLeft})
	if got := titles(lib.playlists[0].Tracks); !slices.Equal(got, want) || next.(Model).playlistTrackCur != 2 {
		t.Errorf("motion after release moved tracks: %q", got)
	}
}

func TestDragReordersTheQueue(t *testing.T) {
	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.width, m.height = 100, 30
	m.input.Blur()
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A", Title: "first"}, {Filename: "B", Title: "second"}, {Filename: "C", Title: "third"}}
	m, cmd := drag(m, cellAt(t, m, "third"), cellAt(t, m, "first"))
	if got := filenames(m.queue.entries); !slices.Equal(got, []string{"C", "A", "B"}) || m.queueCur != 0 {
		t.Fatalf("drag third to the top: queue %q cursor %d", got, m.queueCur)
	}
	if cmd == nil {
		t.Fatal("drag sent no move to mpv")
	}
}

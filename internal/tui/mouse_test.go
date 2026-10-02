package tui

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/library"
)

func TestMouseEnablesCellMotionOnly(t *testing.T) {
	if got := New(Deps{}).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want cell motion so hovering never renders", got)
	}
}

func TestMouseSuspendedDuringInfoOverlay(t *testing.T) {
	m := New(Deps{})
	m.overlay = overlayInfo
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("MouseMode = %v, want None during overlayInfo", got)
	}
}

func TestClickSelectsRowUnderPointer(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.results.cur = 35

	m = click(m, cellAt(t, m, "song 30"))
	if !atPane(m, focusResults) || m.results.cur != 30 {
		t.Errorf("click song 30: focus=%v resultCur=%d, want results/30", m.focus, m.results.cur)
	}

	m = click(m, cellAt(t, m, "queued 2"))
	if !atPane(m, focusQueue) || m.queueCur != 2 {
		t.Errorf("click queued 2: focus=%v queueCur=%d, want queue/2", m.focus, m.queueCur)
	}

	m = click(m, cellAt(t, m, "queued 2").Add(image.Pt(0, 3)))
	if !atPane(m, focusQueue) || m.queueCur != 2 {
		t.Errorf("click below queue: focus=%v queueCur=%d, want queue/2", m.focus, m.queueCur)
	}
}

func TestClickHeader(t *testing.T) {
	m := mouseModel()
	if !atPane(m, focusSearch) {
		t.Fatalf("new model focus = %v, want search", m.focus)
	}

	m = click(m, cellAt(t, m, "Queue"))
	if !atPane(m, focusQueue) || m.input.Focused() {
		t.Errorf("click Queue tab: focus=%v input focused=%v, want queue and blurred input", m.focus, m.input.Focused())
	}
	m = click(m, cellAt(t, m, "Playlists"))
	if !atPane(m, focusPlaylists) {
		t.Errorf("click Playlists tab: focus=%v", m.focus)
	}
	m = click(m, cellAt(t, m, "Playlists").Sub(image.Pt(1, 0))) // the gap between tabs
	if !atPane(m, focusPlaylists) {
		t.Errorf("click between tabs: focus=%v, want unchanged", m.focus)
	}
	m = click(m, cellAt(t, m, " / "))
	if !atPane(m, focusSearch) || !m.input.Focused() {
		t.Errorf("click search box: focus=%v input focused=%v", m.focus, m.input.Focused())
	}
}

func TestWheelMovesCursorOfPaneUnderPointer(t *testing.T) {
	m := mouseModel()
	at := cellAt(t, m, "queued 0")

	m = wheel(m, at, tea.MouseWheelDown)
	if !atPane(m, focusQueue) || m.queueCur != 1 || m.input.Focused() {
		t.Errorf("wheel down over queue: focus=%v queueCur=%d input focused=%v", m.focus, m.queueCur, m.input.Focused())
	}
	for range 5 {
		m = wheel(m, at, tea.MouseWheelDown)
	}
	if m.queueCur != 2 {
		t.Errorf("wheel past end: queueCur=%d, want 2", m.queueCur)
	}

	m.focus = focusResults
	m = wheel(m, cellAt(t, m, "song 00"), tea.MouseWheelUp)
	if !atPane(m, focusResults) || m.results.cur != 0 {
		t.Errorf("wheel up over results top: focus=%v resultCur=%d", m.focus, m.results.cur)
	}
}

func TestMouseInPlaylistPicker(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := store.CreateWithTracks(name, []domain.Track{{Title: name + " song", URL: "https://www.youtube.com/watch?v=" + name}}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(Deps{Library: store})
	m.width, m.height = 100, 30
	m.focus = focusResults
	m.core.Search.Tracks = []domain.Track{{Title: "pick me", URL: "https://www.youtube.com/watch?v=pick"}}
	got, _ := m.update(keyPress("s"))
	m = got.(Model)

	m = click(m, cellAt(t, m, "Beta"))
	if m.overlay != overlayPicker || m.playlistCur != 1 {
		t.Errorf("click Beta in picker: overlay=%v playlistCur=%d, want picker/1", m.overlay, m.playlistCur)
	}
	m = click(m, cellAt(t, m, "Beta song"))
	if m.overlay != overlayPicker {
		t.Errorf("click tracks pane in picker: overlay=%v, want picker kept", m.overlay)
	}
}

func TestMouseIgnoredWhileNamingPlaylist(t *testing.T) {
	m := mouseModel()
	m.overlay = overlayName
	m.input.Blur()
	m = click(m, image.Pt(1, 0))
	if m.overlay != overlayName {
		t.Errorf("click during name input: overlay=%v, want name input kept", m.overlay)
	}
}

func TestDialogFloatsOverPanesAndTakesClicks(t *testing.T) {
	m := mouseModel()
	m.input.Blur()
	m.focus = focusQueue
	m.core.Devices = []domain.AudioDevice{{Name: "auto", Description: "Autoselect device"}, {Name: "b", Description: "Second output"}}
	m.overlay = overlayDevices
	if got := rendered(m); !strings.Contains(got, "Second output") || !strings.Contains(got, "queued 0") {
		t.Fatalf("device dialog should float over the queue it was opened from:\n%s", got)
	}

	m = click(m, cellAt(t, m, "Second output"))
	if m.overlay != overlayDevices || m.deviceCur != 1 {
		t.Errorf("click device row: overlay=%v deviceCur=%d, want devices/1", m.overlay, m.deviceCur)
	}
	m = click(m, cellAt(t, m, "queued 2"))
	if m.overlay != overlayDevices || m.focus != focusQueue || m.queueCur != 0 {
		t.Errorf("click beside the dialog: overlay=%v focus=%v queueCur=%d, want the panes untouched", m.overlay, m.focus, m.queueCur)
	}
}

// results-filter R5
func TestFilteredClickSelectsShownTrack(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.results.filter.SetValue("3")
	m = click(m, cellAt(t, m, "song 35"))
	if got := selectedTitle(m); !atPane(m, focusResults) || got != "song 35" {
		t.Errorf("click song 35: focus=%v selected=%q, want results/song 35", m.focus, got)
	}
}

// results-filter R5
func TestFilteredWheelBoundedByVisibleRows(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.results.filter.SetValue("3")
	at := cellAt(t, m, "song 30")
	for range 20 {
		m = wheel(m, at, tea.MouseWheelDown)
	}
	if got := selectedTitle(m); m.results.cur != 12 || got != "song 39" {
		t.Errorf("wheel past end: cur=%d selected=%q, want row 12 song 39 (13 rows contain 3)", m.results.cur, got)
	}
}

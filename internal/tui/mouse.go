package tui

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.overlay == overlayName || m.fullHelp {
		return m, nil
	}
	mouse := msg.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.drag = paneNone
		if mouse.Button == tea.MouseLeft {
			m.deletePlaylistPending = -1
			return m.click(mouse.X, mouse.Y)
		}
	case tea.MouseMotionMsg:
		if mouse.Button == tea.MouseLeft && m.drag != paneNone {
			return m.dragTo(mouse.X, mouse.Y)
		}
	case tea.MouseReleaseMsg:
		m.drag = paneNone
	case tea.MouseWheelMsg:
		//exhaustive:ignore // only vertical wheel scrolls; the rest are foreign buttons.
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.deletePlaylistPending = -1
			return m.scroll(mouse.X, mouse.Y, -1), nil
		case tea.MouseWheelDown:
			m.deletePlaylistPending = -1
			return m.scroll(mouse.X, mouse.Y, 1), nil
		}
	}
	return m, nil
}

func (m Model) click(x, y int) (tea.Model, tea.Cmd) {
	if m.overlay != overlayDevices && y < headerRows {
		if x < m.tabsX() {
			return m, m.focusSearch()
		}
		if f, ok := m.tabAt(x); ok {
			m.input.Blur()
			m.results.filter.Blur()
			m.overlay, m.focus = overlayNone, f
		}
		return m, nil
	}
	if !m.dialogOpen() && image.Pt(x, y).In(m.progressBar()) && m.core.Seekable() {
		bar := m.progressBar()
		// The cell's middle, so the first and last cells reach neither end exactly.
		frac := (float64(x-bar.Min.X) + 0.5) / float64(bar.Dx())
		return m, m.seekTo(time.Duration(frac * float64(m.core.Playback.Duration)))
	}
	p, i := m.paneAt(x, y)
	if p == panePlaylistTracks && m.focus == focusPlaylists && m.accountSelected() {
		return m.handleAccountAction("enter")
	}
	if m.focusPane(p) && i >= 0 {
		m.selectRow(p, i)
		if m.draggable(p, i) {
			m.drag = p
		}
	}
	return m, nil
}

// draggable reports whether row i of p can be reordered by dragging.
func (m Model) draggable(p listPane, i int) bool {
	if m.overlay != overlayNone {
		return false
	}
	switch p {
	case paneQueue:
		return !m.core.Queue.InsertPending
	case panePlaylists:
		return i < len(m.core.Playlists.List)
	case panePlaylistTracks:
		return !m.accountSelected()
	case paneNone, paneResults, paneHistory, paneDevices:
	}
	return false
}

// dragTo moves the dragged row to the row under the pointer, the way K and J
// would, so the row stays under the pointer.
func (m Model) dragTo(x, y int) (tea.Model, tea.Cmd) {
	p, to := m.paneAt(x, y)
	if p != m.drag || to < 0 || !m.draggable(p, to) {
		return m, nil
	}
	from, _ := m.listCursor(p)
	switch p {
	case paneQueue:
		return m, m.moveQueueEntry(from, to)
	case panePlaylists:
		m.movePlaylist(from, to)
	case panePlaylistTracks:
		m.movePlaylistTrack(from, to)
	case paneNone, paneResults, paneHistory, paneDevices:
	}
	return m, nil
}

func (m Model) scroll(x, y, delta int) Model {
	p, _ := m.paneAt(x, y)
	if !m.focusPane(p) {
		return m
	}
	if cursor, n := m.listCursor(p); n > 0 {
		m.selectRow(p, min(max(cursor+delta, 0), n-1))
	}
	return m
}

func (m *Model) focusPane(p listPane) bool {
	switch p {
	case paneResults:
		m.focus = focusResults
	case paneQueue:
		m.focus = focusQueue
	case panePlaylists:
		if m.overlay != overlayPicker {
			m.focus = focusPlaylists
		}
	case panePlaylistTracks:
		if m.overlay == overlayPicker || m.playlistCount() == 0 {
			return false
		}
		m.focus = focusPlaylistTracks
	case paneHistory:
		m.focus = focusHistory
	case paneDevices:
	case paneNone:
		return false
	}
	m.input.Blur()
	if p != paneResults {
		m.results.filter.Blur()
	}
	return true
}

func (m *Model) selectRow(p listPane, i int) {
	switch p {
	case paneResults:
		m.results.cur = i
	case paneQueue:
		m.queueCur = i
	case panePlaylists:
		if i != m.playlistCur {
			m.playlistCur, m.playlistTrackCur = i, 0
		}
	case panePlaylistTracks:
		m.playlistTrackCur = i
	case paneHistory:
		m.historyCur = i
	case paneDevices:
		m.deviceCur = i
	case paneNone:
	}
}

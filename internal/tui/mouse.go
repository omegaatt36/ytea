package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.overlay == overlayName || m.fullHelp {
		return m, nil
	}
	mouse := msg.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		if mouse.Button == tea.MouseLeft {
			m.deletePlaylistPending = -1
			return m.click(mouse.X, mouse.Y)
		}
	case tea.MouseWheelMsg:
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
	if m.overlay != overlayDevices {
		switch row := m.tabsRow(); {
		case y < row:
			return m, m.focusSearch()
		case y == row:
			if f, ok := m.tabAt(x); ok {
				m.input.Blur()
				m.filterInput.Blur()
				m.overlay, m.focus = overlayNone, f
			}
			return m, nil
		}
	}
	p, i := m.paneAt(x, y)
	if m.focusPane(p) && i >= 0 {
		m.selectRow(p, i)
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
		if m.overlay == overlayPicker || len(m.playlists) == 0 {
			return false
		}
		m.focus = focusPlaylistTracks
	case paneDevices:
	default:
		return false
	}
	m.input.Blur()
	if p != paneResults {
		m.filterInput.Blur()
	}
	return true
}

func (m *Model) selectRow(p listPane, i int) {
	switch p {
	case paneResults:
		m.resultCur = i
	case paneQueue:
		m.queueCur = i
	case panePlaylists:
		if i != m.playlistCur {
			m.playlistCur, m.playlistTrackCur = i, 0
		}
	case panePlaylistTracks:
		m.playlistTrackCur = i
	case paneDevices:
		m.deviceCur = i
	}
}

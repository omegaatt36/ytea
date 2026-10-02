package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/service"
)

func (m Model) openSeek() (tea.Model, tea.Cmd) {
	if !m.core.Seekable() {
		m.setStatus("nothing seekable playing")
		return m, nil
	}
	return m.openName(nameSeek, "", "seek within "+service.FormatDuration(m.core.Playback.Duration))
}

func (m Model) submitSeek() (tea.Model, tea.Cmd) {
	if !m.core.Seekable() {
		m.closeName()
		m.setStatus("nothing seekable playing")
		return m, nil
	}
	pos, err := service.ParseSeekTarget(m.nameInput.Value(), m.core.Playback.Duration)
	if err != nil {
		m.setError(err.Error())
		return m, nil
	}
	m.closeName()
	return m, m.seekTo(pos)
}

func (m *Model) seekTo(pos time.Duration) tea.Cmd {
	m.setStatus("seek to " + service.FormatDuration(pos))
	return teaCmd(m.core.SeekTo(pos))
}

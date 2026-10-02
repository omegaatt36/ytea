package tui

import (
	"fmt"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

// reloadHistory keeps the cursor on its track when a new play is prepended.
func (m *Model) reloadHistory() {
	var selected string
	if t, ok := m.selectedHistoryTrack(); ok {
		selected = t.URL
	}
	m.core.Update(service.HistoryRecorded{})
	entries := m.core.History.Entries
	if i := slices.IndexFunc(entries, func(e domain.HistoryEntry) bool { return e.Track.URL == selected }); i >= 0 {
		m.historyCur = i
	}
	m.historyCur = min(m.historyCur, max(0, len(entries)-1))
}

func (m Model) selectedHistoryTrack() (domain.Track, bool) {
	if m.historyCur < 0 || m.historyCur >= len(m.core.History.Entries) {
		return domain.Track{}, false
	}
	return m.core.History.Entries[m.historyCur].Track, true
}

func (m Model) handleHistoryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.history
	last := max(0, len(m.core.History.Entries)-1)
	switch {
	case key.Matches(msg, k.Up):
		m.historyCur = max(0, m.historyCur-1)
	case key.Matches(msg, k.Down):
		m.historyCur = min(last, m.historyCur+1)
	case key.Matches(msg, k.Top):
		m.historyCur = 0
	case key.Matches(msg, k.Bottom):
		m.historyCur = last
	case key.Matches(msg, k.Play):
		if t, ok := m.selectedHistoryTrack(); ok {
			cmd := teaCmd(m.core.PlayNow(t))
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Enqueue):
		if t, ok := m.selectedHistoryTrack(); ok {
			cmd := teaCmd(m.core.Enqueue(t))
			m.setStatus("queueing " + quote(t.Title) + "…")
			m.historyCur = min(last, m.historyCur+1)
			return m, cmd
		}
	case key.Matches(msg, k.Save):
		if t, ok := m.selectedHistoryTrack(); ok {
			return m.openPlaylistPicker(t)
		}
	case key.Matches(msg, k.Remove):
		if _, ok := m.selectedHistoryTrack(); ok && m.core.History.Enabled() {
			if err := m.core.History.Remove(m.historyCur); err != nil {
				m.setError(err.Error())
				return m, nil
			}
			m.historyCur = min(m.historyCur, max(0, len(m.core.History.Entries)-1))
			m.setStatus("removed from history")
		}
	}
	return m, nil
}

// playedAgo is a compact age for the history's time column.
func playedAgo(at, now time.Time) string {
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return at.Format("Jan 2")
}

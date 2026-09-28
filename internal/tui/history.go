package tui

import (
	"fmt"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/history"
	"github.com/omegaatt36/ytea/internal/youtube"
)

type historyChangedMsg struct{}

// recordPlay adds the current track to the history once it is audibly
// playing, so a session restored paused or a track that fails to load is not
// counted as played.
func (m *Model) recordPlay() tea.Cmd {
	h := m.deps.History
	e, _, ok := m.current()
	if h == nil || !ok || m.player.paused || m.player.timePos <= 0 || e.Filename == m.historyLast {
		return nil
	}
	m.historyLast = e.Filename
	t := m.entryTrack(e)
	if t.Duration == 0 && !t.Live {
		t.Duration = m.player.duration
	}
	return func() tea.Msg {
		if err := h.Record(t, time.Now()); err != nil {
			return errMsg{fmt.Errorf("record history: %w", err)}
		}
		return historyChangedMsg{}
	}
}

// reloadHistory keeps the cursor on its track when a new play is prepended.
func (m *Model) reloadHistory() {
	if m.deps.History == nil {
		return
	}
	var selected string
	if t, ok := m.selectedHistoryTrack(); ok {
		selected = t.URL
	}
	m.history = m.deps.History.Entries()
	if i := slices.IndexFunc(m.history, func(e history.Entry) bool { return e.Track.URL == selected }); i >= 0 {
		m.historyCur = i
	}
	m.historyCur = min(m.historyCur, max(0, len(m.history)-1))
}

func (m Model) selectedHistoryTrack() (youtube.Track, bool) {
	if m.historyCur < 0 || m.historyCur >= len(m.history) {
		return youtube.Track{}, false
	}
	return m.history[m.historyCur].Track, true
}

func (m Model) handleHistoryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.history
	last := max(0, len(m.history)-1)
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
			m.tracks[t.URL] = t
			cmd := m.queue.playNow(m.nextRequest(), t)
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Enqueue):
		if t, ok := m.selectedHistoryTrack(); ok {
			m.tracks[t.URL] = t
			cmd := m.queue.enqueue(m.nextRequest(), t)
			m.setStatus("queueing " + quote(t.Title) + "…")
			m.historyCur = min(last, m.historyCur+1)
			return m, cmd
		}
	case key.Matches(msg, k.Save):
		if t, ok := m.selectedHistoryTrack(); ok {
			return m.openPlaylistPicker(t)
		}
	case key.Matches(msg, k.Remove):
		if _, ok := m.selectedHistoryTrack(); ok && m.deps.History != nil {
			if err := m.deps.History.Remove(m.historyCur); err != nil {
				m.setError(err.Error())
				return m, nil
			}
			m.history = m.deps.History.Entries()
			m.historyCur = min(m.historyCur, max(0, len(m.history)-1))
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

package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

func (m *Model) searchDone(msg service.SearchDone) {
	result := m.core.Update(msg)
	added, ok := result.SearchAdded, result.SearchAccepted
	if !ok {
		return
	}
	current := msg.RequestID == m.core.Active
	if msg.Err != nil {
		if current {
			m.setError("search failed: " + msg.Err.Error())
		}
		return
	}
	if msg.Offset > 0 {
		if current {
			m.setStatus(fmt.Sprintf("%s more for %s", pluralize(added, "result"), quote(msg.Query)))
		}
		return
	}
	m.results.reset(len(msg.Tracks))
	if current {
		m.setStatus(pluralize(len(msg.Tracks), "result") + " for " + quote(msg.Query))
	}
}

func (m Model) loadMoreResults() (tea.Model, tea.Cmd) {
	cmd := m.core.LoadMore()
	if cmd == nil {
		return m, nil
	}
	m.setStatus("loading more results for " + quote(m.core.Search.Query) + "…")
	return m, tea.Batch(m.spinner.Tick, teaCmd(cmd))
}

func (m *Model) queueDone(msg service.AppendDone) tea.Cmd {
	queueCmd := teaCmds(m.core.Update(msg).Cmds)
	// Playback events may have arrived while the append was still running.
	thumbCmd := m.refreshThumb()
	m.syncMPRIS()
	if msg.RequestID == m.core.Active {
		if msg.Err != nil {
			m.setError(msg.Err.Error())
		} else {
			status := pluralize(len(msg.Tracks), "track") + " queued"
			if msg.LimitHit {
				status += fmt.Sprintf(" (import limit: %d)", domain.MaxPlaylistItems)
			}
			m.setStatus(status)
		}
	}
	return tea.Batch(thumbCmd, queueCmd)
}

func (m Model) startRadio() (tea.Model, tea.Cmd) {
	cmd := m.core.StartRadio()
	if cmd == nil {
		m.setStatus("nothing playing to seed a radio from")
		return m, nil
	}
	m.setStatus("fetching radio…")
	return m, tea.Batch(m.spinner.Tick, teaCmd(cmd))
}

func (m *Model) resolveImport(msg service.LookupDone) tea.Cmd {
	return teaCmds(m.core.Update(msg).Cmds)
}

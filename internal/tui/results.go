package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

// cur indexes the visible rows, not tracks.
type resultsPane struct {
	cur    int
	filter textinput.Model
}

// update feeds the filter input; any change to the query starts over at the top row.
func (r resultsPane) update(msg tea.Msg) (resultsPane, tea.Cmd) {
	before := r.filter.Value()
	var cmd tea.Cmd
	r.filter, cmd = r.filter.Update(msg)
	if r.filter.Value() != before {
		r.cur = 0
	}
	return r, cmd
}

func (r *resultsPane) moveTo(i, rows int) {
	r.cur = max(0, min(rows-1, i))
}

func (r *resultsPane) reset(n int) {
	r.cur = 0
	r.filter.Reset()
	if n == 0 {
		r.filter.Blur()
	}
}

func (r *resultsPane) openFilter(n int) tea.Cmd {
	if n == 0 {
		return nil
	}
	return r.filter.Focus()
}

func (r resultsPane) filterOpen() bool {
	return r.filter.Focused() || r.filter.Value() != ""
}

func (r *resultsPane) setWidth(paneWidth int) {
	r.filter.SetWidth(max(1, paneWidth-2*boxInset-lipgloss.Width(r.filter.Prompt)-1))
}

func (m Model) resultRows() []service.Match {
	return service.FilterTracks(m.core.Search.Tracks, m.results.filter.Value())
}

func (m Model) selectedResult() (domain.Track, bool) {
	rows := m.resultRows()
	if m.results.cur < 0 || m.results.cur >= len(rows) {
		return domain.Track{}, false
	}
	return m.core.Search.Tracks[rows[m.results.cur].Index], true
}

func (m *Model) moveResult(i int) {
	m.results.moveTo(i, len(m.resultRows()))
}

func (m *Model) openResultFilter() tea.Cmd {
	return m.results.openFilter(len(m.core.Search.Tracks))
}

func (m *Model) clearResultFilter() {
	if rows := m.resultRows(); m.results.cur >= 0 && m.results.cur < len(rows) {
		m.results.cur = rows[m.results.cur].Index
	}
	m.results.filter.Reset()
	m.results.filter.Blur()
}

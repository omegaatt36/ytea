package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

// resultsPane holds the search results and the filter narrowing them. cur
// indexes the visible rows, not tracks.
type resultsPane struct {
	tracks []youtube.Track
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

func (r resultsPane) rows() []filterMatch {
	return filterResults(r.tracks, r.filter.Value())
}

func (r resultsPane) selected() (youtube.Track, bool) {
	rows := r.rows()
	if r.cur < 0 || r.cur >= len(rows) {
		return youtube.Track{}, false
	}
	return r.tracks[rows[r.cur].index], true
}

func (r *resultsPane) moveTo(i int) {
	r.cur = max(0, min(len(r.rows())-1, i))
}

// set replaces the results and clears the filter; a filter being typed stays
// open for the new list.
func (r *resultsPane) set(tracks []youtube.Track) {
	r.tracks, r.cur = tracks, 0
	r.filter.Reset()
	if len(tracks) == 0 {
		r.filter.Blur()
	}
}

func (r *resultsPane) openFilter() tea.Cmd {
	if len(r.tracks) == 0 {
		return nil
	}
	return r.filter.Focus()
}

// clearFilter keeps the selected track selected in the full list.
func (r *resultsPane) clearFilter() {
	if rows := r.rows(); r.cur >= 0 && r.cur < len(rows) {
		r.cur = rows[r.cur].index
	}
	r.filter.Reset()
	r.filter.Blur()
}

func (r resultsPane) filterOpen() bool {
	return r.filter.Focused() || r.filter.Value() != ""
}

func (r *resultsPane) setWidth(paneWidth int) {
	r.filter.SetWidth(max(1, paneWidth-2*boxInset-lipgloss.Width(r.filter.Prompt)-1))
}

package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

const (
	searchLimit   = 30
	radioLimit    = 25
	searchTimeout = 30 * time.Second
	importTimeout = 2 * time.Minute
)

type (
	searchDoneMsg struct {
		requestID uint64
		query     string
		tracks    []youtube.Track
		err       error
	}
	lookupDoneMsg struct {
		requestID uint64
		tracks    []youtube.Track
		limitHit  bool
		err       error
	}
	queueDoneMsg struct {
		requestID uint64
		tracks    []youtube.Track
		limitHit  bool
		err       error
	}
)

func search(s Searcher, query string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		tracks, err := s.Search(ctx, query, searchLimit)
		return searchDoneMsg{requestID: requestID, query: query, tracks: tracks, err: err}
	}
}

func (m *Model) searchDone(msg searchDoneMsg) {
	if msg.requestID != m.searchRequest {
		return
	}
	if msg.requestID == m.spinnerRequest {
		m.searching = false
	}
	if msg.err != nil {
		if msg.requestID == m.activeRequest {
			m.setError("search failed: " + msg.err.Error())
		}
		return
	}
	m.results.set(msg.tracks)
	for _, t := range msg.tracks {
		m.tracks[t.URL] = t
	}
	if msg.requestID == m.activeRequest {
		m.setStatus(pluralize(len(msg.tracks), "result") + " for " + quote(msg.query))
	}
}

func (m *Model) queueDone(msg queueDoneMsg) tea.Cmd {
	if msg.requestID == m.spinnerRequest {
		m.searching = false
	}
	// mpv may have accepted some entries before AppendAll returned an error.
	for _, t := range msg.tracks {
		m.tracks[t.URL] = t
	}
	// Playback events may have arrived while the append was still running.
	thumbCmd := m.refreshThumb()
	m.syncMPRIS()
	_, queueCmd := m.updateQueue(msg)
	if msg.requestID == m.activeRequest {
		if msg.err != nil {
			m.setError(msg.err.Error())
		} else {
			status := pluralize(len(msg.tracks), "track") + " queued"
			if msg.limitHit {
				status += fmt.Sprintf(" (import limit: %d)", youtube.MaxPlaylistItems)
			}
			m.setStatus(status)
		}
	}
	return tea.Batch(thumbCmd, queueCmd)
}

func (m Model) startRadio() (tea.Model, tea.Cmd) {
	_, t, ok := m.current()
	if !ok || t.ID == "" {
		m.setStatus("nothing playing to seed a radio from")
		return m, nil
	}
	requestID := m.nextRequest()
	m.imports = m.imports.add(requestID)
	m.spinnerRequest = requestID
	m.searching = true
	m.setStatus("fetching radio…")
	return m, tea.Batch(m.spinner.Tick, fetchRadio(m.deps.Searcher, t.ID, requestID))
}

// importQueue lists link and radio lookups in trigger order, so a slow lookup
// cannot reorder the imports queued after it.
type importQueue []pendingImport

type pendingImport struct {
	requestID uint64
	lookup    *lookupDoneMsg // nil while the lookup runs
}

func (q importQueue) add(requestID uint64) importQueue {
	return append(q, pendingImport{requestID: requestID})
}

// resolve records a finished lookup and returns, in trigger order, the
// lookups no longer waiting behind an earlier one.
func (q importQueue) resolve(msg lookupDoneMsg) (importQueue, []lookupDoneMsg) {
	i := slices.IndexFunc(q, func(p pendingImport) bool { return p.requestID == msg.requestID })
	if i < 0 {
		return q, nil
	}
	q = slices.Clone(q)
	q[i].lookup = &msg
	var ready []lookupDoneMsg
	for len(q) > 0 && q[0].lookup != nil {
		ready = append(ready, *q[0].lookup)
		q = q[1:]
	}
	return q, ready
}

// Slots are reserved only once a lookup resolves, so queue edits made during
// a lookup neither wait on the network nor act on its tracks.
func (m *Model) resolveImport(msg lookupDoneMsg) tea.Cmd {
	var ready []lookupDoneMsg
	m.imports, ready = m.imports.resolve(msg)
	p := m.deps.Player
	cmds := make([]tea.Cmd, len(ready))
	for i, lookup := range ready {
		cmds[i] = queueImport(func(tracks []youtube.Track) error { return appendTracks(p, tracks) }, lookup, m.queue.reserve())
	}
	return tea.Batch(cmds...)
}

func queueImport(appendQueue func([]youtube.Track) error, lookup lookupDoneMsg, task queueTask) tea.Cmd {
	return func() tea.Msg {
		err := task.run(func() error {
			if lookup.err != nil || len(lookup.tracks) == 0 {
				return lookup.err
			}
			return appendQueue(lookup.tracks)
		})
		return queueDoneMsg{requestID: lookup.requestID, tracks: lookup.tracks, limitHit: lookup.limitHit, err: err}
	}
}

func fetchQueue(s Searcher, link youtube.Link, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		limit := 0
		if link.Mix {
			limit = radioLimit
		}
		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()
		tracks, err := s.Lookup(ctx, link.URL, limit)
		if link.Mix && len(tracks) > radioLimit {
			tracks = tracks[:radioLimit]
		}
		if !link.Mix && len(tracks) > youtube.MaxPlaylistItems {
			tracks = tracks[:youtube.MaxPlaylistItems]
		}
		limitHit := !link.Mix && len(tracks) == youtube.MaxPlaylistItems
		return lookupDoneMsg{requestID: requestID, tracks: tracks, limitHit: limitHit, err: err}
	}
}

func fetchRadio(s Searcher, seedID string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		ref := "https://www.youtube.com/watch?v=" + seedID + "&list=RD" + seedID
		tracks, err := s.Lookup(ctx, ref, radioLimit+1)
		// The mix lists its seed first, and the seed is what is playing.
		tracks = slices.DeleteFunc(tracks, func(t youtube.Track) bool { return t.ID == seedID })
		if len(tracks) > radioLimit {
			tracks = tracks[:radioLimit]
		}
		return lookupDoneMsg{requestID: requestID, tracks: tracks, err: err}
	}
}

func appendTracks(p Player, tracks []youtube.Track) error {
	urls := make([]string, len(tracks))
	for i, track := range tracks {
		urls[i] = track.URL
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return p.AppendAll(ctx, urls)
}

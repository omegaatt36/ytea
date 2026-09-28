package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

type accountPlaylistState struct {
	started, loading bool
	playlists        []youtube.AccountPlaylist
	err              error
	tracks           map[string][]youtube.Track
	// Reload advances the generation so delayed requests cannot repopulate the new view.
	generation   uint64
	trackLoading map[string]bool
	trackErrors  map[string]error
	queueErrors  map[string]error
}

type accountPlaylistsMsg struct {
	generation uint64
	playlists  []youtube.AccountPlaylist
	err        error
}

type accountTracksMsg struct {
	generation uint64
	id         string
	tracks     []youtube.Track
	err        error
}

// Queue lookups must not complete or clear an independent browse request.
type accountQueueFailedMsg struct {
	generation uint64
	id         string
	err        error
}

func (m Model) accountSelected() bool {
	return m.deps.AccountPlaylists != nil && m.overlay != overlayPicker && m.playlistCur >= len(m.playlists)
}

func (m Model) playlistCount() int {
	if m.deps.AccountPlaylists == nil || m.overlay == overlayPicker {
		return len(m.playlists)
	}
	return len(m.playlists) + max(1, len(m.account.playlists))
}

func (m Model) selectedAccountPlaylist() (youtube.AccountPlaylist, bool) {
	i := m.playlistCur - len(m.playlists)
	if !m.accountSelected() || i < 0 || i >= len(m.account.playlists) {
		return youtube.AccountPlaylist{}, false
	}
	return m.account.playlists[i], true
}

func (m *Model) loadAccountOnFocus() tea.Cmd {
	if m.focus == focusPlaylistTracks {
		return m.loadAccountTracks()
	}
	if m.focus != focusPlaylists || m.deps.AccountPlaylists == nil || m.account.started {
		return nil
	}
	return m.reloadAccount()
}

func (m *Model) reloadAccount() tea.Cmd {
	if m.account.loading {
		return nil
	}
	if m.accountSelected() {
		m.playlistCur = len(m.playlists)
	}
	m.account.playlists = nil
	m.account.started, m.account.loading = true, true
	m.account.err = nil
	m.account.generation++
	generation := m.account.generation
	m.account.trackLoading = make(map[string]bool)
	m.account.trackErrors = make(map[string]error)
	m.account.queueErrors = make(map[string]error)
	m.account.tracks = make(map[string][]youtube.Track)
	source := m.deps.AccountPlaylists
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()
		playlists, err := source.ListPlaylists(ctx)
		return accountPlaylistsMsg{generation: generation, playlists: playlists, err: err}
	}
}

func (m *Model) loadAccountTracks() tea.Cmd {
	p, ok := m.selectedAccountPlaylist()
	if !ok || m.account.loading || m.account.err != nil || m.account.trackLoading[p.ID] {
		return nil
	}
	if _, loaded := m.account.tracks[p.ID]; loaded {
		return nil
	}
	if m.account.trackErrors[p.ID] != nil {
		return nil
	}
	m.account.trackLoading[p.ID] = true
	source, generation := m.deps.AccountPlaylists, m.account.generation
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()
		tracks, err := source.ListTracks(ctx, p.ID)
		return accountTracksMsg{generation: generation, id: p.ID, tracks: tracks, err: err}
	}
}

func (m Model) handleAccountAction(key string) (tea.Model, tea.Cmd) {
	if m.account.loading || m.account.err != nil {
		return m, nil
	}
	p, ok := m.selectedAccountPlaylist()
	if !ok {
		return m, nil
	}
	tracks, loaded := m.account.tracks[p.ID]
	if m.focus == focusPlaylists && key == "enter" {
		m.focus = focusPlaylistTracks
		m.playlistTrackCur = 0
		delete(m.account.trackErrors, p.ID)
		cmd := m.loadAccountTracks()
		return m, cmd
	}
	if m.account.trackLoading[p.ID] {
		return m, nil
	}
	if m.deps.Player == nil {
		return m, nil
	}
	if m.focus == focusPlaylistTracks {
		track, ok := m.selectedPlaylistTrack()
		if !ok {
			return m, nil
		}
		m.tracks[track.URL] = track
		if key == "enter" {
			return m, m.queue.playNow(m.nextRequest(), track)
		}
		return m, m.queue.enqueue(m.nextRequest(), track)
	}
	requestID, task := m.nextRequest(), m.queue.reserve()
	source, player := m.deps.AccountPlaylists, m.deps.Player
	generation := m.account.generation
	delete(m.account.queueErrors, p.ID)
	return m, func() tea.Msg {
		if !loaded {
			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()
			var err error
			tracks, err = source.ListTracks(ctx, p.ID)
			if err != nil {
				_ = task.run(func() error { return err })
				return accountQueueFailedMsg{generation: generation, id: p.ID, err: err}
			}
		}
		return appendPlaylist(player.AppendAll, tracks, requestID, task)()
	}
}

func (m Model) accountPlaylistNames() []string {
	if m.deps.AccountPlaylists == nil || m.overlay == overlayPicker {
		return nil
	}
	switch {
	case m.account.loading:
		return []string{"YouTube — loading…"}
	case m.account.err != nil:
		return []string{"YouTube — " + m.account.err.Error()}
	case len(m.account.playlists) == 0:
		return []string{"YouTube — no playlists"}
	}
	names := make([]string, len(m.account.playlists))
	for i, p := range m.account.playlists {
		names[i] = fmt.Sprintf("YouTube — %s", p.Title)
	}
	return names
}

package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
)

func (m Model) accountSelected() bool {
	return m.core.Account.Enabled() && m.overlay != overlayPicker && m.playlistCur >= len(m.core.Playlists.List)
}

func (m Model) playlistCount() int {
	if !m.core.Account.Enabled() || m.overlay == overlayPicker {
		return len(m.core.Playlists.List)
	}
	return len(m.core.Playlists.List) + max(1, len(m.core.Account.Playlists))
}

func (m Model) selectedAccountPlaylist() (domain.AccountPlaylist, bool) {
	i := m.playlistCur - len(m.core.Playlists.List)
	if !m.accountSelected() || i < 0 || i >= len(m.core.Account.Playlists) {
		return domain.AccountPlaylist{}, false
	}
	return m.core.Account.Playlists[i], true
}

func (m *Model) loadAccountOnFocus() tea.Cmd {
	if m.focus == focusPlaylistTracks {
		return m.loadAccountTracks()
	}
	if m.focus != focusPlaylists || !m.core.Account.Enabled() || m.core.Account.Started {
		return nil
	}
	return m.reloadAccount()
}

func (m *Model) reloadAccount() tea.Cmd {
	cmd := m.core.Account.Reload()
	if cmd != nil && m.accountSelected() {
		m.playlistCur = len(m.core.Playlists.List)
	}
	return teaCmd(cmd)
}

func (m *Model) loadAccountTracks() tea.Cmd {
	p, ok := m.selectedAccountPlaylist()
	if !ok {
		return nil
	}
	return teaCmd(m.core.Account.LoadTracks(p.ID))
}

func (m Model) handleAccountAction(key string) (tea.Model, tea.Cmd) {
	account := &m.core.Account
	if account.Loading || account.Err != nil {
		return m, nil
	}
	p, ok := m.selectedAccountPlaylist()
	if !ok {
		return m, nil
	}
	if m.focus == focusPlaylists && key == "enter" {
		m.focus = focusPlaylistTracks
		m.playlistTrackCur = 0
		account.Retry(p.ID)
		return m, m.loadAccountTracks()
	}
	if account.TrackLoading[p.ID] {
		return m, nil
	}
	if m.focus == focusPlaylistTracks {
		track, ok := m.selectedPlaylistTrack()
		if !ok {
			return m, nil
		}
		if key == "enter" {
			return m, teaCmd(m.core.PlayNow(track))
		}
		return m, teaCmd(m.core.Enqueue(track))
	}
	return m, teaCmd(m.core.QueueAccountPlaylist(p.ID))
}

func (m Model) accountPlaylistNames() []string {
	account := m.core.Account
	if !account.Enabled() || m.overlay == overlayPicker {
		return nil
	}
	switch {
	case account.Loading:
		return []string{"YouTube — loading…"}
	case account.Err != nil:
		return []string{"YouTube — " + account.Err.Error()}
	case len(account.Playlists) == 0:
		return []string{"YouTube — no playlists"}
	}
	names := make([]string, len(account.Playlists))
	for i, p := range account.Playlists {
		names[i] = fmt.Sprintf("YouTube — %s", p.Title)
	}
	return names
}

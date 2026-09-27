package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

func (m *Model) cycleTab(reverse bool) {
	m.deletePlaylistPending = -1
	tab := m.activeTab()
	m.overlay = overlayNone
	if reverse {
		switch tab {
		case focusResults:
			m.focus = focusPlaylists
		case focusQueue:
			m.focus = focusResults
		default:
			m.focus = focusQueue
		}
		return
	}
	switch tab {
	case focusResults:
		m.focus = focusQueue
	case focusQueue:
		m.focus = focusPlaylists
	default:
		m.focus = focusResults
	}
}

func (m Model) openPlaylistPicker(track youtube.Track) (tea.Model, tea.Cmd) {
	if m.deps.Library == nil {
		m.setError("local playlists are unavailable")
		return m, nil
	}
	m.saveTrack = track
	if len(m.playlists) == 0 {
		return m.openPlaylistName([]youtube.Track{track}, true, "name the new playlist")
	}
	m.overlay = overlayPicker
	m.setStatus("choose a playlist for " + quote(track.Title))
	return m, nil
}

func (m Model) openPlaylistName(tracks []youtube.Track, saves bool, status string) (tea.Model, tea.Cmd) {
	m.nameTracks, m.nameSaves = tracks, saves
	m.overlay = overlayName
	m.nameInput.SetValue("")
	m.input.Blur()
	m.setStatus(status)
	return m, m.nameInput.Focus()
}

func (m Model) handlePlaylistNameKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.name.Cancel):
		m.nameInput.Blur()
		m.nameTracks = nil
		m.overlay = overlayNone
		return m, nil
	case key.Matches(msg, m.keys.name.Create):
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.setError("playlist name is empty")
			return m, nil
		}
		index, err := m.deps.Library.CreateWithTracks(name, m.nameTracks)
		if err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.nameInput.Blur()
		m.nameTracks = nil
		m.playlistCur = index
		m.playlistTrackCur = 0
		m.playlists = m.deps.Library.Playlists()
		m.overlay = overlayNone
		if m.nameSaves {
			m.setStatus("saved to " + quote(name))
			return m, nil
		}
		m.focus = focusPlaylists
		m.setStatus("created " + quote(name))
		return m, nil
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m Model) handlePlaylistKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.overlay == overlayPicker:
		return m.handlePickerKey(msg)
	case m.focus == focusPlaylistTracks:
		return m.handlePlaylistTracksKey(msg)
	}
	return m.handlePlaylistsKey(msg)
}

func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.picker
	switch {
	case key.Matches(msg, k.Up):
		m.playlistCur = max(0, m.playlistCur-1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Down):
		m.playlistCur = min(max(0, len(m.playlists)-1), m.playlistCur+1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Top):
		m.playlistCur, m.playlistTrackCur = 0, 0
	case key.Matches(msg, k.Bottom):
		m.playlistCur = max(0, len(m.playlists)-1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Cancel):
		m.overlay = overlayNone
	case key.Matches(msg, k.Create):
		return m.openPlaylistName([]youtube.Track{m.saveTrack}, true, "name the new playlist")
	case key.Matches(msg, k.Save):
		if m.playlistCur >= len(m.playlists) {
			return m, nil
		}
		name := m.playlists[m.playlistCur].Name
		if err := m.deps.Library.Add(m.playlistCur, m.saveTrack); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.playlists = m.deps.Library.Playlists()
		m.overlay = overlayNone
		m.setStatus("saved to " + quote(name))
	}
	return m, nil
}

func (m Model) handlePlaylistsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.playlists
	switch {
	case key.Matches(msg, k.Up):
		m.playlistCur = max(0, m.playlistCur-1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Down):
		m.playlistCur = min(max(0, len(m.playlists)-1), m.playlistCur+1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Top):
		m.playlistCur, m.playlistTrackCur = 0, 0
	case key.Matches(msg, k.Bottom):
		m.playlistCur = max(0, len(m.playlists)-1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Back):
		m.focus = focusResults
	case key.Matches(msg, k.Create):
		return m.openPlaylistName(nil, false, "name the new playlist")
	case key.Matches(msg, k.Browse):
		if len(m.playlists) > 0 {
			m.focus = focusPlaylistTracks
		}
	case key.Matches(msg, k.EnqueueAll):
		tracks := m.selectedPlaylistTracks()
		if len(tracks) == 0 {
			return m, nil
		}
		requestID, task := m.nextRequest(), m.queue.reserve()
		m.spinnerRequest = requestID
		m.searching = true
		m.setStatus("queueing " + quote(m.playlists[m.playlistCur].Name) + "…")
		return m, tea.Batch(m.spinner.Tick, appendPlaylist(func(ctx context.Context, urls []string) error {
			return m.deps.Player.AppendAll(ctx, urls)
		}, tracks, requestID, task))
	case key.Matches(msg, k.Delete):
		if m.playlistCur >= len(m.playlists) {
			return m, nil
		}
		name := m.playlists[m.playlistCur].Name
		if m.deletePlaylistPending != m.playlistCur {
			m.deletePlaylistPending = m.playlistCur
			m.setStatus("press " + m.keys.playlists.Delete.Help().Key + " again to delete " + quote(name))
			return m, nil
		}
		m.deletePlaylistPending = -1
		if err := m.deps.Library.Delete(m.playlistCur); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.playlists = m.deps.Library.Playlists()
		m.playlistCur = min(m.playlistCur, max(0, len(m.playlists)-1))
		m.playlistTrackCur = 0
		m.setStatus("deleted " + quote(name))
	}
	return m, nil
}

func (m Model) handlePlaylistTracksKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.playlistTracks
	switch {
	case key.Matches(msg, k.Up):
		m.playlistTrackCur = max(0, m.playlistTrackCur-1)
	case key.Matches(msg, k.Down):
		m.playlistTrackCur = min(max(0, len(m.selectedPlaylistTracks())-1), m.playlistTrackCur+1)
	case key.Matches(msg, k.Top):
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Bottom):
		m.playlistTrackCur = max(0, len(m.selectedPlaylistTracks())-1)
	case key.Matches(msg, k.Back):
		m.focus = focusPlaylists
	case key.Matches(msg, k.Play):
		if t, ok := m.selectedPlaylistTrack(); ok {
			m.tracks[t.URL] = t
			cmd := m.queue.playNow(m.nextRequest(), t)
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Enqueue):
		if t, ok := m.selectedPlaylistTrack(); ok {
			m.tracks[t.URL] = t
			cmd := m.queue.enqueue(m.nextRequest(), t)
			m.setStatus("queueing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Remove):
		if _, ok := m.selectedPlaylistTrack(); ok {
			if err := m.deps.Library.RemoveTrack(m.playlistCur, m.playlistTrackCur); err != nil {
				m.setError(err.Error())
				return m, nil
			}
			m.playlists = m.deps.Library.Playlists()
			m.playlistTrackCur = min(m.playlistTrackCur, max(0, len(m.selectedPlaylistTracks())-1))
			m.setStatus("removed from playlist")
		}
	}
	return m, nil
}

func (m Model) selectedPlaylistTracks() []youtube.Track {
	if m.playlistCur < 0 || m.playlistCur >= len(m.playlists) {
		return nil
	}
	return m.playlists[m.playlistCur].Tracks
}

func (m Model) selectedPlaylistTrack() (youtube.Track, bool) {
	tracks := m.selectedPlaylistTracks()
	if m.playlistTrackCur < 0 || m.playlistTrackCur >= len(tracks) {
		return youtube.Track{}, false
	}
	return tracks[m.playlistTrackCur], true
}

func appendPlaylist(appendAll func(context.Context, []string) error, tracks []youtube.Track, requestID uint64, task queueTask) tea.Cmd {
	return func() tea.Msg {
		urls := make([]string, len(tracks))
		for i, t := range tracks {
			urls[i] = t.URL
		}
		err := task.run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()
			return appendAll(ctx, urls)
		})
		return queueDoneMsg{requestID: requestID, tracks: tracks, err: err}
	}
}

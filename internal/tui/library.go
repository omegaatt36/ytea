package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

func (m *Model) cycleTab(reverse bool) {
	m.deletePlaylistPending = -1
	if reverse {
		switch m.focus {
		case focusResults:
			m.focus = focusPlaylists
		case focusQueue:
			m.focus = focusResults
		default:
			m.focus = focusQueue
		}
		return
	}
	switch m.focus {
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
	m.saveTrack, m.saveReturn = track, m.focus
	if len(m.playlists) == 0 {
		return m.openPlaylistName(focusPlaylistPicker)
	}
	m.focus = focusPlaylistPicker
	m.setStatus("choose a playlist for " + quote(track.Title))
	return m, nil
}

func (m Model) openPlaylistName(from focus) (tea.Model, tea.Cmd) {
	m.nameReturn = from
	m.nameTracks = nil
	m.focus = focusPlaylistName
	m.nameInput.SetValue("")
	m.input.Blur()
	m.setStatus("name the new playlist")
	return m, m.nameInput.Focus()
}

func (m Model) handlePlaylistNameKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.nameInput.Blur()
		m.nameTracks = nil
		if m.nameReturn == focusPlaylistPicker {
			m.focus = m.saveReturn
		} else {
			m.focus = m.nameReturn
		}
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.setError("playlist name is empty")
			return m, nil
		}
		tracks := m.nameTracks
		if m.nameReturn == focusPlaylistPicker {
			tracks = []youtube.Track{m.saveTrack}
		}
		index, err := m.deps.Library.CreateWithTracks(name, tracks)
		if err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.nameInput.Blur()
		m.nameTracks = nil
		m.playlistCur = index
		m.playlistTrackCur = 0
		m.playlists = m.deps.Library.Playlists()
		if m.nameReturn == focusPlaylistPicker {
			m.focus = m.saveReturn
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

func (m Model) handlePlaylistKey(key string) (tea.Model, tea.Cmd) {
	if key != "D" {
		m.deletePlaylistPending = -1
	}
	switch key {
	case "up", "k":
		if m.focus == focusPlaylistTracks {
			m.playlistTrackCur = max(0, m.playlistTrackCur-1)
		} else {
			m.playlistCur = max(0, m.playlistCur-1)
			m.playlistTrackCur = 0
		}
	case "down", "j":
		if m.focus == focusPlaylistTracks {
			m.playlistTrackCur = min(max(0, len(m.selectedPlaylistTracks())-1), m.playlistTrackCur+1)
		} else {
			m.playlistCur = min(max(0, len(m.playlists)-1), m.playlistCur+1)
			m.playlistTrackCur = 0
		}
	case "g", "home":
		if m.focus == focusPlaylistTracks {
			m.playlistTrackCur = 0
		} else {
			m.playlistCur, m.playlistTrackCur = 0, 0
		}
	case "G", "end":
		if m.focus == focusPlaylistTracks {
			m.playlistTrackCur = max(0, len(m.selectedPlaylistTracks())-1)
		} else {
			m.playlistCur = max(0, len(m.playlists)-1)
			m.playlistTrackCur = 0
		}
	case "esc":
		switch m.focus {
		case focusPlaylistPicker:
			m.focus = m.saveReturn
		case focusPlaylistTracks:
			m.focus = focusPlaylists
		default:
			m.focus = focusResults
		}
	case "c":
		if m.focus != focusPlaylistTracks {
			return m.openPlaylistName(m.focus)
		}
	case "enter":
		switch m.focus {
		case focusPlaylistPicker:
			if m.playlistCur >= len(m.playlists) {
				return m, nil
			}
			name := m.playlists[m.playlistCur].Name
			if err := m.deps.Library.Add(m.playlistCur, m.saveTrack); err != nil {
				m.setError(err.Error())
				return m, nil
			}
			m.playlists = m.deps.Library.Playlists()
			m.focus = m.saveReturn
			m.setStatus("saved to " + quote(name))
		case focusPlaylists:
			if len(m.playlists) > 0 {
				m.focus = focusPlaylistTracks
			}
		case focusPlaylistTracks:
			if t, ok := m.selectedPlaylistTrack(); ok {
				m.tracks[t.URL] = t
				requestID, task := m.nextRequest(), m.reserveQueue()
				m.expectQueue()
				m.queueInsertPending = true
				m.setStatus("playing " + quote(t.Title) + "…")
				return m, projectedQueueAction(task, requestID, "playing "+quote(t.Title), func(ctx context.Context) error { return m.deps.Player.PlayNow(ctx, t.URL) })
			}
		}
	case "a":
		if m.focus == focusPlaylistPicker {
			return m, nil
		}
		if m.focus == focusPlaylistTracks {
			if t, ok := m.selectedPlaylistTrack(); ok {
				m.tracks[t.URL] = t
				requestID, task := m.nextRequest(), m.reserveQueue()
				m.setStatus("queueing " + quote(t.Title) + "…")
				return m, queueAction(task, requestID, "queued "+quote(t.Title), func(ctx context.Context) error { return m.deps.Player.Append(ctx, t.URL) })
			}
			return m, nil
		}
		tracks := m.selectedPlaylistTracks()
		if len(tracks) == 0 {
			return m, nil
		}
		requestID, task := m.nextRequest(), m.reserveQueue()
		m.spinnerRequest = requestID
		m.searching = true
		m.setStatus("queueing " + quote(m.playlists[m.playlistCur].Name) + "…")
		return m, tea.Batch(m.spinner.Tick, appendPlaylist(func(ctx context.Context, urls []string) error {
			return m.deps.Player.AppendAll(ctx, urls)
		}, tracks, requestID, task))
	case "d":
		if m.focus == focusPlaylistTracks {
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
	case "D":
		if m.focus == focusPlaylists && m.playlistCur < len(m.playlists) {
			name := m.playlists[m.playlistCur].Name
			if m.deletePlaylistPending != m.playlistCur {
				m.deletePlaylistPending = m.playlistCur
				m.setStatus("press D again to delete " + quote(name))
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

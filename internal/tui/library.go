package tui

import (
	"context"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

func (m *Model) cycleTab(reverse bool) {
	m.deletePlaylistPending = -1
	i := slices.IndexFunc(tabs, func(t tab) bool { return t.focus == m.activeTab() })
	step := 1
	if reverse {
		step = len(tabs) - 1
	}
	m.overlay = overlayNone
	m.focus = tabs[(i+step)%len(tabs)].focus
}

// nameMode is what the one-line dialog's input is for.
type nameMode int

const (
	nameCreate nameMode = iota // a new playlist holding nameTracks
	nameSave                   // a new playlist the track being saved goes into
	nameRename                 // a new name for the selected playlist
	nameSeek                   // a position in the playing track
)

func (n nameMode) title() string {
	switch n {
	case nameRename:
		return "Rename playlist"
	case nameSeek:
		return "Seek to"
	default:
		return "New playlist"
	}
}

func (m Model) nameKeys() nameKeyMap {
	keys := m.keys.name
	switch m.nameMode {
	case nameRename:
		keys.Create.SetHelp("enter", "rename")
	case nameSeek:
		keys.Create.SetHelp("enter", "seek")
	default:
	}
	return keys
}

func (m Model) openPlaylistPicker(track youtube.Track) (tea.Model, tea.Cmd) {
	if m.deps.Library == nil {
		m.setError("local playlists are unavailable")
		return m, nil
	}
	m.saveTrack = track
	if len(m.playlists) == 0 {
		return m.openPlaylistName([]youtube.Track{track}, nameSave, "name the new playlist")
	}
	m.playlistCur = min(m.playlistCur, len(m.playlists)-1)
	m.playlistTrackCur = 0
	m.overlay = overlayPicker
	m.setStatus("choose a playlist for " + quote(track.Title))
	return m, nil
}

func (m Model) openPlaylistName(tracks []youtube.Track, mode nameMode, status string) (tea.Model, tea.Cmd) {
	m.nameTracks = tracks
	return m.openName(mode, "", status)
}

// openName opens the one-line dialog; value prefills the input.
func (m Model) openName(mode nameMode, value, status string) (tea.Model, tea.Cmd) {
	m.nameMode = mode
	m.overlay = overlayName
	m.nameInput.Prompt, m.nameInput.Placeholder = "name: ", "playlist name"
	if mode == nameSeek {
		m.nameInput.Prompt, m.nameInput.Placeholder = "time: ", "1:23, 90 or 50%"
	}
	m.nameInput.SetValue(value)
	m.input.Blur()
	m.setStatus(status)
	return m, m.nameInput.Focus()
}

func (m *Model) closeName() {
	m.nameInput.Blur()
	m.nameTracks = nil
	m.overlay = overlayNone
}

func (m Model) handlePlaylistNameKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.name.Cancel):
		m.closeName()
		return m, nil
	case key.Matches(msg, m.keys.name.Create) && m.nameMode == nameSeek:
		return m.submitSeek()
	case key.Matches(msg, m.keys.name.Create) && m.nameMode == nameRename:
		name := strings.TrimSpace(m.nameInput.Value())
		if err := m.deps.Library.Rename(m.playlistCur, name); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.closeName()
		m.playlists = m.deps.Library.Playlists()
		m.setStatus("renamed to " + quote(name))
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
		m.closeName()
		m.playlistCur = index
		m.playlistTrackCur = 0
		m.playlists = m.deps.Library.Playlists()
		if m.nameMode == nameSave {
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
		return m.openPlaylistName([]youtube.Track{m.saveTrack}, nameSave, "name the new playlist")
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
	if m.accountSelected() {
		k.Reload.SetEnabled(true)
		switch {
		case key.Matches(msg, k.Reload):
			return m, m.reloadAccount()
		case key.Matches(msg, k.Browse):
			return m.handleAccountAction("enter")
		case key.Matches(msg, k.EnqueueAll):
			return m.handleAccountAction("a")
		case key.Matches(msg, k.PlayAll):
			return m.playPlaylist(0)
		case key.Matches(msg, k.Create), key.Matches(msg, k.Rename), key.Matches(msg, k.Delete),
			key.Matches(msg, k.MoveUp), key.Matches(msg, k.MoveDown):
			return m, nil
		}
	}
	switch {
	case key.Matches(msg, k.Up):
		m.playlistCur = max(0, m.playlistCur-1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Down):
		m.playlistCur = min(max(0, m.playlistCount()-1), m.playlistCur+1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Top):
		m.playlistCur, m.playlistTrackCur = 0, 0
	case key.Matches(msg, k.Bottom):
		m.playlistCur = max(0, m.playlistCount()-1)
		m.playlistTrackCur = 0
	case key.Matches(msg, k.Back):
		m.focus = focusResults
	case key.Matches(msg, k.Create):
		return m.openPlaylistName(nil, nameCreate, "name the new playlist")
	case key.Matches(msg, k.Rename):
		if m.playlistCur < len(m.playlists) {
			return m.openName(nameRename, m.playlists[m.playlistCur].Name, "rename "+quote(m.playlists[m.playlistCur].Name))
		}
	case key.Matches(msg, k.MoveUp):
		m.movePlaylist(m.playlistCur, m.playlistCur-1)
	case key.Matches(msg, k.MoveDown):
		m.movePlaylist(m.playlistCur, m.playlistCur+1)
	case key.Matches(msg, k.PlayAll):
		return m.playPlaylist(0)
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
	if m.accountSelected() {
		k.Reload.SetEnabled(true)
		switch {
		case key.Matches(msg, k.Reload):
			m.focus = focusPlaylists
			return m, m.reloadAccount()
		case key.Matches(msg, k.Play):
			return m.handleAccountAction("enter")
		case key.Matches(msg, k.Enqueue):
			return m.handleAccountAction("a")
		case key.Matches(msg, k.PlayAll):
			return m.playPlaylist(m.playlistTrackCur)
		case key.Matches(msg, k.Remove), key.Matches(msg, k.MoveUp), key.Matches(msg, k.MoveDown):
			return m, nil
		}
	}
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
	case key.Matches(msg, k.PlayAll):
		return m.playPlaylist(m.playlistTrackCur)
	case key.Matches(msg, k.MoveUp):
		m.movePlaylistTrack(m.playlistTrackCur, m.playlistTrackCur-1)
	case key.Matches(msg, k.MoveDown):
		m.movePlaylistTrack(m.playlistTrackCur, m.playlistTrackCur+1)
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

// playPlaylist replaces the queue with the selected playlist and plays its
// track at start.
func (m Model) playPlaylist(start int) (tea.Model, tea.Cmd) {
	tracks := m.selectedPlaylistTracks()
	if len(tracks) == 0 {
		if p, ok := m.selectedAccountPlaylist(); ok {
			if _, loaded := m.account.tracks[p.ID]; !loaded {
				m.setStatus("press " + m.keys.playlists.Browse.Help().Key + " to load the playlist first")
				return m, nil
			}
		}
		m.setStatus("playlist is empty")
		return m, nil
	}
	start = min(max(0, start), len(tracks)-1)
	for _, t := range tracks {
		m.tracks[t.URL] = t
	}
	cmd := m.queue.replace(m.nextRequest(), tracks, start)
	m.queueCur = start
	m.setStatus("playing " + quote(m.selectedPlaylistName()) + "…")
	m.syncMPRIS()
	return m, tea.Batch(cmd, m.refreshThumb())
}

func (m Model) selectedPlaylistName() string {
	if p, ok := m.selectedAccountPlaylist(); ok {
		return p.Title
	}
	if m.playlistCur >= 0 && m.playlistCur < len(m.playlists) {
		return m.playlists[m.playlistCur].Name
	}
	return "playlist"
}

// movePlaylist reorders local playlists; the cursor follows the moved one.
func (m *Model) movePlaylist(from, to int) {
	n := len(m.playlists)
	if from == to || from < 0 || from >= n || to < 0 || to >= n {
		return
	}
	if err := m.deps.Library.Move(from, to); err != nil {
		m.setError(err.Error())
		return
	}
	m.playlists = m.deps.Library.Playlists()
	m.playlistCur = to
}

// movePlaylistTrack reorders the selected local playlist; the cursor follows the moved track.
func (m *Model) movePlaylistTrack(from, to int) {
	n := len(m.selectedPlaylistTracks())
	if m.accountSelected() || from == to || from < 0 || from >= n || to < 0 || to >= n {
		return
	}
	if err := m.deps.Library.MoveTrack(m.playlistCur, from, to); err != nil {
		m.setError(err.Error())
		return
	}
	m.playlists = m.deps.Library.Playlists()
	m.playlistTrackCur = to
}

func (m Model) selectedPlaylistTracks() []youtube.Track {
	if p, ok := m.selectedAccountPlaylist(); ok {
		return m.account.tracks[p.ID]
	}
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

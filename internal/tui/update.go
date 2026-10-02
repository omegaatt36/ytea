package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/service"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	g := m.keys.global
	if !key.Matches(msg, m.keys.playlists.Delete) {
		m.deletePlaylistPending = -1
	}
	if key.Matches(msg, g.ForceQuit) {
		return m.quit()
	}
	if m.fullHelp {
		if key.Matches(msg, m.keys.closeHelp) {
			m.fullHelp = false
			return m, nil
		}
		cmd, _ := m.handlePlaybackKey(msg)
		return m, cmd
	}

	inSearch := m.overlay == overlayNone && m.focus == focusSearch
	inFilter := m.inFilter()
	if key.Matches(msg, g.Paste) && !inSearch && !inFilter && m.overlay != overlayName {
		return m, tea.Batch(m.focusSearch(), textinput.Paste)
	}

	switch {
	case inSearch:
		return m.handleSearchKey(msg)
	case inFilter:
		return m.handleFilterKey(msg)
	case m.overlay == overlayName:
		return m.handlePlaylistNameKey(msg)
	case key.Matches(msg, g.Help):
		m.fullHelp = true
		return m, nil
	case m.overlay == overlayDevices:
		return m.handleDeviceKey(msg)
	case m.overlay == overlayInfo:
		return m.handleInfoKey(msg)
	}

	if cmd, ok := m.handlePlaybackKey(msg); ok {
		return m, cmd
	}

	switch {
	case key.Matches(msg, g.Quit):
		return m.quit()
	case key.Matches(msg, g.Search):
		return m, m.focusSearch()
	case key.Matches(msg, g.NextPane):
		m.cycleTab(false)
		return m, nil
	case key.Matches(msg, g.PrevPane):
		m.cycleTab(true)
		return m, nil
	case key.Matches(msg, g.Output):
		return m, loadDevices(m.core, true)
	case key.Matches(msg, g.Info):
		if _, _, ok := m.core.Current(); !ok {
			m.setStatus("nothing playing")
			return m, nil
		}
		return m, loadStream(m.core, true)
	case key.Matches(msg, g.Viz):
		switch {
		case !m.showViz:
			m.showViz = true
			m.vizMode = vizSpectrum
		case m.vizMode == vizSpectrum:
			m.vizMode = vizVU
		default:
			m.showViz = false
			m.vizMode = vizSpectrum
		}
		if m.showViz && m.vizMode == vizSpectrum && !m.levelsWaiting {
			m.levelsWaiting = true
			return m, waitLevels(m.deps.Tap.Levels())
		}
		if m.showViz && m.vizMode == vizVU && !m.vuWaiting {
			if source, ok := m.deps.Tap.(interface{ VU() <-chan [2]float64 }); ok {
				m.vuWaiting = true
				return m, waitVU(source.VU())
			}
		}
		return m, nil
	case key.Matches(msg, g.Radio):
		return m.startRadio()
	case key.Matches(msg, g.GoTo):
		return m.openSeek()
	}

	switch {
	case m.overlay == overlayPicker, m.focus == focusPlaylists, m.focus == focusPlaylistTracks:
		return m.handlePlaylistKey(msg)
	case m.focus == focusQueue:
		return m.handleQueueKey(msg)
	case m.focus == focusHistory:
		return m.handleHistoryKey(msg)
	}
	return m.handleResultKey(msg)
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.search.Submit):
		query := strings.TrimSpace(m.input.Value())
		if query == "" {
			return m, nil
		}
		cmd, link := m.core.Submit(query)
		m.focus = focusResults
		m.input.Blur()
		if link != nil {
			m.focus = focusQueue
			m.setStatus("importing " + quote(link.URL) + "…")
		} else {
			m.setStatus("searching " + quote(query) + "…")
		}
		return m, tea.Batch(m.spinner.Tick, teaCmd(cmd))
	case key.Matches(msg, m.keys.search.Leave):
		m.focus = focusResults
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.filter
	switch {
	case key.Matches(msg, k.Up):
		m.moveResult(m.results.cur - 1)
	case key.Matches(msg, k.Down):
		m.moveResult(m.results.cur + 1)
	case key.Matches(msg, k.Apply):
		m.results.filter.Blur()
	case key.Matches(msg, k.Clear):
		m.clearResultFilter()
	default:
		return m, m.updateResults(msg)
	}
	return m, nil
}

func (m Model) inFilter() bool {
	return m.overlay == overlayNone && m.focus == focusResults && m.results.filter.Focused()
}

func (m Model) handlePlaybackKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	k := m.keys.playback
	switch {
	case key.Matches(msg, k.Pause):
		return teaCmd(m.core.TogglePause()), true
	case key.Matches(msg, k.SeekBack):
		return teaCmd(m.core.Seek(-seekStep)), true
	case key.Matches(msg, k.SeekForward):
		return teaCmd(m.core.Seek(seekStep)), true
	case key.Matches(msg, m.keys.global.SeekPercent):
		return teaCmd(m.core.SeekPercent(float64(10 * int(msg.Code-'0')))), true
	case key.Matches(msg, k.Next):
		return teaCmd(m.core.Next()), true
	case key.Matches(msg, k.Prev):
		return teaCmd(m.core.Prev()), true
	case key.Matches(msg, k.VolumeUp):
		return teaCmd(m.core.AddVolume(volumeStep)), true
	case key.Matches(msg, k.VolumeDown):
		return teaCmd(m.core.AddVolume(-volumeStep)), true
	case key.Matches(msg, k.Normalize):
		return teaCmd(m.core.ToggleNormalize()), true
	case key.Matches(msg, k.Repeat):
		return teaCmd(m.core.CycleRepeat()), true
	}
	return nil, false
}

func (m Model) handleResultKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k, r := m.keys.results, &m.results
	switch {
	case key.Matches(msg, k.Up):
		m.moveResult(r.cur - 1)
	case key.Matches(msg, k.Down):
		// Running off the end of an unfiltered list asks for the next page.
		if r.cur == len(m.resultRows())-1 && r.filter.Value() == "" {
			return m.loadMoreResults()
		}
		m.moveResult(r.cur + 1)
	case key.Matches(msg, k.More):
		return m.loadMoreResults()
	case key.Matches(msg, k.Top):
		m.moveResult(0)
	case key.Matches(msg, k.Bottom):
		m.moveResult(len(m.resultRows()) - 1)
	case key.Matches(msg, k.Filter):
		return m, m.openResultFilter()
	case key.Matches(msg, k.ClearFilter):
		if r.filter.Value() != "" {
			m.clearResultFilter()
		}
	case key.Matches(msg, k.Play):
		if t, ok := m.selectedResult(); ok {
			cmd := teaCmd(m.core.PlayNow(t))
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Enqueue):
		if t, ok := m.selectedResult(); ok {
			cmd := teaCmd(m.core.Enqueue(t))
			m.setStatus("queueing " + quote(t.Title) + "…")
			m.moveResult(r.cur + 1)
			return m, cmd
		}
	case key.Matches(msg, k.Save):
		if t, ok := m.selectedResult(); ok {
			return m.openPlaylistPicker(t)
		}
	}
	return m, nil
}

func (m Model) handleQueueKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.queue
	entries := m.core.Queue.Entries
	i := m.queueCur
	switch {
	case key.Matches(msg, k.Up):
		m.queueCur = max(0, i-1)
	case key.Matches(msg, k.Down):
		m.queueCur = min(max(0, len(entries)-1), i+1)
	case key.Matches(msg, k.Top):
		m.queueCur = 0
	case key.Matches(msg, k.Bottom):
		m.queueCur = max(0, len(entries)-1)
	case key.Matches(msg, k.Jump):
		return m, teaCmd(m.core.PlayIndex(i))
	case key.Matches(msg, k.Save):
		if i >= 0 && i < len(entries) {
			return m.openPlaylistPicker(m.core.EntryTrack(entries[i]))
		}
	case key.Matches(msg, k.SaveQueue):
		if len(entries) == 0 {
			m.setStatus("queue is empty")
			return m, nil
		}
		tracks := m.core.QueueTracks()
		if len(tracks) == 0 {
			m.setError("queue has no saveable tracks")
			return m, nil
		}
		return m.openPlaylistName(tracks, nameCreate, "name the playlist for the current queue")
	case key.Matches(msg, k.Remove):
		if cmd := m.core.RemoveEntry(i); cmd != nil {
			m.queueCur = min(i, max(0, len(m.core.Queue.Entries)-1))
			return m, teaCmd(cmd)
		}
	case key.Matches(msg, k.Clear):
		cmd := teaCmd(m.core.ClearQueue())
		m.queueCur = 0
		m.setStatus("clearing queue…")
		m.syncMPRIS()
		return m, tea.Batch(cmd, m.refreshThumb())
	case key.Matches(msg, k.MoveUp):
		return m, m.moveQueueEntry(i, i-1)
	case key.Matches(msg, k.MoveDown):
		return m, m.moveQueueEntry(i, i+1)
	case key.Matches(msg, k.Shuffle):
		return m.shuffleQueue()
	}
	return m, nil
}

func (m *Model) moveQueueEntry(from, to int) tea.Cmd {
	cmds := m.core.MoveEntry(from, to)
	if cmds == nil {
		return nil
	}
	m.queueCur = to
	teaCmds := make([]tea.Cmd, len(cmds))
	for i, cmd := range cmds {
		teaCmds[i] = teaCmd(cmd)
	}
	return tea.Batch(teaCmds...)
}

// shuffleQueue permutes the tracks after the current one; the cursor follows
// its track.
func (m Model) shuffleQueue() (tea.Model, tea.Cmd) {
	cmd, order, err := m.core.Shuffle(m.rng)
	switch {
	case errors.Is(err, service.ErrEditsPending):
		return m, nil
	case errors.Is(err, service.ErrNothingToShuffle):
		m.setStatus("nothing to shuffle")
		return m, nil
	}
	if cur := slices.Index(order, m.queueCur); cur >= 0 {
		m.queueCur = cur
	}
	return m, teaCmd(cmd)
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if erase := m.thumb.erase(); erase != nil {
		return m, tea.Sequence(erase, tea.Quit)
	}
	return m, tea.Quit
}

func (m *Model) applyEvent(ev service.PlayerEvent) tea.Cmd {
	before := m.core.Queue.Synced
	result := m.core.Update(ev)
	cmds := []tea.Cmd{teaCmds(result.Cmds)}
	if m.core.Queue.Synced != before {
		m.queueCur = min(max(0, m.queueCur), max(0, len(m.core.Queue.Entries)-1))
	}
	if m.core.Queue.Synced != before || result.SwitchesTrack {
		cmds = append(cmds, m.refreshThumb())
	}
	switch ev := ev.(type) {
	case service.PlaybackRestarted:
		m.clearError()
		if m.deps.MPRIS != nil {
			m.deps.MPRIS.Seeked(m.core.Playback.TimePos)
		}
	case service.FileLoaded:
		m.clearError()
		if m.overlay == overlayInfo {
			cmds = append(cmds, loadStream(m.core, false))
		}
	case service.PlaybackFailed:
		m.setError("playback failed: " + ev.Reason)
	case service.DeviceChanged:
		if tap, ok := m.deps.Tap.(interface{ SetAudioDevice(string) error }); ok {
			old := m.spectrumUnavailable
			m.spectrumUnavailable = ""
			if err := tap.SetAudioDevice(m.core.Playback.DeviceName()); err != nil {
				m.spectrumUnavailable = "spectrum: " + err.Error()
				if old != m.spectrumUnavailable {
					m.setError(m.spectrumUnavailable)
				}
			} else if m.status == old {
				m.clearError()
			}
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) applyChange(ev service.PlayerEvent) tea.Cmd {
	return m.applyEvent(ev)
}

func (m *Model) refreshThumb() tea.Cmd {
	_, t, _ := m.core.Current()
	return m.thumb.refresh(t)
}

func loadDevices(c service.Core, open bool) tea.Cmd {
	load := c.LoadDevices()
	return func() tea.Msg { return devicesMsg{load().(service.DevicesLoaded), open} }
}

func loadStream(c service.Core, open bool) tea.Cmd {
	load := c.LoadStream()
	return func() tea.Msg { return streamMsg{load().(service.StreamLoaded), open} }
}

func waitPlayer(ch <-chan service.PlayerEvent, shown time.Duration) tea.Cmd {
	return func() tea.Msg {
		for ev := range ch {
			if pos, ok := ev.(service.PositionChanged); ok && pos.Pos.Round(time.Second) == shown.Round(time.Second) {
				continue
			}
			return playerEventMsg{ev}
		}
		return playerClosedMsg{}
	}
}

func waitLevels(ch <-chan []float64) tea.Cmd {
	return func() tea.Msg {
		return levelsMsg(<-ch)
	}
}

func waitVU(ch <-chan [2]float64) tea.Cmd {
	return func() tea.Msg { return vuMsg(<-ch) }
}

func waitSpectrumError(ch <-chan error) tea.Cmd {
	return func() tea.Msg { return spectrumErrorMsg{err: <-ch} }
}

func actionStatus(done service.ActionDone) string {
	switch done.Action {
	case service.ActionPlayNow:
		return "playing " + quote(done.Title)
	case service.ActionEnqueue:
		return "queued " + quote(done.Title)
	case service.ActionPlayIndex:
		return "playing selected track"
	case service.ActionReorder:
		return "queue shuffled"
	case service.ActionReplace:
		return "playing " + pluralize(done.Tracks, "track")
	case service.ActionClear:
		return "queue cleared"
	case service.ActionRemove, service.ActionMove:
		return "queue updated"
	}
	return ""
}

func quote(s string) string {
	return "“" + service.Sanitize(s) + "”"
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

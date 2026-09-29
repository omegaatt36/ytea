package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
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
		return m, loadDevices(m.deps.Player, true)
	case key.Matches(msg, g.Info):
		if _, _, ok := m.current(); !ok {
			m.setStatus("nothing playing")
			return m, nil
		}
		return m, loadStream(m.deps.Player, true)
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
		requestID := m.nextRequest()
		m.spinnerRequest = requestID
		m.searching = true
		m.focus = focusResults
		m.input.Blur()
		if link, ok := youtube.RefOf(query); ok {
			m.focus = focusQueue
			m.imports = m.imports.add(requestID)
			m.setStatus("importing " + quote(link.URL) + "…")
			return m, tea.Batch(m.spinner.Tick, fetchQueue(m.deps.Searcher, link, requestID))
		}
		m.setStatus("searching " + quote(query) + "…")
		m.searchRequest = requestID
		return m, tea.Batch(m.spinner.Tick, search(m.deps.Searcher, query, 0, requestID))
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
		m.results.moveTo(m.results.cur - 1)
	case key.Matches(msg, k.Down):
		m.results.moveTo(m.results.cur + 1)
	case key.Matches(msg, k.Apply):
		m.results.filter.Blur()
	case key.Matches(msg, k.Clear):
		m.results.clearFilter()
	default:
		return m, m.updateResults(msg)
	}
	return m, nil
}

func (m Model) inFilter() bool {
	return m.overlay == overlayNone && m.focus == focusResults && m.results.filter.Focused()
}

func (m Model) handlePlaybackKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	p, k := m.deps.Player, m.keys.playback
	switch {
	case key.Matches(msg, k.Pause):
		return do(func(ctx context.Context) error { return p.TogglePause(ctx) }), true
	case key.Matches(msg, k.SeekBack):
		return do(func(ctx context.Context) error { return p.Seek(ctx, -seekStep) }), true
	case key.Matches(msg, k.SeekForward):
		return do(func(ctx context.Context) error { return p.Seek(ctx, seekStep) }), true
	case key.Matches(msg, m.keys.global.SeekPercent):
		return m.seekPercent(10 * int(msg.Code-'0')), true
	case key.Matches(msg, k.Next):
		return do(func(ctx context.Context) error { return p.Next(ctx) }), true
	case key.Matches(msg, k.Prev):
		return do(func(ctx context.Context) error { return p.Prev(ctx) }), true
	case key.Matches(msg, k.VolumeUp):
		return do(func(ctx context.Context) error { return p.AddVolume(ctx, volumeStep) }), true
	case key.Matches(msg, k.VolumeDown):
		return do(func(ctx context.Context) error { return p.AddVolume(ctx, -volumeStep) }), true
	case key.Matches(msg, k.Normalize):
		on := !m.player.normalize
		return do(func(ctx context.Context) error { return p.SetNormalize(ctx, on) }), true
	case key.Matches(msg, k.Repeat):
		next := m.player.repeat().Next()
		return do(func(ctx context.Context) error { return p.SetRepeat(ctx, next) }), true
	}
	return nil, false
}

func (m Model) handleResultKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k, r := m.keys.results, &m.results
	switch {
	case key.Matches(msg, k.Up):
		r.moveTo(r.cur - 1)
	case key.Matches(msg, k.Down):
		// Running off the end of an unfiltered list asks for the next page.
		if r.cur == len(r.rows())-1 && r.filter.Value() == "" {
			return m.loadMoreResults()
		}
		r.moveTo(r.cur + 1)
	case key.Matches(msg, k.More):
		return m.loadMoreResults()
	case key.Matches(msg, k.Top):
		r.moveTo(0)
	case key.Matches(msg, k.Bottom):
		r.moveTo(len(r.rows()) - 1)
	case key.Matches(msg, k.Filter):
		return m, r.openFilter()
	case key.Matches(msg, k.ClearFilter):
		if r.filter.Value() != "" {
			r.clearFilter()
		}
	case key.Matches(msg, k.Play):
		if t, ok := r.selected(); ok {
			cmd := m.queue.playNow(m.nextRequest(), t)
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Enqueue):
		if t, ok := r.selected(); ok {
			cmd := m.queue.enqueue(m.nextRequest(), t)
			m.setStatus("queueing " + quote(t.Title) + "…")
			r.moveTo(r.cur + 1)
			return m, cmd
		}
	case key.Matches(msg, k.Save):
		if t, ok := r.selected(); ok {
			return m.openPlaylistPicker(t)
		}
	}
	return m, nil
}

func (m Model) handleQueueKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.queue
	entries := m.queue.entries
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
		if !m.queue.insertPending && i >= 0 && i < len(entries) {
			return m, m.queue.playIndex(m.nextRequest(), i)
		}
	case key.Matches(msg, k.Save):
		if i >= 0 && i < len(entries) {
			return m.openPlaylistPicker(m.entryTrack(entries[i]))
		}
	case key.Matches(msg, k.SaveQueue):
		if len(entries) == 0 {
			m.setStatus("queue is empty")
			return m, nil
		}
		tracks := make([]youtube.Track, 0, len(entries))
		seen := make(map[string]bool, len(entries))
		for _, e := range entries {
			if e.Filename == "" || seen[e.Filename] {
				continue
			}
			seen[e.Filename] = true
			tracks = append(tracks, m.entryTrack(e))
		}
		if len(tracks) == 0 {
			m.setError("queue has no saveable tracks")
			return m, nil
		}
		return m.openPlaylistName(tracks, nameCreate, "name the playlist for the current queue")
	case key.Matches(msg, k.Remove):
		if !m.queue.insertPending && i >= 0 && i < len(entries) {
			cmd := m.queue.remove(m.nextRequest(), i)
			m.queueCur = min(i, max(0, len(m.queue.entries)-1))
			return m, cmd
		}
	case key.Matches(msg, k.Clear):
		// Imports still resolving take a slot once resolved, so they land after the clear.
		cmd := m.queue.clear(m.nextRequest())
		m.queueCur = 0
		m.player.stop()
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

// moveQueueEntry walks the entry at from to to one slot at a time, since the
// projected queue only swaps neighbours; the cursor follows it.
func (m *Model) moveQueueEntry(from, to int) tea.Cmd {
	n := len(m.queue.entries)
	if m.queue.insertPending || from == to || from < 0 || from >= n || to < 0 || to >= n {
		return nil
	}
	step := 1
	if to < from {
		step = -1
	}
	var cmds []tea.Cmd
	for i := from; i != to; i += step {
		cmds = append(cmds, m.queue.move(m.nextRequest(), i, i+step))
	}
	m.queueCur = to
	return tea.Batch(cmds...)
}

// shuffleQueue permutes the tracks after the current one; the cursor follows
// its track.
func (m Model) shuffleQueue() (tea.Model, tea.Cmd) {
	// The current index is only trustworthy once earlier edits are read back from mpv.
	if m.queue.projection != nil {
		return m, nil
	}
	n := len(m.queue.entries)
	after, current := -1, ""
	if e, _, ok := m.current(); ok {
		after, current = m.queue.pos, e.Filename
	}
	if n-after-1 < 2 {
		m.setStatus("nothing to shuffle")
		return m, nil
	}
	order := mpv.TailShuffle(n, after, m.rng)
	cmd := m.queue.reorder(m.nextRequest(), after, current, order)
	if cur := slices.Index(order, m.queueCur); cur >= 0 {
		m.queueCur = cur
	}
	return m, cmd
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if erase := m.thumb.erase(); erase != nil {
		return m, tea.Sequence(erase, tea.Quit)
	}
	return m, tea.Quit
}

func (m *Model) applyEvent(ev mpv.Event) tea.Cmd {
	switch ev.Name {
	case "property-change":
		return m.applyProperty(ev)
	case "playback-restart":
		m.clearError()
		// MPRIS clients resync on Seeked, which mpv fires after every seek and track start.
		if m.deps.MPRIS != nil {
			m.deps.MPRIS.Seeked(m.player.timePos)
		}
	case "file-loaded":
		m.clearError()
		if m.overlay == overlayInfo {
			return loadStream(m.deps.Player, false)
		}
	case "end-file":
		if ev.Reason == "error" {
			m.setError("playback failed: " + ev.FileError)
		}
	}
	return nil
}

// applyProperty delivers a property change to the queue first, so the root's
// reconciliation below sees the updated playlist position.
func (m *Model) applyProperty(ev mpv.Event) tea.Cmd {
	_, queueCmd := m.updateQueue(mpvEventMsg(ev))
	switchesTrack := m.player.apply(ev)
	if ev.Prop == mpv.PropAudioDevice {
		if tap, ok := m.deps.Tap.(interface{ SetAudioDevice(string) error }); ok {
			old := m.spectrumUnavailable
			m.spectrumUnavailable = ""
			if err := tap.SetAudioDevice(m.player.deviceName()); err != nil {
				m.spectrumUnavailable = "spectrum: " + err.Error()
				if old != m.spectrumUnavailable {
					m.setError(m.spectrumUnavailable)
				}
			} else if m.status == old {
				m.clearError()
			}
		}
	}
	if !switchesTrack {
		return queueCmd
	}
	return tea.Batch(queueCmd, m.refreshThumb())
}

func (m *Model) refreshThumb() tea.Cmd {
	_, t, _ := m.current()
	return m.thumb.refresh(t)
}

func do(fn func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *Model) nextRequest() uint64 {
	m.requestID++
	m.activeRequest = m.requestID
	return m.requestID
}

func loadDevices(p Player, open bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		devices, err := p.AudioDevices(ctx)
		return devicesMsg{devices: devices, open: open, err: err}
	}
}

func loadStream(p Player, open bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		info, err := p.StreamInfo(ctx)
		return streamMsg{info: info, open: open, err: err}
	}
}

func waitMPV(ch <-chan mpv.Event, shown time.Duration) tea.Cmd {
	return func() tea.Msg {
		for ev := range ch {
			if ev.Name == "property-change" && ev.Prop == mpv.PropTimePos && string(ev.Data) != "null" &&
				seconds(mpv.Decode[float64](ev.Data)).Round(time.Second) == shown.Round(time.Second) {
				continue
			}
			return mpvEventMsg(ev)
		}
		return mpvClosedMsg{}
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

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func quote(s string) string {
	return "“" + s + "”"
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

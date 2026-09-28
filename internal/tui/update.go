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
		m.showViz = !m.showViz
		return m, nil
	case key.Matches(msg, g.Radio):
		return m.startRadio()
	}

	switch {
	case m.overlay == overlayPicker, m.focus == focusPlaylists, m.focus == focusPlaylistTracks:
		return m.handlePlaylistKey(msg)
	case m.focus == focusQueue:
		return m.handleQueueKey(msg)
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
			m.imports = append(m.imports, pendingImport{requestID: requestID})
			m.setStatus("importing " + quote(link.URL) + "…")
			return m, tea.Batch(m.spinner.Tick, fetchQueue(m.deps.Searcher, link, requestID))
		}
		m.setStatus("searching " + quote(query) + "…")
		m.searchRequest = requestID
		return m, tea.Batch(m.spinner.Tick, search(m.deps.Searcher, query, requestID))
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
		m.resultCur = max(0, m.resultCur-1)
	case key.Matches(msg, k.Down):
		m.resultCur = max(0, min(len(m.resultRows())-1, m.resultCur+1))
	case key.Matches(msg, k.Apply):
		m.filterInput.Blur()
	case key.Matches(msg, k.Clear):
		m.clearFilter()
	default:
		return m, m.updateFilter(msg)
	}
	return m, nil
}

func (m *Model) updateFilter(msg tea.Msg) tea.Cmd {
	before := m.filterInput.Value()
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	if m.filterInput.Value() != before {
		m.resultCur = 0
	}
	return cmd
}

// clearFilter keeps the selected track selected in the full list.
func (m *Model) clearFilter() {
	if rows := m.resultRows(); m.resultCur >= 0 && m.resultCur < len(rows) {
		m.resultCur = rows[m.resultCur].index
	}
	m.filterInput.Reset()
	m.filterInput.Blur()
}

func (m Model) inFilter() bool {
	return m.overlay == overlayNone && m.focus == focusResults && m.filterInput.Focused()
}

// resultRows are the visible Results rows; resultCur indexes them.
func (m Model) resultRows() []filterMatch {
	return filterResults(m.results, m.filterInput.Value())
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
	case key.Matches(msg, k.Next):
		return do(func(ctx context.Context) error { return p.Next(ctx) }), true
	case key.Matches(msg, k.Prev):
		return do(func(ctx context.Context) error { return p.Prev(ctx) }), true
	case key.Matches(msg, k.VolumeUp):
		return do(func(ctx context.Context) error { return p.AddVolume(ctx, volumeStep) }), true
	case key.Matches(msg, k.VolumeDown):
		return do(func(ctx context.Context) error { return p.AddVolume(ctx, -volumeStep) }), true
	case key.Matches(msg, k.Normalize):
		on := !m.normalize
		return do(func(ctx context.Context) error { return p.SetNormalize(ctx, on) }), true
	case key.Matches(msg, k.Repeat):
		next := m.repeat().Next()
		return do(func(ctx context.Context) error { return p.SetRepeat(ctx, next) }), true
	}
	return nil, false
}

func (m Model) handleResultKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.results
	n := len(m.resultRows())
	switch {
	case key.Matches(msg, k.Up):
		m.resultCur = max(0, m.resultCur-1)
	case key.Matches(msg, k.Down):
		m.resultCur = max(0, min(n-1, m.resultCur+1))
	case key.Matches(msg, k.Top):
		m.resultCur = 0
	case key.Matches(msg, k.Bottom):
		m.resultCur = max(0, n-1)
	case key.Matches(msg, k.Filter):
		if len(m.results) > 0 {
			return m, m.filterInput.Focus()
		}
	case key.Matches(msg, k.ClearFilter):
		if m.filterInput.Value() != "" {
			m.clearFilter()
		}
	case key.Matches(msg, k.Play):
		if t, ok := m.selectedResult(); ok {
			cmd := m.queue.playNow(m.nextRequest(), t)
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case key.Matches(msg, k.Enqueue):
		if t, ok := m.selectedResult(); ok {
			cmd := m.queue.enqueue(m.nextRequest(), t)
			m.setStatus("queueing " + quote(t.Title) + "…")
			m.resultCur = max(0, min(n-1, m.resultCur+1))
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
			e := entries[i]
			t := m.tracks[e.Filename]
			t.URL = e.Filename
			if t.Title == "" {
				t.Title = e.Title
			}
			return m.openPlaylistPicker(t)
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
			t := m.tracks[e.Filename]
			t.URL = e.Filename
			if t.Title == "" {
				t.Title = e.Title
			}
			tracks = append(tracks, t)
		}
		if len(tracks) == 0 {
			m.setError("queue has no saveable tracks")
			return m, nil
		}
		return m.openPlaylistName(tracks, false, "name the playlist for the current queue")
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
		m.idle = true
		m.timePos, m.duration = 0, 0
		m.setStatus("clearing queue…")
		m.syncMPRIS()
		return m, tea.Batch(cmd, m.refreshThumb())
	case key.Matches(msg, k.MoveUp):
		if !m.queue.insertPending && i > 0 && i < len(entries) {
			cmd := m.queue.move(m.nextRequest(), i, i-1)
			m.queueCur--
			return m, cmd
		}
	case key.Matches(msg, k.MoveDown):
		if !m.queue.insertPending && i >= 0 && i < len(entries)-1 {
			cmd := m.queue.move(m.nextRequest(), i, i+1)
			m.queueCur++
			return m, cmd
		}
	case key.Matches(msg, k.Shuffle):
		// The current index is only trustworthy once earlier edits are read back from mpv.
		if m.queue.projection != nil {
			return m, nil
		}
		after, current := -1, ""
		if e, _, ok := m.current(); ok {
			after, current = m.queue.pos, e.Filename
		}
		if len(entries)-after-1 < 2 {
			m.setStatus("nothing to shuffle")
			return m, nil
		}
		order := mpv.TailShuffle(len(entries), after, m.rng)
		cmd := m.queue.reorder(m.nextRequest(), after, current, order)
		if cur := slices.Index(order, i); cur >= 0 {
			m.queueCur = cur
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if erase := m.thumb.erase(); erase != nil {
		return m, tea.Sequence(erase, tea.Quit)
	}
	return m, tea.Quit
}

func (m Model) selectedResult() (youtube.Track, bool) {
	rows := m.resultRows()
	if m.resultCur < 0 || m.resultCur >= len(rows) {
		return youtube.Track{}, false
	}
	return m.results[rows[m.resultCur].index], true
}

func (m *Model) applyEvent(ev mpv.Event) tea.Cmd {
	switch ev.Name {
	case "property-change":
		return m.applyProperty(ev)
	case "playback-restart":
		// MPRIS clients resync on Seeked, which mpv fires after every seek and track start.
		if m.deps.MPRIS != nil {
			m.deps.MPRIS.Seeked(m.timePos)
		}
	case "file-loaded":
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
	var cmd tea.Cmd
	switch ev.Prop {
	case mpv.PropTimePos:
		m.timePos = seconds(mpv.Decode[float64](ev.Data))
	case mpv.PropDuration:
		m.duration = seconds(mpv.Decode[float64](ev.Data))
	case mpv.PropPause:
		m.paused = mpv.Decode[bool](ev.Data)
	case mpv.PropIdle:
		m.idle = mpv.Decode[bool](ev.Data)
		if m.idle {
			m.timePos, m.duration = 0, 0
		}
		cmd = m.refreshThumb()
	case mpv.PropVolume:
		m.volume = mpv.Decode[float64](ev.Data)
	case mpv.PropCodec:
		m.codec = mpv.Decode[string](ev.Data)
	case mpv.PropAudioParams:
		m.params = mpv.Decode[mpv.AudioParams](ev.Data)
	case mpv.PropAudioDevice:
		m.device = mpv.Decode[string](ev.Data)
	case mpv.PropAF:
		filters := mpv.Decode[[]mpv.Filter](ev.Data)
		m.normalize = false
		for _, f := range filters {
			if f.Label == mpv.NormalizeLabel {
				m.normalize = true
			}
		}
	case mpv.PropLoopPlaylist:
		m.loopPlaylist = mpv.LoopOn(ev.Data)
	case mpv.PropLoopFile:
		m.loopFile = mpv.LoopOn(ev.Data)
	case mpv.PropPlaylistPos:
		m.timePos = 0
		cmd = m.refreshThumb()
	}
	return tea.Batch(queueCmd, cmd)
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

func search(s Searcher, query string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		tracks, err := s.Search(ctx, query, searchLimit)
		return searchDoneMsg{requestID: requestID, query: query, tracks: tracks, err: err}
	}
}

func (m Model) startRadio() (tea.Model, tea.Cmd) {
	_, t, ok := m.current()
	if !ok || t.ID == "" {
		m.setStatus("nothing playing to seed a radio from")
		return m, nil
	}
	requestID := m.nextRequest()
	m.imports = append(m.imports, pendingImport{requestID: requestID})
	m.spinnerRequest = requestID
	m.searching = true
	m.setStatus("fetching radio…")
	return m, tea.Batch(m.spinner.Tick, fetchRadio(m.deps.Searcher, t.ID, requestID))
}

type pendingImport struct {
	requestID uint64
	lookup    *lookupDoneMsg // nil while the lookup runs
}

// Slots are reserved only once a lookup resolves, so queue edits made during
// a lookup neither wait on the network nor act on its tracks.
func (m *Model) resolveImport(msg lookupDoneMsg) tea.Cmd {
	i := slices.IndexFunc(m.imports, func(p pendingImport) bool { return p.requestID == msg.requestID })
	if i < 0 {
		return nil
	}
	m.imports = slices.Clone(m.imports)
	m.imports[i].lookup = &msg
	var cmds []tea.Cmd
	for len(m.imports) > 0 && m.imports[0].lookup != nil {
		cmds = append(cmds, queueImport(func(tracks []youtube.Track) error {
			return appendTracks(m.deps.Player, tracks)
		}, *m.imports[0].lookup, m.queue.reserve()))
		m.imports = m.imports[1:]
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

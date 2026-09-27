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
	pressed := msg.String()
	if pressed != "D" {
		m.deletePlaylistPending = -1
	}
	if pressed == "ctrl+c" {
		return m.quit()
	}

	inSearch := m.overlay == overlayNone && m.focus == focusSearch
	if key.Matches(msg, pasteKeys) && !inSearch && m.overlay != overlayName {
		return m, tea.Batch(m.focusSearch(), textinput.Paste)
	}

	switch {
	case inSearch:
		return m.handleSearchKey(msg)
	case m.overlay == overlayName:
		return m.handlePlaylistNameKey(msg)
	case m.overlay == overlayDevices:
		return m.handleDeviceKey(pressed)
	case m.overlay == overlayInfo:
		return m.handleInfoKey(pressed)
	}

	if cmd, ok := m.handlePlaybackKey(pressed); ok {
		return m, cmd
	}

	switch pressed {
	case "q":
		return m.quit()
	case "/":
		return m, m.focusSearch()
	case "tab", "shift+tab":
		m.cycleTab(pressed == "shift+tab")
		return m, nil
	case "o":
		return m, loadDevices(m.deps.Player, true)
	case "i":
		if _, _, ok := m.current(); !ok {
			m.setStatus("nothing playing")
			return m, nil
		}
		return m, loadStream(m.deps.Player, true)
	case "v":
		if m.deps.Tap != nil {
			m.showViz = !m.showViz
		}
		return m, nil
	case "r":
		return m.startRadio()
	}

	switch {
	case m.overlay == overlayPicker, m.focus == focusPlaylists, m.focus == focusPlaylistTracks:
		return m.handlePlaylistKey(pressed)
	case m.focus == focusQueue:
		return m.handleQueueKey(pressed)
	}
	return m.handleResultKey(pressed)
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
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
			m.imports = append(m.imports, pendingImport{requestID: requestID})
			m.setStatus("importing " + quote(link.URL) + "…")
			return m, tea.Batch(m.spinner.Tick, fetchQueue(m.deps.Searcher, link, requestID))
		}
		m.setStatus("searching " + quote(query) + "…")
		m.searchRequest = requestID
		return m, tea.Batch(m.spinner.Tick, search(m.deps.Searcher, query, requestID))
	case "esc":
		m.focus = focusResults
		m.input.Blur()
		return m, nil
	case "tab":
		m.focus = focusResults
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handlePlaybackKey(key string) (tea.Cmd, bool) {
	p := m.deps.Player
	switch key {
	case "space":
		return do(func(ctx context.Context) error { return p.TogglePause(ctx) }), true
	case "left":
		return do(func(ctx context.Context) error { return p.Seek(ctx, -seekStep) }), true
	case "right":
		return do(func(ctx context.Context) error { return p.Seek(ctx, seekStep) }), true
	case "n", ">":
		return do(func(ctx context.Context) error { return p.Next(ctx) }), true
	case "p", "<":
		return do(func(ctx context.Context) error { return p.Prev(ctx) }), true
	case "+", "=":
		return do(func(ctx context.Context) error { return p.AddVolume(ctx, volumeStep) }), true
	case "-":
		return do(func(ctx context.Context) error { return p.AddVolume(ctx, -volumeStep) }), true
	case "N":
		on := !m.normalize
		return do(func(ctx context.Context) error { return p.SetNormalize(ctx, on) }), true
	}
	return nil, false
}

func (m Model) handleResultKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.resultCur = max(0, m.resultCur-1)
	case "down", "j":
		m.resultCur = min(len(m.results)-1, m.resultCur+1)
	case "g", "home":
		m.resultCur = 0
	case "G", "end":
		m.resultCur = max(0, len(m.results)-1)
	case "enter":
		if t, ok := m.selectedResult(); ok {
			cmd := m.queue.playNow(m.nextRequest(), t)
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, cmd
		}
	case "a":
		if t, ok := m.selectedResult(); ok {
			cmd := m.queue.enqueue(m.nextRequest(), t)
			m.setStatus("queueing " + quote(t.Title) + "…")
			m.resultCur = min(len(m.results)-1, m.resultCur+1)
			return m, cmd
		}
	case "s":
		if t, ok := m.selectedResult(); ok {
			return m.openPlaylistPicker(t)
		}
	}
	return m, nil
}

func (m Model) handleQueueKey(key string) (tea.Model, tea.Cmd) {
	entries := m.queue.entries
	i := m.queueCur
	switch key {
	case "up", "k":
		m.queueCur = max(0, i-1)
	case "down", "j":
		m.queueCur = min(max(0, len(entries)-1), i+1)
	case "g", "home":
		m.queueCur = 0
	case "G", "end":
		m.queueCur = max(0, len(entries)-1)
	case "enter":
		if !m.queue.insertPending && i >= 0 && i < len(entries) {
			return m, m.queue.playIndex(m.nextRequest(), i)
		}
	case "s":
		if i >= 0 && i < len(entries) {
			e := entries[i]
			t := m.tracks[e.Filename]
			t.URL = e.Filename
			if t.Title == "" {
				t.Title = e.Title
			}
			return m.openPlaylistPicker(t)
		}
	case "S":
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
	case "d", "x", "delete":
		if !m.queue.insertPending && i >= 0 && i < len(entries) {
			cmd := m.queue.remove(m.nextRequest(), i)
			m.queueCur = min(i, max(0, len(m.queue.entries)-1))
			return m, cmd
		}
	case "C":
		// Imports still resolving take a slot once resolved, so they land after the clear.
		cmd := m.queue.clear(m.nextRequest())
		m.queueCur = 0
		m.idle = true
		m.timePos, m.duration = 0, 0
		m.setStatus("clearing queue…")
		m.syncMPRIS()
		return m, tea.Batch(cmd, m.refreshThumb())
	case "K", "shift+up":
		if !m.queue.insertPending && i > 0 && i < len(entries) {
			cmd := m.queue.move(m.nextRequest(), i, i-1)
			m.queueCur--
			return m, cmd
		}
	case "J", "shift+down":
		if !m.queue.insertPending && i >= 0 && i < len(entries)-1 {
			cmd := m.queue.move(m.nextRequest(), i, i+1)
			m.queueCur++
			return m, cmd
		}
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
	if m.resultCur < 0 || m.resultCur >= len(m.results) {
		return youtube.Track{}, false
	}
	return m.results[m.resultCur], true
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

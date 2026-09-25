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
	"github.com/omegaatt36/ytea/internal/thumbnail"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pressed := msg.String()
	if pressed == "ctrl+c" {
		return m.quit()
	}

	if key.Matches(msg, pasteKeys) && m.focus != focusSearch {
		// Pasting from a list pane goes to the search box, like terminal paste does.
		return m, tea.Batch(m.focusSearch(), textinput.Paste)
	}

	switch m.focus {
	case focusSearch:
		return m.handleSearchKey(msg)
	case focusDevices:
		return m.handleDeviceKey(pressed)
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
		if m.focus == focusResults {
			m.focus = focusQueue
		} else {
			m.focus = focusResults
		}
		return m, nil
	case "o":
		return m, loadDevices(m.deps.Player, true)
	case "v":
		if m.deps.Tap != nil {
			m.showViz = !m.showViz
		}
		return m, nil
	case "r":
		return m.startRadio()
	}

	if m.focus == focusQueue {
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
			task := m.reserveQueue()
			m.setStatus("importing " + quote(link.URL) + "…")
			return m, tea.Batch(m.spinner.Tick, fetchQueue(m.deps.Searcher, func(tracks []youtube.Track) error {
				return appendTracks(m.deps.Player, tracks)
			}, link, requestID, task))
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

// handlePlaybackKey handles keys that work regardless of which pane is focused.
func (m Model) handlePlaybackKey(key string) (tea.Cmd, bool) {
	p := m.deps.Player
	switch key {
	case "space":
		return do(p.TogglePause), true
	case "left":
		return do(func(ctx context.Context) error { return p.Seek(ctx, -seekStep) }), true
	case "right":
		return do(func(ctx context.Context) error { return p.Seek(ctx, seekStep) }), true
	case "n", ">":
		return do(p.Next), true
	case "p", "<":
		return do(p.Prev), true
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
			requestID, task := m.nextRequest(), m.reserveQueue()
			// insert-next-play shifts playlist indices. Keep index-based queue
			// actions paused until the insertion has been read back from mpv.
			m.expectQueue()
			m.queueInsertPending = true
			m.setStatus("playing " + quote(t.Title) + "…")
			return m, projectedQueueAction(task, requestID, "playing "+quote(t.Title), func(ctx context.Context) error { return m.deps.Player.PlayNow(ctx, t.URL) })
		}
	case "a":
		if t, ok := m.selectedResult(); ok {
			requestID, task := m.nextRequest(), m.reserveQueue()
			m.setStatus("queueing " + quote(t.Title) + "…")
			m.resultCur = min(len(m.results)-1, m.resultCur+1)
			return m, queueAction(task, requestID, "queued "+quote(t.Title), func(ctx context.Context) error { return m.deps.Player.Append(ctx, t.URL) })
		}
	}
	return m, nil
}

func (m Model) handleQueueKey(key string) (tea.Model, tea.Cmd) {
	p := m.deps.Player
	i := m.queueCur
	switch key {
	case "up", "k":
		m.queueCur = max(0, i-1)
	case "down", "j":
		m.queueCur = min(max(0, len(m.queue)-1), i+1)
	case "g", "home":
		m.queueCur = 0
	case "G", "end":
		m.queueCur = max(0, len(m.queue)-1)
	case "enter":
		if !m.queueInsertPending && i >= 0 && i < len(m.queue) {
			return m, queueAction(m.reserveQueue(), m.nextRequest(), "playing selected track", func(ctx context.Context) error { return p.PlayIndex(ctx, i) })
		}
	case "d", "x", "delete":
		if !m.queueInsertPending && i >= 0 && i < len(m.queue) {
			cmd := m.queueWrite(func(ctx context.Context) error { return p.Remove(ctx, i) })
			m.queue = slices.Delete(slices.Clone(m.queue), i, i+1)
			m.queueCur = min(i, max(0, len(m.queue)-1))
			m.expectQueue()
			return m, cmd
		}
	case "C":
		// Clear follows any in-flight import or edit, even when the displayed
		// queue is already empty. The authoritative refresh settles its result.
		cmd := projectedQueueAction(m.reserveQueue(), m.nextRequest(), "queue cleared", p.Stop)
		m.queue = nil
		m.queueCur = 0
		m.pos = -1
		m.idle = true
		m.timePos, m.duration = 0, 0
		m.expectQueue()
		m.setStatus("clearing queue…")
		m.syncMPRIS()
		return m, tea.Batch(cmd, m.refreshThumb())
	case "K", "shift+up":
		if !m.queueInsertPending && i > 0 && i < len(m.queue) {
			cmd := m.queueWrite(func(ctx context.Context) error { return p.Move(ctx, i, i-1) })
			m.queue = slices.Clone(m.queue)
			m.queue[i], m.queue[i-1] = m.queue[i-1], m.queue[i]
			m.queueCur--
			m.expectQueue()
			return m, cmd
		}
	case "J", "shift+down":
		if !m.queueInsertPending && i >= 0 && i < len(m.queue)-1 {
			cmd := m.queueWrite(func(ctx context.Context) error { return p.Move(ctx, i, i+1) })
			m.queue = slices.Clone(m.queue)
			m.queue[i], m.queue[i+1] = m.queue[i+1], m.queue[i]
			m.queueCur++
			m.expectQueue()
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) handleDeviceKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.deviceCur = max(0, m.deviceCur-1)
	case "down", "j":
		m.deviceCur = min(len(m.devices)-1, m.deviceCur+1)
	case "esc", "o", "q":
		m.focus = focusResults
	case "enter":
		m.focus = focusResults
		if m.deviceCur < len(m.devices) {
			d := m.devices[m.deviceCur]
			m.setStatus("output → " + d.Label())
			// Routing mpv itself (rather than the system mixer) makes the choice
			// stick across tracks and survive stream re-creation.
			return m, do(func(ctx context.Context) error {
				return m.deps.Player.SetAudioDevice(ctx, d.Name)
			})
		}
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.thumbID != 0 {
		return m, tea.Sequence(tea.Raw(thumbnail.Delete(m.thumbID)), tea.Quit)
	}
	return m, tea.Quit
}

func (m Model) selectedResult() (youtube.Track, bool) {
	if m.resultCur < 0 || m.resultCur >= len(m.results) {
		return youtube.Track{}, false
	}
	return m.results[m.resultCur], true
}

// applyEvent folds an mpv event into the model.
func (m *Model) applyEvent(ev mpv.Event) tea.Cmd {
	switch ev.Name {
	case "property-change":
		return m.applyProperty(ev)
	case "playback-restart":
		// Fired after every seek and track start; MPRIS clients resync on Seeked.
		if m.deps.MPRIS != nil {
			m.deps.MPRIS.Seeked(m.timePos)
		}
	case "end-file":
		if ev.Reason == "error" {
			m.setError("playback failed: " + ev.FileError)
		}
	}
	return nil
}

func (m *Model) applyProperty(ev mpv.Event) tea.Cmd {
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
		return m.refreshThumb()
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
	case mpv.PropPlaylist:
		queue := mpv.Decode[[]mpv.PlaylistEntry](ev.Data)
		if m.queueProjection != nil || m.queueAuthoritative {
			// Event delivery can lag behind queued commands, and an older
			// playlist can equal the final projection after opposing edits.
			m.queueEventVersion++
			return m.scheduleQueueDebounce()
		}
		m.queue = queue
		m.queueCur = min(max(0, m.queueCur), max(0, len(m.queue)-1))
		return m.refreshThumb()
	case mpv.PropPlaylistPos:
		// mpv reports -1 when nothing is selected, but null (unavailable) would
		// decode to 0 and wrongly mark the first entry as playing.
		m.pos = -1
		if string(ev.Data) != "null" {
			m.pos = mpv.Decode[int](ev.Data)
		}
		m.timePos = 0
		return m.refreshThumb()
	}
	return nil
}

// refreshThumb fetches the current track's thumbnail when it changed.
// It waits for the graphics probe so it knows which rendering to produce.
func (m *Model) refreshThumb() tea.Cmd {
	if !m.deps.Thumbnails || m.graphics == graphicsUnknown {
		return nil
	}
	_, t, ok := m.current()
	if !ok || t.ID == "" {
		m.thumbArt, m.thumbVideo, m.placed = "", "", false
		if m.thumbID != 0 {
			id := m.thumbID
			m.thumbID = 0
			return tea.Raw(thumbnail.Delete(id))
		}
		return nil
	}
	if t.ID == m.thumbVideo {
		return nil
	}
	m.thumbVideo = t.ID
	client, url := m.deps.HTTP, t.ThumbnailURL()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		img, err := thumbnail.Fetch(ctx, client, url)
		return thumbMsg{videoID: t.ID, img: img, err: err}
	}
}

func (m Model) applyThumb(msg thumbMsg) (tea.Model, tea.Cmd) {
	// A slow fetch may land after the user already skipped ahead.
	if msg.videoID != m.thumbVideo {
		return m, nil
	}
	if msg.err != nil {
		m.setError(msg.err.Error())
		return m, nil
	}

	if m.graphics == graphicsNone {
		m.thumbArt = thumbnail.HalfBlocks(msg.img, thumbCols, thumbRows)
		return m, nil
	}

	prev := m.thumbID
	m.thumbID = nextThumbID(prev)
	var (
		seq string
		err error
	)
	if m.graphics == graphicsPlaceholder {
		seq, err = thumbnail.TransmitVirtual(msg.img, m.thumbID, thumbCols, thumbRows)
	} else {
		// Direct mode: upload only; syncPlacement puts it once the layout is known.
		seq, err = thumbnail.Transmit(msg.img, m.thumbID)
		m.placed = false
	}
	if err != nil {
		m.thumbID = prev
		m.setError(err.Error())
		return m, nil
	}
	if prev != 0 {
		seq += thumbnail.Delete(prev)
	}
	return m, tea.Raw(seq)
}

// nextThumbID alternates ids within [16, 255]: a fresh id per track means the
// old placeholders never briefly show the new image at the wrong size.
func nextThumbID(prev int) int {
	if prev < 16 || prev >= 255 {
		return 16
	}
	return prev + 1
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

// queueTask links writes in the order their keys were pressed, regardless of
// how long their network lookups or Bubble Tea command scheduling take.
type queueTask struct {
	previous <-chan struct{}
	done     chan struct{}
}

func (m *Model) reserveQueue() queueTask {
	task := queueTask{previous: m.queueTail, done: make(chan struct{})}
	m.queueTail = task.done
	return task
}

func (task queueTask) run(fn func() error) error {
	if task.previous != nil {
		<-task.previous
	}
	defer close(task.done)
	return fn()
}

func queueAction(task queueTask, requestID uint64, status string, fn func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		err := task.run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			return fn(ctx)
		})
		return queueActionDoneMsg{requestID: requestID, status: status, err: err}
	}
}

func (m *Model) queueWrite(fn func(context.Context) error) tea.Cmd {
	return projectedQueueAction(m.reserveQueue(), m.nextRequest(), "queue updated", fn)
}

func projectedQueueAction(task queueTask, requestID uint64, status string, fn func(context.Context) error) tea.Cmd {
	cmd := queueAction(task, requestID, status, fn)
	return func() tea.Msg {
		msg := cmd().(queueActionDoneMsg)
		msg.projected = true
		return msg
	}
}

type queueProjection struct{}

func (m *Model) expectQueue() {
	m.queueProjection = &queueProjection{}
	m.queueEditsPending++
	m.queueRevision++
}

func (m *Model) scheduleQueueRefresh() tea.Cmd {
	if (m.queueProjection == nil && !m.queueAuthoritative) || m.queueEditsPending > 0 || m.queueRefreshPending {
		return nil
	}
	m.queueRefreshPending = true
	return refreshQueue(m.reserveQueue(), m.deps.Player, m.queueRevision, m.queueEventVersion)
}

const queueDebounceDelay = 50 * time.Millisecond

func (m *Model) scheduleQueueDebounce() tea.Cmd {
	if m.queueDebouncePending {
		return nil
	}
	m.queueDebouncePending = true
	return queueDebounce(m.queueEventVersion)
}

func queueDebounce(version uint64) tea.Cmd {
	return tea.Tick(queueDebounceDelay, func(time.Time) tea.Msg { return queueDebounceMsg{version: version} })
}

func refreshQueue(task queueTask, p *mpv.Player, revision, eventVersion uint64) tea.Cmd {
	return func() tea.Msg {
		var entries []mpv.PlaylistEntry
		pos := -1
		err := task.run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			var err error
			entries, pos, err = p.Playlist(ctx)
			return err
		})
		return queueRefreshMsg{revision: revision, eventVersion: eventVersion, entries: entries, pos: pos, err: err}
	}
}

func search(s Searcher, query string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		tracks, err := s.Search(ctx, query, searchLimit)
		return searchDoneMsg{requestID: requestID, query: query, tracks: tracks, err: err}
	}
}

// startRadio queues YouTube's mix for the track playing now.
func (m Model) startRadio() (tea.Model, tea.Cmd) {
	_, t, ok := m.current()
	if !ok || t.ID == "" {
		m.setStatus("nothing playing to seed a radio from")
		return m, nil
	}
	requestID, task := m.nextRequest(), m.reserveQueue()
	m.spinnerRequest = requestID
	m.searching = true
	m.setStatus("fetching radio…")
	return m, tea.Batch(m.spinner.Tick, fetchRadio(m.deps.Searcher, func(tracks []youtube.Track) error {
		return appendTracks(m.deps.Player, tracks)
	}, t.ID, requestID, task))
}

// fetchQueue resolves the tracks behind a pasted link so update can queue them.
func fetchQueue(s Searcher, appendQueue func([]youtube.Track) error, link youtube.Link, requestID uint64, task queueTask) tea.Cmd {
	return func() tea.Msg {
		limit := 0
		if link.Mix {
			// A mix queues like a radio, not in full.
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
		err = task.run(func() error {
			if err != nil || len(tracks) == 0 {
				return err
			}
			return appendQueue(tracks)
		})
		return queueDoneMsg{requestID: requestID, tracks: tracks, limitHit: limitHit, err: err}
	}
}

// fetchRadio resolves YouTube's mix for the track with seedID.
func fetchRadio(s Searcher, appendQueue func([]youtube.Track) error, seedID string, requestID uint64, task queueTask) tea.Cmd {
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
		err = task.run(func() error {
			if err != nil || len(tracks) == 0 {
				return err
			}
			return appendQueue(tracks)
		})
		return queueDoneMsg{requestID: requestID, tracks: tracks, err: err}
	}
}

func appendTracks(p *mpv.Player, tracks []youtube.Track) error {
	urls := make([]string, len(tracks))
	for i, track := range tracks {
		urls[i] = track.URL
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return p.AppendAll(ctx, urls)
}

// loadDevices reads mpv's output devices, which covers every audio output
// driver mpv picked (pipewire, coreaudio, …).
func loadDevices(p *mpv.Player, open bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		devices, err := p.AudioDevices(ctx)
		return devicesMsg{devices: devices, open: open, err: err}
	}
}

func waitMPV(ch <-chan mpv.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return mpvClosedMsg{}
		}
		return mpvEventMsg(ev)
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

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/pipewire"
	"github.com/omegaatt36/ytea/internal/thumbnail"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m.quit()
	}

	switch m.focus {
	case focusSearch:
		return m.handleSearchKey(msg)
	case focusSinks:
		return m.handleSinkKey(key)
	}

	if cmd, ok := m.handlePlaybackKey(key); ok {
		return m, cmd
	}

	switch key {
	case "q":
		return m.quit()
	case "/":
		m.focus = focusSearch
		return m, m.input.Focus()
	case "tab", "shift+tab":
		if m.focus == focusResults {
			m.focus = focusQueue
		} else {
			m.focus = focusResults
		}
		return m, nil
	case "o":
		return m, loadSinks(true)
	case "v":
		if m.deps.Tap != nil {
			m.showViz = !m.showViz
		}
		return m, nil
	}

	if m.focus == focusQueue {
		return m.handleQueueKey(key)
	}
	return m.handleResultKey(key)
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		query := strings.TrimSpace(m.input.Value())
		if query == "" {
			return m, nil
		}
		m.searching = true
		m.focus = focusResults
		m.input.Blur()
		m.setStatus("searching " + quote(query) + "…")
		return m, tea.Batch(m.spinner.Tick, search(m.deps.Searcher, query))
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
			m.setStatus("playing " + quote(t.Title))
			return m, do(func(ctx context.Context) error { return m.deps.Player.PlayNow(ctx, t.URL) })
		}
	case "a":
		if t, ok := m.selectedResult(); ok {
			m.setStatus("queued " + quote(t.Title))
			m.resultCur = min(len(m.results)-1, m.resultCur+1)
			return m, do(func(ctx context.Context) error { return m.deps.Player.Append(ctx, t.URL) })
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
		m.queueCur = min(len(m.queue)-1, i+1)
	case "g", "home":
		m.queueCur = 0
	case "G", "end":
		m.queueCur = max(0, len(m.queue)-1)
	case "enter":
		if i < len(m.queue) {
			return m, do(func(ctx context.Context) error { return p.PlayIndex(ctx, i) })
		}
	case "d", "x", "delete":
		if i < len(m.queue) {
			return m, do(func(ctx context.Context) error { return p.Remove(ctx, i) })
		}
	case "K", "shift+up":
		if i > 0 && i < len(m.queue) {
			m.queueCur--
			return m, do(func(ctx context.Context) error { return p.Move(ctx, i, i-1) })
		}
	case "J", "shift+down":
		if i < len(m.queue)-1 {
			m.queueCur++
			return m, do(func(ctx context.Context) error { return p.Move(ctx, i, i+1) })
		}
	}
	return m, nil
}

func (m Model) handleSinkKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.sinkCur = max(0, m.sinkCur-1)
	case "down", "j":
		m.sinkCur = min(len(m.sinks)-1, m.sinkCur+1)
	case "esc", "o", "q":
		m.focus = focusResults
	case "enter":
		m.focus = focusResults
		if m.sinkCur < len(m.sinks) {
			s := m.sinks[m.sinkCur]
			m.setStatus("output → " + s.Label())
			// Routing mpv itself (rather than relinking with wpctl) makes the
			// choice stick across tracks and survive stream re-creation.
			return m, do(func(ctx context.Context) error {
				return m.deps.Player.SetAudioDevice(ctx, "pipewire/"+s.Name)
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
		m.queue = mpv.Decode[[]mpv.PlaylistEntry](ev.Data)
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
func (m *Model) refreshThumb() tea.Cmd {
	if !m.deps.Thumbnails {
		return nil
	}
	_, t, ok := m.current()
	if !ok || t.ID == "" {
		if m.thumbID != 0 {
			id := m.thumbID
			m.thumbID, m.thumbVideo = 0, ""
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

	prev := m.thumbID
	m.thumbID = nextThumbID(prev)
	seq, err := thumbnail.Transmit(msg.img, m.thumbID, thumbCols, thumbRows)
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

func search(s Searcher, query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		tracks, err := s.Search(ctx, query, searchLimit)
		return searchDoneMsg{query: query, tracks: tracks, err: err}
	}
}

func loadSinks(open bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		g, err := pipewire.Dump(ctx)
		if err != nil {
			return sinksMsg{err: err}
		}
		return sinksMsg{sinks: g.Sinks(), open: open}
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

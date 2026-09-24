// Package tui is the Bubble Tea front end tying search, playback, PipeWire and MPRIS together.
package tui

import (
	"context"
	"image"
	"net/http"
	"slices"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/pipewire"
	"github.com/omegaatt36/ytea/internal/youtube"
)

const (
	searchLimit   = 30
	seekStep      = 5 * time.Second
	volumeStep    = 5
	thumbCols     = 18
	thumbRows     = 5
	vizRows       = 6
	cmdTimeout    = 5 * time.Second
	searchTimeout = 30 * time.Second
)

// Searcher finds tracks. It is satisfied by youtube.Searcher.
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]youtube.Track, error)
}

// Deps are the collaborators the UI drives. Tap and MPRIS are optional.
type Deps struct {
	Searcher   Searcher
	Player     *mpv.Player
	Tap        *pipewire.Tap
	MPRIS      *mpris.Server
	Thumbnails bool
	HTTP       *http.Client
	Normalize  bool
}

type focus int

const (
	focusSearch focus = iota
	focusResults
	focusQueue
	focusSinks
)

// Model is the root Bubble Tea model.
type Model struct {
	deps Deps

	width, height int

	input   textinput.Model
	spinner spinner.Model
	focus   focus

	searching bool
	results   []youtube.Track
	resultCur int

	queue    []mpv.PlaylistEntry
	queueCur int
	pos      int
	// tracks remembers search metadata by URL, since mpv only knows filenames
	// until yt-dlp resolves each entry.
	tracks map[string]youtube.Track

	timePos, duration time.Duration
	paused, idle      bool
	volume            float64
	codec             string
	params            mpv.AudioParams
	device            string
	normalize         bool

	levels  []float64
	showViz bool

	sinks   []pipewire.Sink
	sinkCur int

	status    string
	statusErr bool

	thumbID    int
	thumbVideo string
}

// New builds the root model.
func New(deps Deps) Model {
	in := textinput.New()
	in.Placeholder = "search YouTube…"
	in.Prompt = " / "
	in.CharLimit = 200
	// Focus here, not in Init: Init has a value receiver, so focusing there
	// would only change a copy and keystrokes would be ignored.
	in.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot

	return Model{
		deps:      deps,
		input:     in,
		spinner:   sp,
		focus:     focusSearch,
		tracks:    make(map[string]youtube.Track),
		pos:       -1,
		idle:      true,
		volume:    100,
		normalize: deps.Normalize,
		showViz:   deps.Tap != nil,
	}
}

// Init starts the event pumps.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		waitMPV(m.deps.Player.Events()),
		loadSinks(false),
	}
	if m.deps.Tap != nil {
		cmds = append(cmds, waitLevels(m.deps.Tap.Levels()))
	}
	return tea.Batch(cmds...)
}

type (
	searchDoneMsg struct {
		query  string
		tracks []youtube.Track
		err    error
	}
	mpvEventMsg  mpv.Event
	mpvClosedMsg struct{}
	levelsMsg    []float64
	sinksMsg     struct {
		sinks []pipewire.Sink
		open  bool
		err   error
	}
	errMsg   struct{ err error }
	thumbMsg struct {
		videoID string
		img     image.Image
		err     error
	}
)

// Update handles a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.width/2))
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if !m.searching {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case searchDoneMsg:
		m.searching = false
		if msg.err != nil {
			m.setError("search failed: " + msg.err.Error())
			return m, nil
		}
		m.results, m.resultCur = msg.tracks, 0
		for _, t := range msg.tracks {
			m.tracks[t.URL] = t
		}
		m.setStatus(pluralize(len(msg.tracks), "result") + " for " + quote(msg.query))
		return m, nil

	case mpvEventMsg:
		cmd := m.applyEvent(mpv.Event(msg))
		m.syncMPRIS()
		return m, tea.Batch(cmd, waitMPV(m.deps.Player.Events()))

	case mpvClosedMsg:
		m.setError("mpv exited")
		return m, tea.Quit

	case levelsMsg:
		m.levels = msg
		return m, waitLevels(m.deps.Tap.Levels())

	case sinksMsg:
		if msg.err != nil {
			m.setError("list outputs: " + msg.err.Error())
			return m, nil
		}
		m.sinks = msg.sinks
		if msg.open {
			m.focus = focusSinks
			m.sinkCur = max(0, slices.IndexFunc(m.sinks, m.isCurrentSink))
		}
		return m, nil

	case thumbMsg:
		return m.applyThumb(msg)

	case errMsg:
		m.setError(msg.err.Error())
		return m, nil
	}
	return m, nil
}

func (m *Model) setStatus(s string) {
	m.status, m.statusErr = s, false
}

func (m *Model) setError(s string) {
	m.status, m.statusErr = s, true
}

// current returns the playing playlist entry and its search metadata, if any.
func (m Model) current() (mpv.PlaylistEntry, youtube.Track, bool) {
	if m.idle || m.pos < 0 || m.pos >= len(m.queue) {
		return mpv.PlaylistEntry{}, youtube.Track{}, false
	}
	e := m.queue[m.pos]
	return e, m.tracks[e.Filename], true
}

func (m Model) isCurrentSink(s pipewire.Sink) bool {
	if m.device == "" || m.device == "auto" {
		return s.Default
	}
	return m.device == "pipewire/"+s.Name
}

func (m Model) currentSink() (pipewire.Sink, bool) {
	i := slices.IndexFunc(m.sinks, m.isCurrentSink)
	if i < 0 {
		return pipewire.Sink{}, false
	}
	return m.sinks[i], true
}

func (m Model) syncMPRIS() {
	if m.deps.MPRIS == nil {
		return
	}
	st := mpris.State{
		Status:   mpris.Stopped,
		Volume:   min(m.volume/100, 1),
		Position: m.timePos,
		CanPrev:  m.pos > 0,
		CanNext:  m.pos >= 0 && m.pos < len(m.queue)-1,
	}
	if e, t, ok := m.current(); ok {
		st.Status = mpris.Playing
		if m.paused {
			st.Status = mpris.Paused
		}
		st.TrackID = t.ID
		if st.TrackID == "" {
			st.TrackID = e.Filename
		}
		st.Title = displayTitle(e, t)
		st.Artist = t.Channel
		st.URL = e.Filename
		// A live stream's duration is its DVR window, not a track length.
		if !t.Live {
			st.Length = m.duration
		}
		if t.ID != "" {
			st.ArtURL = t.ThumbnailURL()
		}
	}
	m.deps.MPRIS.Update(st)
}

func displayTitle(e mpv.PlaylistEntry, t youtube.Track) string {
	switch {
	case t.Title != "":
		return t.Title
	case e.Title != "":
		return e.Title
	default:
		return e.Filename
	}
}

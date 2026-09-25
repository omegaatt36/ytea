// Package tui is the Bubble Tea front end tying search, playback and MPRIS together.
package tui

import (
	"context"
	"image"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/thumbnail"
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

// Spectrum delivers visualizer band levels. It is satisfied by pipewire.Tap,
// whose capture is PipeWire-specific and therefore unavailable off Linux.
type Spectrum interface {
	Levels() <-chan []float64
}

// Deps are the collaborators the UI drives. Tap and MPRIS are optional.
type Deps struct {
	Searcher   Searcher
	Player     *mpv.Player
	Tap        Spectrum
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
	focusDevices
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

	devices   []mpv.AudioDevice
	deviceCur int

	status    string
	statusErr bool

	graphics         graphicsSupport
	probeKitty       bool
	probePlaceholder bool
	thumbID          int    // kitty image id in placeholder or direct mode
	thumbArt         string // half-block rendering otherwise
	thumbVideo       string
	// placedAt is where the direct-mode image was last put; placed is false
	// when it must be (re)placed.
	placedAt image.Point
	placed   bool
}

// graphicsSupport is how thumbnails are drawn, decided by probing the terminal.
type graphicsSupport int

const (
	graphicsUnknown graphicsSupport = iota
	// graphicsPlaceholder uses kitty Unicode placeholders, which the cell
	// renderer treats as text (Ghostty, kitty).
	graphicsPlaceholder
	// graphicsDirect puts the image at a cursor position, for kitty graphics
	// implementations without placeholders (Zellij >= 0.45).
	graphicsDirect
	// graphicsNone falls back to half-block art (Zellij < 0.45, tmux).
	graphicsNone
)

func (g graphicsSupport) String() string {
	switch g {
	case graphicsPlaceholder:
		return "kitty placeholders"
	case graphicsDirect:
		return "kitty direct placement"
	case graphicsNone:
		return "half-blocks"
	default:
		return "unknown"
	}
}

// pasteKeys read the OS clipboard directly. ctrl+shift+v and shift+insert are
// normally consumed by the terminal as its own paste, but under a multiplexer
// speaking the kitty keyboard protocol (Zellij) they can arrive as plain key
// events instead, so treat them as paste requests too.
var pasteKeys = key.NewBinding(key.WithKeys("ctrl+v", "ctrl+shift+v", "shift+insert"))

// New builds the root model.
func New(deps Deps) Model {
	in := textinput.New()
	in.Placeholder = "search YouTube…"
	in.Prompt = " / "
	in.CharLimit = 200
	in.KeyMap.Paste = pasteKeys
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
		loadDevices(m.deps.Player, false),
	}
	if m.deps.Tap != nil {
		cmds = append(cmds, waitLevels(m.deps.Tap.Levels()))
	}
	if m.deps.Thumbnails {
		cmds = append(cmds, tea.Raw(thumbnail.Query()))
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
	devicesMsg   struct {
		devices []mpv.AudioDevice
		open    bool
		err     error
	}
	errMsg   struct{ err error }
	thumbMsg struct {
		videoID string
		img     image.Image
		err     error
	}
	placeMsg struct {
		id int
		at image.Point
	}
)

// placeDelay lets the renderer finish the frame (including any post-resize
// screen clear) before the image is put on top of it.
const placeDelay = 100 * time.Millisecond

// syncPlacement schedules a direct-mode placement when the image is new, the
// screen was cleared, or the now-playing box moved.
func (m *Model) syncPlacement() tea.Cmd {
	if m.graphics != graphicsDirect || m.thumbID == 0 {
		return nil
	}
	if _, _, ok := m.current(); !ok {
		return nil
	}
	at := m.thumbOrigin()
	if m.placed && at == m.placedAt {
		return nil
	}
	m.placed, m.placedAt = true, at
	id := m.thumbID
	return tea.Tick(placeDelay, func(time.Time) tea.Msg { return placeMsg{id: id, at: at} })
}

// Update handles a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	// Any message can shift the layout under a directly placed image, so the
	// placement is reconciled after every update rather than at each call site.
	// Called before the return: Go leaves unspecified whether a return operand
	// reading nm is evaluated before or after a call that mutates it.
	placeCmd := nm.syncPlacement()
	return nm, tea.Batch(cmd, placeCmd)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.width/2))
		// The renderer clears the screen after a resize, which drops placements.
		m.placed = false
		return m, nil

	case placeMsg:
		if m.graphics != graphicsDirect || msg.id != m.thumbID || msg.at != m.placedAt {
			return m, nil
		}
		return m, tea.Raw(thumbnail.Put(msg.id, msg.at.X, msg.at.Y, thumbCols, thumbRows))

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		// Terminal paste (ctrl+shift+v, cmd+v) always lands in the search box;
		// there is nothing else to paste into.
		cmd := m.focusSearch()
		var inputCmd tea.Cmd
		m.input, inputCmd = m.input.Update(msg)
		return m, tea.Batch(cmd, inputCmd)

	case uv.KittyGraphicsEvent:
		if m.graphics != graphicsUnknown {
			return m, nil
		}
		ok := string(msg.Payload) == "OK"
		switch msg.Options.ID {
		case thumbnail.QueryID:
			m.probeKitty = ok
		case thumbnail.PlaceholderQueryID:
			m.probePlaceholder = ok
		}
		return m, nil

	case uv.PrimaryDeviceAttributesEvent:
		// DA1 is answered after both probes, so every kitty reply is in by now.
		if !m.deps.Thumbnails || m.graphics != graphicsUnknown {
			return m, nil
		}
		switch {
		case m.probePlaceholder:
			m.graphics = graphicsPlaceholder
		case m.probeKitty:
			m.graphics = graphicsDirect
		default:
			m.graphics = graphicsNone
		}
		slog.Info("thumbnail rendering", "mode", m.graphics.String())
		return m, m.refreshThumb()

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

	case devicesMsg:
		if msg.err != nil {
			m.setError("list outputs: " + msg.err.Error())
			return m, nil
		}
		m.devices = msg.devices
		if msg.open {
			m.focus = focusDevices
			m.deviceCur = max(0, slices.IndexFunc(m.devices, m.isCurrentDevice))
		}
		return m, nil

	case thumbMsg:
		return m.applyThumb(msg)

	case errMsg:
		m.setError(msg.err.Error())
		return m, nil
	}

	// The text input has private messages of its own: cursor blinks and the
	// clipboard contents read by its ctrl+v binding.
	if m.focus == focusSearch {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) focusSearch() tea.Cmd {
	m.focus = focusSearch
	return m.input.Focus()
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

// isCurrentDevice reports whether d is the device mpv plays through. An unset
// device is mpv's "auto".
func (m Model) isCurrentDevice(d mpv.AudioDevice) bool {
	return d.Name == m.currentDeviceName()
}

// currentDeviceName is mpv's configured audio device, defaulting to auto.
func (m Model) currentDeviceName() string {
	if m.device == "" {
		return "auto"
	}
	return m.device
}

// currentDevice returns the device mpv is playing through, if it is listed.
func (m Model) currentDevice() (mpv.AudioDevice, bool) {
	name := m.currentDeviceName()
	i := slices.IndexFunc(m.devices, func(d mpv.AudioDevice) bool { return d.Name == name })
	if i < 0 {
		return mpv.AudioDevice{}, false
	}
	return m.devices[i], true
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

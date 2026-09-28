// Package tui is the Bubble Tea front end tying search, playback and MPRIS together.
package tui

import (
	"context"
	"errors"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/omegaatt36/ytea/internal/history"
	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

const (
	seekStep      = 5 * time.Second
	volumeStep    = 5
	thumbCols     = 18
	thumbRows     = 5
	cmdTimeout    = 5 * time.Second
	statusTimeout = 4 * time.Second
)

type Searcher interface {
	// Search skips the first offset results, so later pages extend earlier ones.
	Search(ctx context.Context, query string, offset, limit int) ([]youtube.Track, error)
	// Lookup resolves the tracks behind a YouTube URL; a positive limit caps the listing.
	Lookup(ctx context.Context, url string, limit int) ([]youtube.Track, error)
}

// Spectrum delivers visualizer band levels. It is satisfied by pipewire.Tap
// on Linux and audiotee.Tap on macOS.
type Spectrum interface {
	Levels() <-chan []float64
}

type Player interface {
	Events() <-chan mpv.Event
	AudioDevices(context.Context) ([]mpv.AudioDevice, error)
	SetAudioDevice(context.Context, string) error
	StreamInfo(context.Context) (mpv.StreamInfo, error)
	TogglePause(context.Context) error
	Seek(context.Context, time.Duration) error
	SeekTo(context.Context, time.Duration) error
	SeekPercent(context.Context, float64) error
	Next(context.Context) error
	Prev(context.Context) error
	AddVolume(context.Context, int) error
	SetNormalize(context.Context, bool) error
	PlayNow(context.Context, string) error
	Append(context.Context, string) error
	AppendAll(context.Context, []string) error
	PlayAll(ctx context.Context, urls []string, start int) error
	PlayIndex(context.Context, int) error
	Remove(context.Context, int) error
	Move(context.Context, int, int) error
	Reorder(ctx context.Context, pos int, current string, order []int) error
	SetRepeat(context.Context, mpv.Repeat) error
	Stop(context.Context) error
	Playlist(context.Context) ([]mpv.PlaylistEntry, int, error)
}

type Library interface {
	Playlists() []library.Playlist
	CreateWithTracks(name string, tracks []youtube.Track) (int, error)
	Add(index int, track youtube.Track) error
	Rename(index int, name string) error
	Move(from, to int) error
	MoveTrack(playlistIndex, from, to int) error
	RemoveTrack(playlistIndex, trackIndex int) error
	Delete(index int) error
}

// History remembers recently played tracks, newest first.
type History interface {
	Entries() []history.Entry
	Record(track youtube.Track, at time.Time) error
	Remove(index int) error
}

type MPRIS interface {
	Update(mpris.State)
	Seeked(time.Duration)
}

// AccountPlaylistSource supplies read-only account playlists.
type AccountPlaylistSource interface {
	ListPlaylists(context.Context) ([]youtube.AccountPlaylist, error)
	ListTracks(context.Context, string) ([]youtube.Track, error)
}

// Deps are the collaborators the UI drives. AccountPlaylists, Tap, MPRIS and
// History are optional.
type Deps struct {
	AccountPlaylists AccountPlaylistSource
	Searcher         Searcher
	Player           Player
	Tap              Spectrum
	MPRIS            MPRIS
	Thumbnails       bool
	HTTP             *http.Client
	Normalize        bool
	InitialTracks    map[string]youtube.Track
	Library          Library
	History          History
	OpenURL          func(string) error
}

type focus int

const (
	focusSearch focus = iota
	focusResults
	focusQueue
	focusPlaylists
	focusPlaylistTracks
	focusHistory
)

type Model struct {
	deps    Deps
	account accountPlaylistState
	keys    keyMap
	help    help.Model

	width, height int

	input                 textinput.Model
	nameInput             textinput.Model
	spinner               spinner.Model
	focus                 focus
	overlay               overlay
	saveTrack             youtube.Track
	nameTracks            []youtube.Track
	nameMode              nameMode
	playlists             []library.Playlist
	playlistCur           int
	playlistTrackCur      int
	deletePlaylistPending int
	drag                  listPane

	searching      bool
	requestID      uint64
	activeRequest  uint64
	searchRequest  uint64
	spinnerRequest uint64
	imports        importQueue

	results resultsPane

	queue    queueSync
	queueCur int
	// tracks remembers search metadata by URL, since mpv only knows filenames
	// until yt-dlp resolves each entry.
	tracks map[string]youtube.Track

	player playerState

	history    []history.Entry
	historyCur int
	// historyLast is the queue entry last recorded, so one play is recorded once.
	historyLast string

	levels              []float64
	showViz             bool
	spectrumUnavailable string
	spectrumFailure     string
	fullHelp            bool

	devices   []mpv.AudioDevice
	deviceCur int

	stream mpv.StreamInfo

	status              string
	statusErr           bool
	statusVersion       uint64
	statusTickScheduled uint64

	thumb thumbImage

	rng *rand.Rand
}

func New(deps Deps) Model {
	keys := newKeyMap()
	keys.global.Viz.SetEnabled(deps.Tap != nil)
	hm := help.New()
	hm.ShortSeparator = " · "
	hm.Styles = help.Styles{
		ShortKey: mutedStyle, ShortDesc: dimStyle, ShortSeparator: dimStyle, Ellipsis: dimStyle,
		FullKey: lipgloss.NewStyle(), FullDesc: mutedStyle, FullSeparator: dimStyle,
	}
	// Built from scratch: the bubbles defaults use fixed 256-color greys and
	// recolor the terminal's own cursor.
	inputStyles := textinput.Styles{
		Focused: textinput.StyleState{Prompt: headStyle, Placeholder: dimStyle},
		Blurred: textinput.StyleState{Prompt: dimStyle, Placeholder: dimStyle, Text: mutedStyle},
		Cursor:  textinput.CursorStyle{Shape: tea.CursorBlock, Blink: true},
	}
	in := textinput.New()
	in.SetStyles(inputStyles)
	in.Placeholder = "search YouTube…"
	in.Prompt = "/ "
	in.CharLimit = 200
	in.KeyMap.Paste = keys.global.Paste
	in.SetVirtualCursor(false)
	in.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	nameInput := textinput.New()
	nameInput.SetStyles(inputStyles)
	nameInput.Placeholder = "playlist name"
	nameInput.Prompt = "name: "
	nameInput.CharLimit = 100
	nameInput.KeyMap.Paste = keys.global.Paste
	nameInput.SetVirtualCursor(false)
	filterInput := textinput.New()
	filterInput.SetStyles(inputStyles)
	filterInput.Placeholder = "filter results"
	filterInput.Prompt = "filter: "
	filterInput.CharLimit = 100
	filterInput.KeyMap.Paste = keys.global.Paste
	filterInput.SetVirtualCursor(false)

	tracks := make(map[string]youtube.Track, len(deps.InitialTracks))
	maps.Copy(tracks, deps.InitialTracks)
	var playlists []library.Playlist
	if deps.Library != nil {
		playlists = deps.Library.Playlists()
	}
	var played []history.Entry
	if deps.History != nil {
		played = deps.History.Entries()
	}
	return Model{
		deps:                  deps,
		keys:                  keys,
		help:                  hm,
		input:                 in,
		nameInput:             nameInput,
		results:               resultsPane{filter: filterInput},
		spinner:               sp,
		focus:                 focusSearch,
		tracks:                tracks,
		playlists:             playlists,
		history:               played,
		deletePlaylistPending: -1,
		queue:                 newQueueSync(deps.Player),
		thumb:                 newThumbImage(deps.Thumbnails, deps.HTTP),
		player:                playerState{idle: true, volume: 100, normalize: deps.Normalize},
		showViz:               deps.Tap != nil,
		rng:                   rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
}

func (m Model) KnownTrack(url string) youtube.Track { return m.tracks[url] }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		waitMPV(m.deps.Player.Events(), m.player.timePos),
		loadDevices(m.deps.Player, false),
	}
	if m.deps.Tap != nil {
		cmds = append(cmds, waitLevels(m.deps.Tap.Levels()))
		if source, ok := m.deps.Tap.(interface{ Errors() <-chan error }); ok {
			cmds = append(cmds, waitSpectrumError(source.Errors()))
		}
	}
	cmds = append(cmds, m.thumb.init())
	return tea.Batch(cmds...)
}

type (
	mpvEventMsg      mpv.Event
	mpvClosedMsg     struct{}
	levelsMsg        []float64
	spectrumErrorMsg struct{ err error }
	devicesMsg       struct {
		devices []mpv.AudioDevice
		open    bool
		err     error
	}
	streamMsg struct {
		info mpv.StreamInfo
		open bool
		err  error
	}
	errMsg           struct{ err error }
	statusTimeoutMsg struct{ version uint64 }
)

func (m *Model) syncPlacement() tea.Cmd {
	if _, _, ok := m.current(); !ok {
		return nil
	}
	return m.thumb.syncPlacement(m.thumbOrigin)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	accountCmd := nm.loadAccountOnFocus()
	// Any message can shift the layout under a directly placed image. Called
	// before the return: Go leaves the order of nm's read vs. this mutation unspecified.
	placeCmd := nm.syncPlacement()
	var statusCmd tea.Cmd
	if nm.status != "" && nm.statusErr && nm.statusTickScheduled != nm.statusVersion {
		nm.statusTickScheduled = nm.statusVersion
		v := nm.statusVersion
		statusCmd = tea.Tick(statusTimeout, func(time.Time) tea.Msg {
			return statusTimeoutMsg{version: v}
		})
	}
	return nm, tea.Batch(cmd, accountCmd, placeCmd, statusCmd)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case accountPlaylistsMsg:
		if msg.generation != m.account.generation {
			return m, nil
		}
		m.account.loading = false
		m.account.err = msg.err
		if msg.err == nil && len(msg.playlists) == 0 {
			m.account.err = errors.New("no account data")
		}
		m.account.playlists = msg.playlists
		m.playlistCur = min(m.playlistCur, max(0, m.playlistCount()-1))
		return m, nil
	case accountQueueFailedMsg:
		// A successful browse supersedes an earlier queue lookup for the same playlist.
		_, browsed := m.account.tracks[msg.id]
		if msg.generation == m.account.generation && !browsed {
			m.account.queueErrors[msg.id] = msg.err
		}
		return m, nil
	case accountTracksMsg:
		if msg.generation != m.account.generation {
			return m, nil
		}
		m.account.trackLoading[msg.id] = false
		m.account.trackErrors[msg.id] = msg.err
		if msg.err == nil {
			m.account.tracks[msg.id] = msg.tracks
			delete(m.account.queueErrors, msg.id)
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(m.width)
		m.input.SetWidth(m.searchWidth())
		m.nameInput.SetWidth(max(1, dialogWidth(nameDialogWidth, m.width)-2*boxInset-lipgloss.Width(m.nameInput.Prompt)-1))
		m.results.setWidth(resultsWidth(m.width))
		return m, m.updateThumb(msg)

	case placeMsg, uv.KittyGraphicsEvent, uv.PrimaryDeviceAttributesEvent, thumbMsg:
		return m, m.updateThumb(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.PasteMsg:
		if m.fullHelp {
			return m, nil
		}
		if m.overlay == overlayName {
			var inputCmd tea.Cmd
			m.nameInput, inputCmd = m.nameInput.Update(msg)
			return m, inputCmd
		}
		if m.inFilter() {
			return m, m.updateResults(msg)
		}
		cmd := m.focusSearch()
		var inputCmd tea.Cmd
		m.input, inputCmd = m.input.Update(msg)
		return m, tea.Batch(cmd, inputCmd)

	case spinner.TickMsg:
		if !m.searching {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case searchDoneMsg:
		m.searchDone(msg)
		return m, nil

	case lookupDoneMsg:
		return m, m.resolveImport(msg)

	case queueDoneMsg:
		return m, m.queueDone(msg)

	case queueActionDoneMsg:
		_, cmd := m.updateQueue(msg)
		if msg.requestID == m.activeRequest {
			if msg.err != nil {
				m.setError(msg.err.Error())
			} else {
				m.setStatus(msg.status)
			}
		}
		return m, cmd

	case queueRefreshMsg:
		synced, cmd := m.updateQueue(msg)
		if synced {
			m.syncMPRIS()
		}
		return m, cmd

	case queueDebounceMsg:
		_, cmd := m.updateQueue(msg)
		return m, cmd

	case mpvEventMsg:
		cmd := m.applyEvent(mpv.Event(msg))
		m.syncMPRIS()
		return m, tea.Batch(cmd, m.recordPlay(), waitMPV(m.deps.Player.Events(), m.player.timePos))

	case historyChangedMsg:
		m.reloadHistory()
		return m, nil

	case mpvClosedMsg:
		m.setError("mpv exited")
		return m, tea.Quit

	case levelsMsg:
		m.levels = msg
		return m, waitLevels(m.deps.Tap.Levels())

	case spectrumErrorMsg:
		old := m.spectrumFailure
		m.spectrumFailure = ""
		if msg.err != nil {
			m.spectrumFailure = "spectrum: " + msg.err.Error()
			m.setError(m.spectrumFailure)
		} else if m.status == old {
			m.clearError()
		}
		if source, ok := m.deps.Tap.(interface{ Errors() <-chan error }); ok {
			return m, waitSpectrumError(source.Errors())
		}
		return m, nil

	case devicesMsg:
		if msg.err != nil {
			m.setError("list outputs: " + msg.err.Error())
			return m, nil
		}
		m.devices = msg.devices
		if msg.open {
			m.overlay = overlayDevices
			m.deviceCur = max(0, slices.IndexFunc(m.devices, m.isCurrentDevice))
		}
		return m, nil

	case streamMsg:
		if msg.err != nil {
			m.setError("read stream info: " + msg.err.Error())
			return m, nil
		}
		m.stream = msg.info
		if msg.open {
			m.overlay = overlayInfo
		}
		return m, nil

	case errMsg:
		m.setError(msg.err.Error())
		return m, nil

	case statusTimeoutMsg:
		if msg.version == m.statusVersion {
			m.status = ""
			m.statusErr = false
		}
		return m, nil
	}

	if m.overlay == overlayNone && m.focus == focusSearch {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	if m.inFilter() {
		return m, m.updateResults(msg)
	}
	return m, nil
}

func (m *Model) updateQueue(msg tea.Msg) (synced bool, cmd tea.Cmd) {
	before := m.queue.synced
	m.queue, cmd = m.queue.update(msg)
	if m.queue.synced == before {
		return false, cmd
	}
	m.queueCur = min(max(0, m.queueCur), max(0, len(m.queue.entries)-1))
	return true, tea.Batch(cmd, m.refreshThumb())
}

func (m *Model) updateResults(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.results, cmd = m.results.update(msg)
	return cmd
}

func (m *Model) updateThumb(msg tea.Msg) tea.Cmd {
	before := m.thumb.graphics
	var cmd tea.Cmd
	m.thumb, cmd = m.thumb.update(msg)
	if m.thumb.graphics == before {
		return cmd
	}
	return tea.Batch(cmd, m.refreshThumb())
}

func (m *Model) focusSearch() tea.Cmd {
	m.overlay, m.focus = overlayNone, focusSearch
	m.results.filter.Blur()
	return m.input.Focus()
}

func (m *Model) setStatus(s string) {
	m.status, m.statusErr = s, false
	m.statusVersion++
}

func (m *Model) setError(s string) {
	m.status, m.statusErr = s, true
	m.statusVersion++
}

func (m *Model) clearError() {
	if m.statusErr {
		m.status, m.statusErr = "", false
		m.statusVersion++
	}
}

func (m Model) current() (mpv.PlaylistEntry, youtube.Track, bool) {
	pos := m.queue.pos
	if m.player.idle || pos < 0 || pos >= len(m.queue.entries) {
		return mpv.PlaylistEntry{}, youtube.Track{}, false
	}
	e := m.queue.entries[pos]
	return e, m.tracks[e.Filename], true
}

// entryTrack is what is known about a queue entry, falling back to mpv's title.
func (m Model) entryTrack(e mpv.PlaylistEntry) youtube.Track {
	t := m.tracks[e.Filename]
	t.URL = e.Filename
	if t.Title == "" {
		t.Title = e.Title
	}
	return t
}

func (m Model) isCurrentDevice(d mpv.AudioDevice) bool {
	return d.Name == m.player.deviceName()
}

func (m Model) currentDevice() (mpv.AudioDevice, bool) {
	name := m.player.deviceName()
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
		Volume:   min(m.player.volume/100, 1),
		Position: m.player.timePos,
		CanPrev:  m.queue.pos > 0,
		CanNext:  m.queue.pos >= 0 && m.queue.pos < len(m.queue.entries)-1,
	}
	if e, t, ok := m.current(); ok {
		st.Status = mpris.Playing
		if m.player.paused {
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
			st.Length = m.player.duration
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

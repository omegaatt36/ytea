// Package tui is the Bubble Tea front end tying search, playback and MPRIS together.
package tui

import (
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

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/service"
)

const (
	seekStep      = 5 * time.Second
	volumeStep    = 5
	thumbCols     = 18
	thumbRows     = 5
	cmdTimeout    = 5 * time.Second
	statusTimeout = 4 * time.Second
)

// Spectrum delivers visualizer band levels. It is satisfied by pipewire.Tap
// on Linux and audiotee.Tap on macOS.
type Spectrum interface {
	Levels() <-chan []float64
}

type MPRIS interface {
	Update(mpris.State)
	Seeked(time.Duration)
}

// Deps are the collaborators the UI drives. AccountPlaylists, Tap, MPRIS and
// History are optional.
type Deps struct {
	AccountPlaylists service.AccountSource
	AccountIgnores   service.AccountIgnoreStore
	Searcher         service.Searcher
	Player           service.Player
	Tap              Spectrum
	MPRIS            MPRIS
	Thumbnails       bool
	HTTP             *http.Client
	Normalize        bool
	InitialTracks    map[string]domain.Track
	Library          service.PlaylistStore
	History          service.HistoryStore
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
	deps Deps
	keys keyMap
	help help.Model

	width, height int

	input                 textinput.Model
	nameInput             textinput.Model
	spinner               spinner.Model
	focus                 focus
	overlay               overlay
	saveTrack             domain.Track
	nameTracks            []domain.Track
	nameMode              nameMode
	playlistCur           int
	playlistTrackCur      int
	deletePlaylistPending int
	drag                  listPane

	// core is everything that outlives a front end; the rest of the model is
	// what only this one needs: cursors, focus, overlays, inputs and text.
	core service.Core

	results  resultsPane
	queueCur int

	historyCur int

	levels              []float64
	vu                  [2]float64
	levelsWaiting       bool
	vuWaiting           bool
	showViz             bool
	vizMode             vizMode
	spectrumUnavailable string
	spectrumFailure     string
	fullHelp            bool

	deviceCur int

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

	return Model{
		deps:                  deps,
		keys:                  keys,
		help:                  hm,
		input:                 in,
		nameInput:             nameInput,
		results:               resultsPane{filter: filterInput},
		spinner:               sp,
		focus:                 focusSearch,
		deletePlaylistPending: -1,
		core: service.New(service.Deps{
			Searcher:       deps.Searcher,
			Player:         deps.Player,
			Library:        deps.Library,
			History:        deps.History,
			Account:        deps.AccountPlaylists,
			AccountIgnores: deps.AccountIgnores,
			InitialTracks:  deps.InitialTracks,
			Normalize:      deps.Normalize,
		}),
		thumb:         newThumbImage(deps.Thumbnails, deps.HTTP),
		showViz:       deps.Tap != nil,
		levelsWaiting: deps.Tap != nil,
		rng:           rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
}

func (m Model) KnownTrack(url string) domain.Track { return m.core.Tracks[url] }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		waitPlayer(m.core.Events(), m.core.Playback.TimePos),
		loadDevices(m.core, false),
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
	playerEventMsg   struct{ ev service.PlayerEvent }
	playerClosedMsg  struct{}
	levelsMsg        []float64
	vuMsg            [2]float64
	spectrumErrorMsg struct{ err error }
	devicesMsg       struct {
		service.DevicesLoaded
		open bool
	}
	streamMsg struct {
		service.StreamLoaded
		open bool
	}
	errMsg           struct{ err error }
	statusTimeoutMsg struct{ version uint64 }
)

func (m *Model) syncPlacement() tea.Cmd {
	if _, _, ok := m.core.Current(); !ok {
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
	case service.AccountPlaylistsDone:
		return m.updateAccount(msg)
	case service.AccountTracksDone:
		return m.updateAccount(msg)
	case service.AccountQueueFailed:
		return m.updateAccount(msg)
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
		if !m.core.Busy() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case service.SearchDone:
		m.searchDone(msg)
		return m, nil

	case service.LookupDone:
		return m, m.resolveImport(msg)

	case service.AppendDone:
		return m, m.queueDone(msg)

	case service.ActionDone:
		_, cmd := m.updateQueueMsg(msg)
		if msg.RequestID == m.core.Active {
			if msg.Err != nil {
				m.setError(msg.Err.Error())
			} else {
				m.setStatus(actionStatus(msg))
			}
		}
		return m, cmd

	case service.Refreshed:
		synced, cmd := m.updateQueueMsg(msg)
		if synced {
			m.syncMPRIS()
		}
		return m, cmd

	case service.Debounced:
		_, cmd := m.updateQueueMsg(msg)
		return m, cmd

	case service.Failed:
		m.setError(msg.Err.Error())
		return m, nil

	case playerEventMsg:
		cmd := m.applyEvent(msg.ev)
		m.syncMPRIS()
		return m, tea.Batch(cmd, waitPlayer(m.core.Events(), m.core.Playback.TimePos))

	case service.HistoryRecorded:
		m.reloadHistory()
		return m, nil

	case playerClosedMsg:
		m.setError("mpv exited")
		return m, tea.Quit

	case levelsMsg:
		m.levels = msg
		m.levelsWaiting = false
		if m.showViz && m.vizMode == vizSpectrum {
			m.levelsWaiting = true
			return m, waitLevels(m.deps.Tap.Levels())
		}
		return m, nil

	case vuMsg:
		m.vu = msg
		m.vuWaiting = false
		if source, ok := m.deps.Tap.(interface{ VU() <-chan [2]float64 }); ok && m.showViz && m.vizMode == vizVU {
			m.vuWaiting = true
			return m, waitVU(source.VU())
		}
		return m, nil

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
		if msg.Err != nil {
			m.setError("list outputs: " + msg.Err.Error())
			return m, nil
		}
		m.core.Update(msg.DevicesLoaded)
		if msg.open {
			m.overlay = overlayDevices
			m.deviceCur = max(0, slices.IndexFunc(m.core.Devices, m.core.IsCurrentDevice))
		}
		return m, nil

	case streamMsg:
		if msg.Err != nil {
			m.setError("read stream info: " + msg.Err.Error())
			return m, nil
		}
		m.core.Update(msg.StreamLoaded)
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

func (m *Model) updateQueueMsg(msg service.Msg) (synced bool, cmd tea.Cmd) {
	before := m.core.Queue.Synced
	cmd = teaCmds(m.core.Update(msg).Cmds)
	if m.core.Queue.Synced == before {
		return false, cmd
	}
	m.queueCur = min(max(0, m.queueCur), max(0, len(m.core.Queue.Entries)-1))
	return true, tea.Batch(cmd, m.refreshThumb())
}

func (m Model) updateAccount(msg service.Msg) (tea.Model, tea.Cmd) {
	result := m.core.Update(msg)
	m.playlistCur = min(m.playlistCur, max(0, m.playlistCount()-1))
	return m, teaCmds(result.Cmds)
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

func (m Model) syncMPRIS() {
	if m.deps.MPRIS != nil {
		m.deps.MPRIS.Update(mpris.StateOf(m.core.NowPlaying()))
	}
}

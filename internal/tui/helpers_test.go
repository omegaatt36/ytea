package tui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

const watchURL = "https://www.youtube.com/watch?v=abc"

// Model fixtures.

// overlayModel is a 120x40 model with one playing track, one saved playlist
// and one audio device, focused on origin with the search input blurred.
func overlayModel(t *testing.T, origin focus) Model {
	t.Helper()
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWithTracks("Keep", []youtube.Track{{Title: "Kept", URL: watchURL}}); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store, Player: &spyPlayer{info: mpv.StreamInfo{Path: watchURL}}})
	m.width, m.height = 120, 40
	m.input.Blur()
	m.focus = origin
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watchURL, Title: "Song"}}
	m.queue.pos, m.idle = 0, false
	m.tracks[watchURL] = youtube.Track{ID: "abc", Title: "Song", URL: watchURL}
	m.devices = []mpv.AudioDevice{{Name: "auto", Description: "Autoselect device"}}
	return m
}

func playingModel(g graphicsSupport) Model {
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = g
	m.width, m.height = 100, 30
	m.idle, m.queue.pos = false, 0
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watchURL}}
	m.tracks[watchURL] = youtube.Track{ID: "abc", Title: "Song", URL: watchURL}
	m.thumb.video = "abc"
	return m
}

func mouseModel() Model {
	m := New(Deps{})
	m.width, m.height = 100, 30
	for i := range 40 {
		m.results = append(m.results, youtube.Track{Title: fmt.Sprintf("song %02d", i)})
	}
	for i := range 3 {
		m.queue.entries = append(m.queue.entries, mpv.PlaylistEntry{Filename: fmt.Sprintf("u%d", i), Title: fmt.Sprintf("queued %d", i)})
	}
	return m
}

func filterModel() (Model, *spyPlayer) {
	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.width, m.height = 100, 30
	m.input.Blur()
	m.focus = focusResults
	m.results = []youtube.Track{
		{Title: "lofi", URL: "a"},
		{Title: "rock", URL: "b"},
		{Title: "lofi beats", URL: "c"},
	}
	return m, player
}

func resize(m Model, width int) Model {
	got, _ := m.update(tea.WindowSizeMsg{Width: width, Height: 40})
	return got.(Model)
}

// Input.

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func press(t *testing.T, m Model, k tea.KeyPressMsg) Model {
	t.Helper()
	got, _ := m.update(k)
	return got.(Model)
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = press(t, m, keyPress(string(r)))
	}
	return m
}

// openInfo presses i and delivers the stream info it fetches.
func openInfo(t *testing.T, m Model) Model {
	t.Helper()
	got, cmd := m.update(keyPress("i"))
	m = got.(Model)
	if cmd == nil {
		t.Fatal("i fetched no stream info")
	}
	got, _ = m.update(cmd())
	return got.(Model)
}

func cellAt(t *testing.T, m Model, text string) image.Point {
	t.Helper()
	for y, line := range strings.Split(rendered(m), "\n") {
		if before, _, ok := strings.Cut(line, text); ok {
			return image.Pt(lipgloss.Width(before), y)
		}
	}
	t.Fatalf("%q not rendered", text)
	return image.Point{}
}

func click(m Model, at image.Point) Model {
	next, _ := m.Update(tea.MouseClickMsg{X: at.X, Y: at.Y, Button: tea.MouseLeft})
	return next.(Model)
}

func wheel(m Model, at image.Point, button tea.MouseButton) Model {
	next, _ := m.Update(tea.MouseWheelMsg{X: at.X, Y: at.Y, Button: button})
	return next.(Model)
}

// Output.

func atPane(m Model, f focus) bool {
	return m.overlay == overlayNone && m.focus == f
}

func rendered(m Model) string {
	return ansi.Strip(m.render())
}

func assertFits(t *testing.T, m Model) {
	t.Helper()
	if w, h := lipgloss.Width(m.render()), lipgloss.Height(m.render()); w > m.width || h > m.height {
		t.Errorf("render() size = %dx%d, want within %dx%d", w, h, m.width, m.height)
	}
}

func footerHelp(m Model) string {
	lines := strings.Split(m.renderFooter(), "\n")
	return ansi.Strip(lines[len(lines)-1])
}

func helpEntry(b key.Binding) string {
	return b.Help().Key + " " + b.Help().Desc
}

func selectedTitle(m Model) string {
	tr, _ := m.selectedResult()
	return tr.Title
}

func filenames(entries []mpv.PlaylistEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Filename
	}
	return names
}

// Test doubles.

type spectrumStub struct{}

func (spectrumStub) Levels() <-chan []float64 { return nil }

type searchStub struct {
	tracks []youtube.Track
}

func (s searchStub) Search(context.Context, string, int) ([]youtube.Track, error) {
	return nil, errors.New("unexpected search")
}

func (s searchStub) Lookup(context.Context, string, int) ([]youtube.Track, error) {
	return s.tracks, nil
}

// spyPlayer records the commands the UI sends to mpv. Every command returns
// err; methods it does not override panic on the nil *mpv.Player.
type spyPlayer struct {
	*mpv.Player
	err      error
	info     mpv.StreamInfo
	playlist []mpv.PlaylistEntry

	calls   []string
	repeats []mpv.Repeat
	orders  [][]int
}

func (p *spyPlayer) record(call string) error {
	p.calls = append(p.calls, call)
	return p.err
}

func (p *spyPlayer) PlayNow(_ context.Context, url string) error { return p.record("play " + url) }
func (p *spyPlayer) Append(_ context.Context, url string) error  { return p.record("append " + url) }

func (p *spyPlayer) AppendAll(_ context.Context, urls []string) error {
	return p.record("append " + strings.Join(urls, ","))
}
func (p *spyPlayer) Stop(context.Context) error                { return p.record("stop") }
func (p *spyPlayer) TogglePause(context.Context) error         { return p.record("toggle pause") }
func (p *spyPlayer) Next(context.Context) error                { return p.record("next") }
func (p *spyPlayer) PlayIndex(context.Context, int) error      { return p.record("play index") }
func (p *spyPlayer) Seek(context.Context, time.Duration) error { return p.record("seek") }

func (p *spyPlayer) AddVolume(_ context.Context, delta int) error {
	if delta > 0 {
		return p.record("volume up")
	}
	return p.record("volume down")
}

func (p *spyPlayer) SetRepeat(_ context.Context, r mpv.Repeat) error {
	p.repeats = append(p.repeats, r)
	return p.record("set repeat")
}

func (p *spyPlayer) Reorder(_ context.Context, _ int, _ string, order []int) error {
	p.orders = append(p.orders, order)
	return p.record("reorder")
}

func (p *spyPlayer) Playlist(context.Context) ([]mpv.PlaylistEntry, int, error) {
	p.calls = append(p.calls, "playlist")
	return p.playlist, 1, nil
}

func (p *spyPlayer) StreamInfo(context.Context) (mpv.StreamInfo, error) { return p.info, nil }

// appendRecorder reports AppendAll calls on a channel so tests can observe
// the order of concurrent writes.
type appendRecorder struct {
	*mpv.Player
	writes chan string
}

func (p appendRecorder) AppendAll(_ context.Context, urls []string) error {
	p.writes <- urls[0]
	return nil
}

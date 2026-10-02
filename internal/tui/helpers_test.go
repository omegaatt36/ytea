package tui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/service"
)

const watchURL = "https://www.youtube.com/watch?v=abc"

// Model fixtures.

// overlayModel is a 120x40 model with one playing track, one saved playlist
// and one audio device, focused on origin with the search input blurred.
func overlayModel(t *testing.T, origin focus, opts ...func(*Deps)) Model {
	t.Helper()
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWithTracks("Keep", []domain.Track{{Title: "Kept", URL: watchURL}}); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Library: store, Player: &spyPlayer{info: domain.StreamInfo{Path: watchURL}}}
	for _, opt := range opts {
		opt(&deps)
	}
	m := New(deps)
	m.width, m.height = 120, 40
	m.input.Blur()
	m.focus = origin
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: watchURL, Title: "Song"}}
	m.core.Queue.Pos, m.core.Playback.Idle = 0, false
	m.core.Tracks[watchURL] = domain.Track{ID: "abc", Title: "Song", URL: watchURL}
	m.core.Devices = []domain.AudioDevice{{Name: "auto", Description: "Autoselect device"}}
	return m
}

func playingModel(g graphicsSupport) Model {
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = g
	m.width, m.height = 100, 30
	m.core.Playback.Idle, m.core.Queue.Pos = false, 0
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: watchURL}}
	m.core.Tracks[watchURL] = domain.Track{ID: "abc", Title: "Song", URL: watchURL}
	m.thumb.video = "abc"
	return m
}

func mouseModel() Model {
	m := New(Deps{})
	m.width, m.height = 100, 30
	for i := range 40 {
		m.core.Search.Tracks = append(m.core.Search.Tracks, domain.Track{Title: fmt.Sprintf("song %02d", i)})
	}
	for i := range 3 {
		m.core.Queue.Entries = append(m.core.Queue.Entries, domain.PlaylistEntry{Filename: fmt.Sprintf("u%d", i), Title: fmt.Sprintf("queued %d", i)})
	}
	return m
}

func filterModel() (Model, *spyPlayer) {
	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.width, m.height = 100, 30
	m.input.Blur()
	m.focus = focusResults
	m.core.Search.Tracks = []domain.Track{
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

func filenames(entries []domain.PlaylistEntry) []string {
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
	tracks []domain.Track
}

func (s searchStub) Search(context.Context, string, int, int) ([]domain.Track, error) {
	return nil, errors.New("unexpected search")
}

func (s searchStub) Lookup(context.Context, string, int) ([]domain.Track, error) {
	return s.tracks, nil
}

// Methods it does not override panic on the nil embedded service.Player.
type spyPlayer struct {
	service.Player
	err      error
	info     domain.StreamInfo
	playlist []domain.PlaylistEntry

	calls   []string
	repeats []domain.Repeat
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
func (p *spyPlayer) Move(context.Context, int, int) error      { return p.record("move") }
func (p *spyPlayer) TogglePause(context.Context) error         { return p.record("toggle pause") }
func (p *spyPlayer) Next(context.Context) error                { return p.record("next") }
func (p *spyPlayer) PlayIndex(context.Context, int) error      { return p.record("play index") }
func (p *spyPlayer) Seek(context.Context, time.Duration) error { return p.record("seek") }

func (p *spyPlayer) SeekTo(_ context.Context, pos time.Duration) error {
	return p.record("seek to " + pos.String())
}

func (p *spyPlayer) SeekPercent(_ context.Context, percent float64) error {
	return p.record(fmt.Sprintf("seek %g%%", percent))
}

func (p *spyPlayer) PlayAll(_ context.Context, urls []string, start int) error {
	return p.record(fmt.Sprintf("play all %s from %d", strings.Join(urls, ","), start))
}

func (p *spyPlayer) AddVolume(_ context.Context, delta int) error {
	if delta > 0 {
		return p.record("volume up")
	}
	return p.record("volume down")
}

func (p *spyPlayer) SetRepeat(_ context.Context, r domain.Repeat) error {
	p.repeats = append(p.repeats, r)
	return p.record("set repeat")
}

func (p *spyPlayer) Reorder(_ context.Context, _ int, _ string, order []int) error {
	p.orders = append(p.orders, order)
	return p.record("reorder")
}

func (p *spyPlayer) Playlist(context.Context) ([]domain.PlaylistEntry, int, error) {
	p.calls = append(p.calls, "playlist")
	return p.playlist, 1, nil
}

func (p *spyPlayer) StreamInfo(context.Context) (domain.StreamInfo, error) { return p.info, nil }

func (p *spyPlayer) Events() <-chan service.PlayerEvent {
	ch := make(chan service.PlayerEvent)
	close(ch)
	return ch
}

type spyMPRIS struct {
	updates []mpris.State
	seeked  []time.Duration
}

func (s *spyMPRIS) Update(st mpris.State) {
	s.updates = append(s.updates, st)
}

func (s *spyMPRIS) Seeked(pos time.Duration) {
	s.seeked = append(s.seeked, pos)
}

type spyLibrary struct {
	playlists []domain.Playlist
	err       error
}

func (s *spyLibrary) Playlists() []domain.Playlist {
	return s.playlists
}

func (s *spyLibrary) CreateWithTracks(name string, tracks []domain.Track) (int, error) {
	if s.err != nil {
		return -1, s.err
	}
	s.playlists = append(s.playlists, domain.Playlist{Name: name, Tracks: tracks})
	return len(s.playlists) - 1, nil
}

func (s *spyLibrary) Add(index int, track domain.Track) error {
	if s.err != nil {
		return s.err
	}
	if index < 0 || index >= len(s.playlists) {
		return library.ErrNotFound
	}
	s.playlists[index].Tracks = append(s.playlists[index].Tracks, track)
	return nil
}

func (s *spyLibrary) RemoveTrack(playlistIndex, trackIndex int) error {
	if s.err != nil {
		return s.err
	}
	if playlistIndex < 0 || playlistIndex >= len(s.playlists) || trackIndex < 0 || trackIndex >= len(s.playlists[playlistIndex].Tracks) {
		return library.ErrNotFound
	}
	s.playlists[playlistIndex].Tracks = append(s.playlists[playlistIndex].Tracks[:trackIndex], s.playlists[playlistIndex].Tracks[trackIndex+1:]...)
	return nil
}

func (s *spyLibrary) Rename(index int, name string) error {
	if s.err != nil {
		return s.err
	}
	if index < 0 || index >= len(s.playlists) {
		return library.ErrNotFound
	}
	s.playlists[index].Name = name
	return nil
}

func (s *spyLibrary) Move(from, to int) error {
	if s.err != nil {
		return s.err
	}
	p := s.playlists[from]
	s.playlists = slices.Insert(slices.Delete(s.playlists, from, from+1), to, p)
	return nil
}

func (s *spyLibrary) MoveTrack(playlistIndex, from, to int) error {
	if s.err != nil {
		return s.err
	}
	tracks := s.playlists[playlistIndex].Tracks
	t := tracks[from]
	s.playlists[playlistIndex].Tracks = slices.Insert(slices.Delete(tracks, from, from+1), to, t)
	return nil
}

func (s *spyLibrary) Delete(index int) error {
	if s.err != nil {
		return s.err
	}
	if index < 0 || index >= len(s.playlists) {
		return library.ErrNotFound
	}
	s.playlists = append(s.playlists[:index], s.playlists[index+1:]...)
	return nil
}

func pendingSearch(m *Model, query string) uint64 {
	m.core.Submit(query)
	return m.core.Active
}

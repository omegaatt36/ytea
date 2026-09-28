package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestCursorFollowsFocusedInput(t *testing.T) {
	const typed = "lofi beats"
	tests := []struct {
		name  string
		setup func(m *Model)
	}{
		{name: "search", setup: func(m *Model) {
			m.input.SetValue(typed)
			m.input.CursorEnd()
		}},
		{name: "results filter", setup: func(m *Model) {
			m.results.tracks = []youtube.Track{{Title: "song"}}
			m.focus = focusResults
			m.input.Blur()
			m.results.filter.Focus()
			m.results.filter.SetValue(typed)
			m.results.filter.CursorEnd()
		}},
		{name: "playlist name", setup: func(m *Model) {
			m.overlay = overlayName
			m.input.Blur()
			m.nameInput.Focus()
			m.nameInput.SetValue(typed)
			m.nameInput.CursorEnd()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, _ := New(Deps{}).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m := next.(Model)
			tt.setup(&m)

			c := m.View().Cursor
			if c == nil {
				t.Fatal("View().Cursor = nil, want the input's cursor")
			}
			lines := strings.Split(m.render(), "\n")
			if c.Y < 0 || c.Y >= len(lines) {
				t.Fatalf("cursor row %d outside the %d rendered rows", c.Y, len(lines))
			}
			if got := ansi.Strip(ansi.Cut(lines[c.Y], c.X-len(typed), c.X)); got != typed {
				t.Errorf("cells before cursor (%d,%d) = %q, want %q", c.X, c.Y, got, typed)
			}
		})
	}
}

func TestNoCursorOrBlinkOutsideInputs(t *testing.T) {
	m := New(Deps{})
	if cmd := m.focusSearch(); cmd != nil {
		t.Error("focusing search returned a command, want none: the terminal blinks the cursor")
	}
	m.focus = focusResults
	m.input.Blur()
	if c := m.View().Cursor; c != nil {
		t.Errorf("View().Cursor = %+v with a list focused, want nil", c)
	}
}

// textinput.Paste reads the system clipboard, so tests compare the command
// instead of running it.
func isClipboardRead(cmd tea.Cmd) bool {
	return cmd != nil && reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(textinput.Paste).Pointer()
}

// Bracketed paste lands in the focused text input, or in search from a list;
// the paste keys read the clipboard into search.
func TestPasteRouting(t *testing.T) {
	list := func(t *testing.T) Model {
		m := New(Deps{})
		m.focus = focusResults
		m.input.Blur()
		return m
	}
	naming := func(t *testing.T) Model {
		store, err := library.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		m := New(Deps{Library: store})
		m.focus = focusPlaylists
		m.input.Blur()
		return press(t, m, keyPress("c"))
	}
	tests := []struct {
		name       string
		model      func(*testing.T) Model
		msg        tea.Msg
		wantSearch string
		wantName   string
		wantRead   bool
	}{
		{name: "paste from a list", model: list, msg: tea.PasteMsg{Content: "joe hisaishi"}, wantSearch: "joe hisaishi"},
		{name: "paste while naming a playlist", model: naming, msg: tea.PasteMsg{Content: "Morning"}, wantName: "Morning"},
		{name: "ctrl+shift+v in search", model: func(*testing.T) Model { return New(Deps{}) }, msg: tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}, wantRead: true},
		{name: "ctrl+shift+v from a list", model: list, msg: tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}, wantRead: true},
		{name: "shift+insert from a list", model: list, msg: tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModShift}, wantRead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.model(t)
			naming := m.overlay == overlayName
			got, cmd := m.Update(tt.msg)
			m = got.(Model)
			if naming && m.overlay != overlayName || !naming && !atPane(m, focusSearch) {
				t.Errorf("focus=%v overlay=%v, want the input that was pasted into", m.focus, m.overlay)
			}
			if m.input.Value() != tt.wantSearch || m.nameInput.Value() != tt.wantName {
				t.Errorf("search=%q name=%q, want search=%q name=%q", m.input.Value(), m.nameInput.Value(), tt.wantSearch, tt.wantName)
			}
			if got := isClipboardRead(cmd); got != tt.wantRead {
				t.Errorf("clipboard read = %v, want %v", got, tt.wantRead)
			}
		})
	}
}

func TestKeysRunTheirActions(t *testing.T) {
	listModel := func(p *spyPlayer, f focus) Model {
		m := New(Deps{Player: p})
		m.input.Blur()
		m.focus = f
		m.results.tracks = []youtube.Track{{URL: "r0"}, {URL: "r1"}, {URL: "r2"}}
		m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
		return m
	}
	playerCall := func(want string) func(t *testing.T, m Model, cmd tea.Cmd, player *spyPlayer) {
		return func(t *testing.T, _ Model, cmd tea.Cmd, player *spyPlayer) {
			t.Helper()
			if cmd == nil {
				t.Fatal("cmd = nil, want a player command")
			}
			if msg := cmd(); msg != nil {
				t.Fatalf("player command returned %v", msg)
			}
			if !slices.Equal(player.calls, []string{want}) {
				t.Errorf("player calls = %v, want [%s]", player.calls, want)
			}
		}
	}
	resultCur := func(want int) func(t *testing.T, m Model, cmd tea.Cmd, player *spyPlayer) {
		return func(t *testing.T, m Model, _ tea.Cmd, _ *spyPlayer) {
			t.Helper()
			if m.results.cur != want {
				t.Errorf("resultCur = %d, want %d", m.results.cur, want)
			}
		}
	}
	inResults := func(_ *testing.T, p *spyPlayer) Model { return listModel(p, focusResults) }
	atLastResult := func(_ *testing.T, p *spyPlayer) Model {
		m := listModel(p, focusResults)
		m.results.cur = 2
		return m
	}

	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		setup func(t *testing.T, player *spyPlayer) Model
		check func(t *testing.T, m Model, cmd tea.Cmd, player *spyPlayer)
	}{
		{name: "space pauses", key: tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, setup: inResults, check: playerCall("toggle pause")},
		{name: "> skips to next track", key: keyPress(">"), setup: inResults, check: playerCall("next")},
		{name: "= raises volume", key: keyPress("="), setup: inResults, check: playerCall("volume up")},
		{name: "k moves the cursor up", key: keyPress("k"), setup: atLastResult, check: resultCur(1)},
		{name: "g jumps to the top", key: keyPress("g"), setup: atLastResult, check: resultCur(0)},
		{
			name: "x removes the selected queue entry",
			key:  keyPress("x"),
			setup: func(_ *testing.T, p *spyPlayer) Model {
				m := listModel(p, focusQueue)
				m.queueCur = 1
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyPlayer) {
				if got := filenames(m.queue.entries); !slices.Equal(got, []string{"A", "C"}) {
					t.Errorf("queue = %v, want [A C]", got)
				}
			},
		},
		{
			name: "tab leaves search",
			key:  tea.KeyPressMsg{Code: tea.KeyTab},
			setup: func(_ *testing.T, p *spyPlayer) Model {
				m := New(Deps{Player: p})
				m.input.SetValue("lofi")
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyPlayer) {
				if !atPane(m, focusResults) || m.input.Focused() {
					t.Errorf("focus = %v, input focused = %v, want results with search blurred", m.focus, m.input.Focused())
				}
				if v := m.input.Value(); v != "lofi" {
					t.Errorf("input = %q, want the query untouched", v)
				}
			},
		},
		{
			name: "y copies the URL from the info panel",
			key:  keyPress("y"),
			setup: func(t *testing.T, _ *spyPlayer) Model {
				return openInfo(t, overlayModel(t, focusQueue))
			},
			check: func(t *testing.T, _ Model, cmd tea.Cmd, _ *spyPlayer) {
				if cmd == nil {
					t.Fatal("cmd = nil, want a clipboard write")
				}
				if got, want := cmd(), tea.SetClipboard(watchURL)(); got != want {
					t.Errorf("cmd() = %#v, want clipboard write of %q", got, watchURL)
				}
			},
		},
		{
			name:  "q quits",
			key:   keyPress("q"),
			setup: inResults,
			check: func(t *testing.T, _ Model, cmd tea.Cmd, _ *spyPlayer) {
				if cmd == nil {
					t.Fatal("cmd = nil, want quit")
				}
				if msg := cmd(); msg != (tea.QuitMsg{}) {
					t.Errorf("cmd() = %#v, want tea.QuitMsg", msg)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &spyPlayer{}
			got, cmd := tt.setup(t, player).Update(tt.key)
			tt.check(t, got.(Model), cmd, player)
		})
	}
}

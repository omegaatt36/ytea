package tui

import (
	"fmt"
	"image"
	"regexp"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

// Column headings are tui-keymap-help R4's wording; "Navigation and global"
// appears nowhere else, so it marks the full help as shown.
const (
	playbackHeading = "Playback"
	globalHeading   = "Navigation and global"
)

func fullHelpShown(m Model) bool {
	return strings.Contains(rendered(m), globalHeading)
}

func enabledEntries(km help.KeyMap) []key.Binding {
	var entries []key.Binding
	for _, col := range km.FullHelp() {
		for _, b := range col {
			if b.Enabled() {
				entries = append(entries, b)
			}
		}
	}
	return entries
}

// Full help aligns keys and descriptions in columns, so any run of spaces may
// separate them; entries sit between spaces or the pane border.
func listsEntry(rendered string, b key.Binding) bool {
	re := regexp.MustCompile(`(?m)(^|[\s│])` + regexp.QuoteMeta(b.Help().Key) + ` +` + regexp.QuoteMeta(b.Help().Desc) + `([\s│]|$)`)
	return re.MatchString(rendered)
}

func helpContexts(t *testing.T) []struct {
	name  string
	model Model
	keys  help.KeyMap
} {
	t.Helper()
	at := func(f focus, o overlay) Model {
		m := overlayModel(t, f)
		m.overlay = o
		if f == focusSearch && o == overlayNone {
			m.focusSearch()
		}
		return m
	}
	filtered := at(focusResults, overlayNone)
	filtered.results.filter.SetValue("song")
	typing := at(focusResults, overlayNone)
	typing.results.filter.Focus()
	paged := at(focusResults, overlayNone)
	paged.results.query, paged.results.more = "song", true
	k := newKeyMap()
	// More is only offered once a search has a next page.
	onePage := k.results
	onePage.More.SetEnabled(false)
	unfiltered := onePage
	unfiltered.ClearFilter.SetEnabled(false)
	withMore := k.results
	withMore.ClearFilter.SetEnabled(false)
	return []struct {
		name  string
		model Model
		keys  help.KeyMap
	}{
		{"search", at(focusSearch, overlayNone), k.search},
		{"results", at(focusResults, overlayNone), unfiltered},
		{"results filtered", filtered, onePage},
		{"results with more", paged, withMore},
		{"results filter input", typing, k.filter},
		{"queue", at(focusQueue, overlayNone), k.queue},
		{"playlists", at(focusPlaylists, overlayNone), k.playlists},
		{"playlist tracks", at(focusPlaylistTracks, overlayNone), k.playlistTracks},
		{"history", at(focusHistory, overlayNone), k.history},
		{"device picker", at(focusQueue, overlayDevices), k.devices},
		{"track info", at(focusQueue, overlayInfo), k.info},
		{"playlist picker", at(focusQueue, overlayPicker), k.picker},
		{"playlist name", at(focusQueue, overlayName), k.name},
	}
}

func TestShortHelpListsContextActionsWithoutPlaybackKeys(t *testing.T) {
	playbackTokens := []string{"space", "←", "→", "n/p", "+/-"}
	for _, b := range newKeyMap().playback.ShortHelp() {
		playbackTokens = append(playbackTokens, helpEntry(b))
	}
	for _, tc := range helpContexts(t) {
		t.Run(tc.name, func(t *testing.T) {
			got := footerHelp(resize(tc.model, 500))
			if !strings.HasSuffix(got, "? more") {
				t.Errorf("help %q does not end with %q", got, "? more")
			}
			for _, b := range tc.keys.ShortHelp() {
				if b.Enabled() && !strings.Contains(got, helpEntry(b)) {
					t.Errorf("help %q is missing the context action %q", got, helpEntry(b))
				}
			}
			for _, tok := range playbackTokens {
				if strings.Contains(got, tok) {
					t.Errorf("help %q repeats the playback key %q", got, tok)
				}
			}
		})
	}
}

// queue-repeat-shuffle R6 [derived]: L and Z are listed only in the full help.
func TestShortHelpOmitsRepeatAndShuffle(t *testing.T) {
	m := overlayModel(t, focusQueue)
	footer := footerHelp(resize(m, 500))
	for _, b := range []key.Binding{m.keys.playback.Repeat, m.keys.queue.Shuffle} {
		if strings.Contains(footer, helpEntry(b)) {
			t.Errorf("Queue short help %q lists %q", footer, helpEntry(b))
		}
	}
}

func TestShortHelpTruncatesWholeEntriesAndKeepsMore(t *testing.T) {
	m := overlayModel(t, focusQueue)
	entries := m.keys.queue.ShortHelp()
	// bubbles renders its " …" tail only when it fits with a cell to spare.
	roomy := lipgloss.Width(" · ? more") + 3
	for width := 9; width <= 120; width++ {
		got := footerHelp(resize(m, width))
		if w := lipgloss.Width(got); w > width {
			t.Errorf("width %d: help %q is %d cells wide", width, got, w)
		}
		if !strings.HasSuffix(got, "? more") {
			t.Errorf("width %d: help %q does not end with %q", width, got, "? more")
		}
		before, _, truncated := strings.Cut(got, "…")
		if !truncated {
			if width < roomy || width == 40 {
				t.Errorf("width %d: help %q has no ellipsis for the dropped entries", width, got)
			}
			for _, b := range entries {
				if !strings.Contains(got, helpEntry(b)) {
					t.Errorf("width %d: help %q drops %q without an ellipsis", width, got, helpEntry(b))
				}
			}
			continue
		}
		kept := strings.TrimRight(before, " ·•")
		if kept != "" && width < roomy {
			t.Errorf("width %d: help %q unexpectedly fits a queue entry", width, got)
		}
		if kept != "" && !slices.ContainsFunc(entries, func(b key.Binding) bool {
			return strings.HasSuffix(kept, helpEntry(b))
		}) {
			t.Errorf("width %d: help %q cuts an entry: %q does not end with a whole queue entry", width, got, kept)
		}
	}
}

func TestVizBindingNeedsSpectrum(t *testing.T) {
	for _, tc := range []struct {
		name string
		tap  Spectrum
		want bool
	}{
		{"without spectrum", nil, false},
		{"with spectrum", spectrumStub{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(Deps{Tap: tc.tap})
			v := keyPress("v")
			if got := key.Matches(v, m.keys.global.Viz); got != tc.want {
				t.Errorf("v matches the viz binding = %v, want %v", got, tc.want)
			}
			entry := "v " + m.keys.global.Viz.Help().Desc
			if got := strings.Contains(ansi.Strip(help.New().View(m.keys.global)), entry); got != tc.want {
				t.Errorf("global help contains %q = %v, want %v", entry, got, tc.want)
			}

			m = resize(m, 120)
			m.input.Blur()
			m.focus = focusResults
			if got := listsEntry(rendered(press(t, m, keyPress("?"))), m.keys.global.Viz); got != tc.want {
				t.Errorf("full help lists %q = %v, want %v", entry, got, tc.want)
			}

			m.showViz = true
			got, _ := m.update(v)
			if toggled := !got.(Model).showViz; toggled != tc.want {
				t.Errorf("v toggled the spectrum = %v, want %v", toggled, tc.want)
			}
		})
	}
	vEntry := regexp.MustCompile(`(^|\s)v \pL`)
	for _, tc := range helpContexts(t) {
		t.Run("no v in "+tc.name, func(t *testing.T) {
			if got := footerHelp(resize(tc.model, 500)); vEntry.MatchString(got) {
				t.Errorf("help %q shows v without a spectrum tap", got)
			}
		})
	}
}

func TestInlineKeyHintsUseKeymapOrAreAbsent(t *testing.T) {
	customBinding := func(keyName, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(keyName), key.WithHelp(helpKey, description))
	}
	blank := func(f focus) Model {
		m := New(Deps{})
		m.width, m.height = 120, 40
		m.input.Blur()
		m.focus = f
		return m
	}
	tests := []struct {
		name  string
		model func(*testing.T) Model

		duplicateHints []string
		wantHints      []string
		wantBindings   func(Model) []key.Binding
	}{
		{
			name: "empty playlist list",
			model: func(_ *testing.T) Model {
				m := blank(focusPlaylists)
				m.keys.playlists.Create = customBinding("x", "x", "new playlist")
				return m
			},

			duplicateHints: []string{"press c to create"},
			wantHints:      []string{"press x to create"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.playlists.Create} },
		},
		{
			name: "empty search results",
			model: func(_ *testing.T) Model {
				m := blank(focusResults)
				m.queue.entries = []mpv.PlaylistEntry{{Filename: "queued"}}
				m.keys.global.Search = customBinding("x", "x", "search")
				return m
			},

			duplicateHints: []string{"press / to search"},
			wantHints:      []string{"press x to search"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.results.Enqueue} },
		},
		{
			name: "empty queue",
			model: func(_ *testing.T) Model {
				m := blank(focusResults)
				m.results.tracks = []youtube.Track{{Title: "Result"}}
				m.keys.results.Enqueue = customBinding("x", "x", "queue")
				return m
			},

			duplicateHints: []string{"empty — press a on a result"},
			wantHints:      []string{"empty — press x on a result"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.results.Enqueue} },
		},
		{
			name: "playlist picker",
			model: func(t *testing.T) Model {
				m := overlayModel(t, focusQueue)
				m.overlay = overlayPicker
				m.keys.picker.Save = customBinding("x", "x", "save track")
				return m
			},

			duplicateHints: []string{"Save to playlist — enter to select"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.picker.Save} },
		},
		{
			name: "playlist name",
			model: func(t *testing.T) Model {
				m := overlayModel(t, focusQueue)
				m.overlay = overlayName
				m.keys.name.Create = customBinding("x", "x", "create playlist")
				m.keys.name.Cancel = customBinding("z", "z", "cancel")
				return m
			},

			duplicateHints: []string{"New playlist — enter to save, esc to cancel"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.name.Create, m.keys.name.Cancel} },
		},
		{
			name: "device picker",
			model: func(t *testing.T) Model {
				m := overlayModel(t, focusQueue)
				m.overlay = overlayDevices
				m.keys.devices.Select = customBinding("x", "x", "switch")
				m.keys.devices.Close = customBinding("z", "z", "cancel")
				return m
			},

			duplicateHints: []string{"Output device — enter to switch, esc to cancel"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.devices.Select, m.keys.devices.Close} },
		},
		{
			name: "track info",
			model: func(t *testing.T) Model {
				m := overlayModel(t, focusQueue)
				m.overlay = overlayInfo
				m.keys.info.Open = customBinding("w", "w", "open in browser")
				m.keys.info.Copy = customBinding("x", "x", "copy url")
				m.keys.info.Close = customBinding("z", "z", "close")
				return m
			},

			duplicateHints: []string{"Track info — y copy URL, esc to close"},
			wantBindings: func(m Model) []key.Binding {
				return []key.Binding{m.keys.info.Open, m.keys.info.Copy, m.keys.info.Close}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.model(t)
			got := rendered(m)

			for _, hint := range tt.duplicateHints {
				if strings.Contains(got, hint) {
					t.Errorf("render() still contains duplicate key hint %q", hint)
				}
			}
			for _, hint := range tt.wantHints {
				if !strings.Contains(got, hint) {
					t.Errorf("render() lacks keymap-derived hint %q", hint)
				}
			}
			for _, b := range tt.wantBindings(m) {
				if !strings.Contains(footerHelp(m), helpEntry(b)) {
					t.Errorf("context help %q does not include keymap binding %q", footerHelp(m), helpEntry(b))
				}
			}
		})
	}

	t.Run("delete confirmation uses keymap help label", func(t *testing.T) {
		store, err := library.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Create("Keep"); err != nil {
			t.Fatal(err)
		}
		m := New(Deps{Library: store})
		m.input.Blur()
		m.focus = focusPlaylists
		m.keys.playlists.Delete = customBinding("x", "alt+x", "delete playlist")
		m = press(t, m, keyPress("x"))
		want := "press " + m.keys.playlists.Delete.Help().Key + " again to delete " + quote("Keep")
		if m.status != want {
			t.Errorf("delete confirmation = %q, want %q", m.status, want)
		}
		if m.deletePlaylistPending != 0 {
			t.Errorf("deletePlaylistPending = %d, want 0 after first press", m.deletePlaylistPending)
		}
	})
}

func TestFullHelpShowsPlaybackGlobalAndContextColumns(t *testing.T) {
	k := newKeyMap()
	for _, tc := range helpContexts(t) {
		if tc.name == "search" || tc.name == "playlist name" || tc.name == "results filter input" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model
			if fullHelpShown(m) {
				t.Fatalf("full help shown before ?")
			}
			m = press(t, m, keyPress("?"))
			got := rendered(m)
			bodyHeight := m.screen().body.Dy()
			helpBody := ansi.Strip(m.renderFullHelp(bodyHeight))
			for _, heading := range []string{playbackHeading, globalHeading} {
				if !strings.Contains(got, heading) {
					t.Errorf("full help is missing the %q column", heading)
				}
			}
			var want []key.Binding
			want = append(want, enabledEntries(k.playback)...)
			want = append(want, k.global.Search, k.global.NextPane)
			want = append(want, enabledEntries(tc.keys)...)
			for _, b := range want {
				if !listsEntry(got, b) {
					t.Errorf("full help is missing %q:\n%s", helpEntry(b), got)
				}
			}
			for _, b := range []key.Binding{m.keys.closeHelp, m.keys.global.ForceQuit} {
				if !listsEntry(helpBody, b) {
					t.Errorf("full help is missing R5 binding %q:\n%s", helpEntry(b), got)
				}
			}
			if got := footerHelp(m); got != "?/esc/q close" {
				t.Errorf("full-help footer = %q, want %q", got, "?/esc/q close")
			}
		})
	}
}

// The visualizer, now-playing box and status live in the player bar, so full
// help keeps them and still lists every group in a short or narrow window.
// At 40 columns the spectrum has already given way to the track details.
func TestFullHelpFitsSmallWindows(t *testing.T) {
	for _, tt := range []struct {
		size image.Point
		viz  bool
	}{
		{image.Pt(120, 24), true},
		{image.Pt(40, 24), false},
	} {
		size := tt.size
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			m := New(Deps{Tap: spectrumStub{}})
			m.input.Blur()
			m.focus = focusQueue
			m.levels = []float64{1}
			m.queue.entries = []mpv.PlaylistEntry{{Filename: watchURL, Title: "Song"}}
			m.queue.pos, m.player.idle = 0, false
			m.tracks[watchURL] = youtube.Track{ID: "abc", Title: "Song", URL: watchURL}
			m.setStatus("status line text")
			got, _ := m.update(tea.WindowSizeMsg{Width: size.X, Height: size.Y})
			m = press(t, got.(Model), keyPress("?"))
			if !fullHelpShown(m) {
				t.Fatal("? did not open the full help")
			}
			assertFits(t, m)
			screen := rendered(m)
			for _, text := range []string{"▶ Song", "status line text"} {
				if !strings.Contains(screen, text) {
					t.Errorf("full help dropped %q from the player bar", text)
				}
			}
			if got := strings.Contains(screen, "█"); got != tt.viz {
				t.Errorf("visualizer shown = %v with full help open, want %v", got, tt.viz)
			}
			global := m.keys.global
			global.Quit = m.keys.closeHelp
			for _, group := range []struct {
				title string
				keys  help.KeyMap
			}{
				{playbackHeading, m.keys.playback},
				{globalHeading, global},
				{"Queue", m.keys.queue},
			} {
				if !strings.Contains(screen, group.title) {
					t.Errorf("full help is missing the %q heading", group.title)
				}
				for _, b := range enabledEntries(group.keys) {
					if !listsEntry(screen, b) {
						t.Errorf("full help is missing %s binding %q", group.title, helpEntry(b))
					}
				}
			}

			m = press(t, m, keyPress("q"))
			if fullHelpShown(m) || !m.showViz || strings.Contains(rendered(m), "█") != tt.viz {
				t.Errorf("after q: help=%v showViz=%v, want the player bar unchanged without full help", fullHelpShown(m), m.showViz)
			}
		})
	}
}

func TestFullHelpClosesWithQuestionMarkOrEsc(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{keyPress("?"), keyPress("esc")} {
		t.Run(k.String(), func(t *testing.T) {
			m := press(t, overlayModel(t, focusQueue), keyPress("?"))
			if !fullHelpShown(m) {
				t.Fatal("? did not open the full help")
			}
			m = press(t, m, k)
			if fullHelpShown(m) {
				t.Errorf("full help still shown after %s", k)
			}
			if !atPane(m, focusQueue) {
				t.Errorf("after %s: focus=%v overlay=%v, want queue", k, m.focus, m.overlay)
			}
			if !strings.Contains(rendered(m), "Queue (1)") {
				t.Errorf("queue pane not restored after %s", k)
			}
		})
	}
}

func TestFullHelpReplacesListPanesKeepsNowPlayingAndStatus(t *testing.T) {
	m := overlayModel(t, focusResults)
	m.results.tracks = []youtube.Track{{Title: "A search result"}}
	m.setStatus("status line text")
	before := rendered(m)
	for _, s := range []string{"Queue (1)", "A search result"} {
		if !strings.Contains(before, s) {
			t.Fatalf("list panes do not render %q before ?", s)
		}
	}

	m = press(t, m, keyPress("?"))
	got := rendered(m)
	for _, s := range []string{"Queue (1)", "A search result"} {
		if strings.Contains(got, s) {
			t.Errorf("full help still renders the list pane content %q", s)
		}
	}
	for _, s := range []string{"▶ Song", "status line text"} {
		if !strings.Contains(got, s) {
			t.Errorf("full help dropped %q (now-playing box / status line)", s)
		}
	}
}

func TestQuestionMarkInInputIsLiteralNotFullHelp(t *testing.T) {
	m := overlayModel(t, focusQueue)
	m.focusSearch()
	m = press(t, m, keyPress("?"))
	if v := m.input.Value(); v != "?" {
		t.Errorf("search input = %q after typing ?, want %q", v, "?")
	}
	if fullHelpShown(m) {
		t.Error("? in the search input opened the full help")
	}

	m = press(t, overlayModel(t, focusPlaylists), keyPress("c"))
	if m.overlay != overlayName {
		t.Fatalf("overlay = %v, want name input", m.overlay)
	}
	m = press(t, m, keyPress("?"))
	if v := m.nameInput.Value(); v != "?" {
		t.Errorf("name input = %q after typing ?, want %q", v, "?")
	}
	if fullHelpShown(m) {
		t.Error("? in the playlist name input opened the full help")
	}
}

func TestFullHelpAppliesPlaybackKeysAndIgnoresPaneKeys(t *testing.T) {
	m := overlayModel(t, focusQueue)
	player := m.deps.Player.(*spyPlayer)
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m = press(t, m, keyPress("?"))

	got, cmd := m.update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = got.(Model)
	if cmd == nil {
		t.Fatal("space: cmd = nil, want a player command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("space: player command returned %v", msg)
	}
	if !slices.Equal(player.calls, []string{"toggle pause"}) {
		t.Errorf("player calls = %v, want [toggle pause]", player.calls)
	}
	if !fullHelpShown(m) {
		t.Error("space closed the full help")
	}

	got, cmd = m.update(keyPress("d"))
	m = got.(Model)
	if cmd != nil || len(m.queue.entries) != 3 {
		t.Errorf("d with full help open: cmd=%v, queue=%v, want no removal", cmd != nil, m.queue.entries)
	}
	if !fullHelpShown(m) {
		t.Error("d closed the full help")
	}
}

func TestFullHelpIgnoresGlobalKeysButForceQuit(t *testing.T) {
	ignored := []tea.KeyPressMsg{
		{Code: tea.KeyTab},
		{Code: tea.KeyTab, Mod: tea.ModShift},
		keyPress("/"),
		keyPress("o"),
		keyPress("i"),
		keyPress("r"),
		{Code: 'v', Mod: tea.ModCtrl},
	}
	for _, k := range ignored {
		t.Run(k.String(), func(t *testing.T) {
			m := press(t, overlayModel(t, focusQueue), keyPress("?"))
			got, cmd := m.update(k)
			m = got.(Model)
			if cmd != nil {
				t.Errorf("%s returned a command with full help open", k)
			}
			if !atPane(m, focusQueue) || !fullHelpShown(m) {
				t.Errorf("after %s: focus=%v overlay=%v help=%v, want queue with full help", k, m.focus, m.overlay, fullHelpShown(m))
			}
		})
	}

	t.Run("bracketed paste", func(t *testing.T) {
		m := press(t, overlayModel(t, focusQueue), keyPress("?"))
		got, _ := m.update(tea.PasteMsg{Content: "lofi"})
		m = got.(Model)
		if m.input.Value() != "" || !atPane(m, focusQueue) || !fullHelpShown(m) {
			t.Errorf("paste: input=%q focus=%v help=%v, want ignored", m.input.Value(), m.focus, fullHelpShown(m))
		}
	})

	t.Run("q closes without quitting", func(t *testing.T) {
		m := press(t, overlayModel(t, focusQueue), keyPress("?"))
		got, cmd := m.update(keyPress("q"))
		m = got.(Model)
		if cmd != nil {
			t.Errorf("q returned %#v, want no command", cmd())
		}
		if fullHelpShown(m) || !atPane(m, focusQueue) {
			t.Errorf("after q: help=%v focus=%v overlay=%v, want queue without full help", fullHelpShown(m), m.focus, m.overlay)
		}
	})

	t.Run("ctrl+c quits", func(t *testing.T) {
		m := press(t, overlayModel(t, focusQueue), keyPress("?"))
		_, cmd := m.update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		if cmd == nil {
			t.Fatal("ctrl+c: cmd = nil, want quit")
		}
		if msg := cmd(); msg != (tea.QuitMsg{}) {
			t.Errorf("ctrl+c: cmd() = %#v, want tea.QuitMsg", msg)
		}
	})
}

func TestFullHelpOverOverlayReturnsToIt(t *testing.T) {
	tests := []struct {
		name  string
		open  func(t *testing.T, m Model) Model
		want  overlay
		shown string
	}{
		{"device picker", func(_ *testing.T, m Model) Model {
			got, _ := m.update(devicesMsg{devices: m.devices, open: true})
			return got.(Model)
		}, overlayDevices, "Autoselect device"},
		{"track info", openInfo, overlayInfo, "copy url"},
		{"playlist picker", func(t *testing.T, m Model) Model { return press(t, m, keyPress("s")) }, overlayPicker, "Save to playlist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.open(t, overlayModel(t, focusQueue))
			if m.overlay != tt.want {
				t.Fatalf("overlay = %v, want %v", m.overlay, tt.want)
			}
			m = press(t, m, keyPress("?"))
			if !fullHelpShown(m) {
				t.Fatal("? did not open the full help over the overlay")
			}
			m = press(t, m, keyPress("esc"))
			if fullHelpShown(m) || m.overlay != tt.want || m.focus != focusQueue {
				t.Errorf("after esc: help=%v overlay=%v focus=%v, want %v over queue", fullHelpShown(m), m.overlay, m.focus, tt.want)
			}
			if !strings.Contains(rendered(m), tt.shown) {
				t.Errorf("overlay body %q not restored", tt.shown)
			}
		})
	}
}

func TestFullHelpIgnoresMouse(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	row := cellAt(t, m, "queued 1")
	tab := cellAt(t, m, "Queue")
	searchBox := image.Pt(lipgloss.Width(brand())+1, 0)
	m = press(t, m, keyPress("?"))

	for name, at := range map[string]image.Point{"pane row": row, "tab": tab, "search": searchBox} {
		got := click(m, at)
		if !atPane(got, focusResults) || got.queueCur != 0 || !fullHelpShown(got) {
			t.Errorf("click on %s: focus=%v queueCur=%d help=%v, want ignored", name, got.focus, got.queueCur, fullHelpShown(got))
		}
	}
	got := wheel(m, row, tea.MouseWheelDown)
	if !atPane(got, focusResults) || got.results.cur != 0 || got.queueCur != 0 {
		t.Errorf("wheel: focus=%v resultCur=%d queueCur=%d, want ignored", got.focus, got.results.cur, got.queueCur)
	}
}

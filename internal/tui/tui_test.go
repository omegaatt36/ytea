package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math/rand/v2"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/thumbnail"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "unknown", in: 0, want: "--:--"},
		{name: "minutes", in: 3*time.Minute + 5*time.Second, want: "3:05"},
		{name: "rounds", in: 59*time.Second + 600*time.Millisecond, want: "1:00"},
		{name: "hours", in: 8*time.Hour + 28*time.Second, want: "8:00:28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.in); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNextThumbIDStaysInPaletteRange(t *testing.T) {
	tests := []struct {
		prev, want int
	}{
		{prev: 0, want: 16},
		{prev: 16, want: 17},
		{prev: 254, want: 255},
		{prev: 255, want: 16},
	}
	for _, tt := range tests {
		if got := nextThumbID(tt.prev); got != tt.want {
			t.Errorf("nextThumbID(%d) = %d, want %d", tt.prev, got, tt.want)
		}
	}
}

func TestApplyPlaylistPos(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "index", data: "2", want: 2},
		{name: "none selected", data: "-1", want: -1},
		{name: "unavailable", data: "null", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.applyProperty(mpv.Event{Name: "property-change", Prop: mpv.PropPlaylistPos, Data: json.RawMessage(tt.data)})
			if m.queue.pos != tt.want {
				t.Errorf("pos = %d, want %d", m.queue.pos, tt.want)
			}
		})
	}
}

func TestApplyAFTracksNormalize(t *testing.T) {
	m := New(Deps{})
	on := `[{"label":"norm","name":"lavfi"}]`
	m.applyProperty(mpv.Event{Prop: mpv.PropAF, Data: json.RawMessage(on)})
	if !m.normalize {
		t.Fatal("normalize = false after norm filter added, want true")
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropAF, Data: json.RawMessage(`[]`)})
	if m.normalize {
		t.Fatal("normalize = true after filters cleared, want false")
	}
}

func TestWaitMPVDropsTimePosWithinShownSecond(t *testing.T) {
	timePos := func(data string) mpv.Event {
		return mpv.Event{Name: "property-change", Prop: mpv.PropTimePos, Data: json.RawMessage(data)}
	}
	tests := []struct {
		name   string
		shown  time.Duration
		events []mpv.Event
		want   tea.Msg
	}{
		{
			name:   "same second is dropped",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.2"), timePos("12.4"), timePos("12.6")},
			want:   mpvEventMsg(timePos("12.6")),
		},
		{
			name:   "backward seek into a new second",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("3.1")},
			want:   mpvEventMsg(timePos("3.1")),
		},
		{
			name:   "unavailable",
			shown:  0,
			events: []mpv.Event{timePos("null")},
			want:   mpvEventMsg(timePos("null")),
		},
		{
			name:   "other events pass",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.1"), {Name: "playback-restart"}},
			want:   mpvEventMsg(mpv.Event{Name: "playback-restart"}),
		},
		{
			name:   "closed after dropped events",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.1")},
			want:   mpvClosedMsg{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan mpv.Event, len(tt.events))
			for _, ev := range tt.events {
				ch <- ev
			}
			close(ch)
			got := waitMPV(ch, tt.shown)()
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("waitMPV() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
			m.results = []youtube.Track{{Title: "song"}}
			m.focus = focusResults
			m.input.Blur()
			m.filterInput.Focus()
			m.filterInput.SetValue(typed)
			m.filterInput.CursorEnd()
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

func atPane(m Model, f focus) bool {
	return m.overlay == overlayNone && m.focus == f
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

func TestRenderSpectrumSize(t *testing.T) {
	m := New(Deps{})
	m.levels = []float64{0, 0.25, 0.5, 1}
	got := m.renderSpectrum(40, vizRows)
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w != 40 || h != vizRows {
		t.Errorf("renderSpectrum() size = %dx%d, want 40x%d", w, h, vizRows)
	}
}

func TestRenderFitsWindow(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 100, 30
	m.showViz = true
	got := m.render()
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w > 100 || h > 30 {
		t.Errorf("render() size = %dx%d, want within 100x30", w, h)
	}
}

type spectrumStub struct{}

func (spectrumStub) Levels() <-chan []float64 { return nil }

func resize(m Model, width int) Model {
	got, _ := m.update(tea.WindowSizeMsg{Width: width, Height: 40})
	return got.(Model)
}

func footerHelp(m Model) string {
	lines := strings.Split(m.renderFooter(), "\n")
	return ansi.Strip(lines[len(lines)-1])
}

func helpEntry(b key.Binding) string {
	return b.Help().Key + " " + b.Help().Desc
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
	filtered.filterInput.SetValue("song")
	typing := at(focusResults, overlayNone)
	typing.filterInput.Focus()
	k := newKeyMap()
	unfiltered := k.results
	unfiltered.ClearFilter.SetEnabled(false)
	return []struct {
		name  string
		model Model
		keys  help.KeyMap
	}{
		{"search", at(focusSearch, overlayNone), k.search},
		{"results", at(focusResults, overlayNone), unfiltered},
		{"results filtered", filtered, k.results},
		{"results filter input", typing, k.filter},
		{"queue", at(focusQueue, overlayNone), k.queue},
		{"playlists", at(focusPlaylists, overlayNone), k.playlists},
		{"playlist tracks", at(focusPlaylistTracks, overlayNone), k.playlistTracks},
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

func TestShortHelpTruncatesWholeEntriesAndKeepsMore(t *testing.T) {
	m := overlayModel(t, focusQueue)
	entries := m.keys.queue.ShortHelp()
	// bubbles renders its " …" tail only when it fits with a cell to spare.
	for width := lipgloss.Width(" · ? more") + 3; width <= 120; width++ {
		got := footerHelp(resize(m, width))
		if w := lipgloss.Width(got); w > width {
			t.Errorf("width %d: help %q is %d cells wide", width, got, w)
		}
		if !strings.HasSuffix(got, "? more") {
			t.Errorf("width %d: help %q does not end with %q", width, got, "? more")
		}
		before, _, truncated := strings.Cut(got, "…")
		if !truncated {
			for _, b := range entries {
				if !strings.Contains(got, helpEntry(b)) {
					t.Errorf("width %d: help %q drops %q without an ellipsis", width, got, helpEntry(b))
				}
			}
			continue
		}
		kept := strings.TrimRight(before, " ·•")
		if kept != "" && !slices.ContainsFunc(entries, func(b key.Binding) bool {
			return strings.HasSuffix(kept, helpEntry(b))
		}) {
			t.Errorf("width %d: help %q cuts an entry: %q does not end with a whole queue entry", width, got, kept)
		}
	}
	if got := footerHelp(resize(m, 40)); !strings.Contains(got, "…") {
		t.Errorf("width 40: help %q has no ellipsis for the dropped entries", got)
	}
}

func TestShortHelpKeepsMoreAndEllipsisAtNarrowWidths(t *testing.T) {
	m := overlayModel(t, focusQueue)
	for width := 9; width <= 11; width++ {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			got := footerHelp(resize(m, width))
			if w := lipgloss.Width(got); w > width {
				t.Errorf("help %q is %d cells wide, want at most %d", got, w, width)
			}
			if !strings.HasSuffix(got, "? more") {
				t.Errorf("help %q does not keep %q visible", got, "? more")
			}
			if !strings.Contains(got, "…") {
				t.Errorf("help %q has no ellipsis for the omitted queue entries", got)
			}
			for _, b := range m.keys.queue.ShortHelp() {
				if strings.Contains(got, helpEntry(b)) {
					t.Errorf("help %q unexpectedly fits queue entry %q at width %d", got, helpEntry(b), width)
				}
			}
		})
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

			m.input.Blur()
			m.focus = focusResults
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

func TestQuestionMarkTypesIntoInputs(t *testing.T) {
	m := New(Deps{})
	got, _ := m.update(keyPress("?"))
	if v := got.(Model).input.Value(); v != "?" {
		t.Errorf("search input = %q after typing ?, want %q", v, "?")
	}

	m = overlayModel(t, focusPlaylists)
	m = press(t, m, keyPress("c"))
	if m.overlay != overlayName {
		t.Fatalf("overlay = %v, want name input", m.overlay)
	}
	got, _ = m.update(keyPress("?"))
	if v := got.(Model).nameInput.Value(); v != "?" {
		t.Errorf("name input = %q after typing ?, want %q", v, "?")
	}
}

func TestLocalPlaylistFlow(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := youtube.Track{ID: "one", Title: "First", URL: "https://www.youtube.com/watch?v=one"}
	second := youtube.Track{ID: "two", Title: "Second", URL: "https://www.youtube.com/watch?v=two"}
	m := New(Deps{Library: store})
	m.focus = focusResults
	m.results = []youtube.Track{first, second}
	got, _ := m.handleResultKey(keyPress("s"))
	m = got.(Model)
	if m.overlay != overlayName {
		t.Fatalf("save without lists: overlay=%v, want name input", m.overlay)
	}
	m.nameInput.SetValue("Favorites")
	got, _ = m.handlePlaylistNameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	if !atPane(m, focusResults) || len(store.Playlists()) != 1 || len(store.Playlists()[0].Tracks) != 1 {
		t.Fatalf("created playlist and saved song: focus=%v overlay=%v playlists=%+v", m.focus, m.overlay, store.Playlists())
	}
	m.resultCur = 1
	got, _ = m.handleResultKey(keyPress("s"))
	m = got.(Model)
	if m.overlay != overlayPicker {
		t.Fatalf("save with existing list: overlay=%v, want picker", m.overlay)
	}
	got, _ = m.handlePlaylistKey(keyPress("enter"))
	m = got.(Model)
	if !atPane(m, focusResults) || len(store.Playlists()[0].Tracks) != 2 {
		t.Fatalf("saved second song: focus=%v overlay=%v playlists=%+v", m.focus, m.overlay, store.Playlists())
	}
	m.cycleTab(false)
	m.cycleTab(false)
	if !atPane(m, focusPlaylists) {
		t.Fatalf("two tabs from results: focus=%v overlay=%v", m.focus, m.overlay)
	}
	got, _ = m.handlePlaylistKey(keyPress("enter"))
	m = got.(Model)
	if !atPane(m, focusPlaylistTracks) {
		t.Fatalf("enter list: focus=%v overlay=%v", m.focus, m.overlay)
	}
	m.playlistTrackCur = 1
	got, _ = m.handlePlaylistKey(keyPress("d"))
	m = got.(Model)
	if len(store.Playlists()[0].Tracks) != 1 || store.Playlists()[0].Tracks[0].URL != first.URL {
		t.Fatalf("after removal = %+v", store.Playlists())
	}
	m.width, m.height = 80, 24
	if w, h := lipgloss.Width(m.render()), lipgloss.Height(m.render()); w > 80 || h > 24 {
		t.Errorf("playlist render size = %dx%d", w, h)
	}
}

type spyPlaylistPlayer struct {
	*mpv.Player
	calls []string
	err   error
}

func (p *spyPlaylistPlayer) PlayNow(_ context.Context, url string) error {
	p.calls = append(p.calls, "play "+url)
	return p.err
}

func (p *spyPlaylistPlayer) Append(_ context.Context, url string) error {
	p.calls = append(p.calls, "append "+url)
	return p.err
}

func (p *spyPlaylistPlayer) AppendAll(_ context.Context, urls []string) error {
	p.calls = append(p.calls, "append "+strings.Join(urls, ","))
	return p.err
}

func (p *spyPlaylistPlayer) Stop(context.Context) error {
	p.calls = append(p.calls, "stop")
	return p.err
}

func TestPlaylistTrackKeyRunsPlayerCommand(t *testing.T) {
	for _, tt := range []struct {
		name       string
		key        rune
		wantCall   string
		wantStatus string
		projected  bool
		err        error
	}{
		{name: "play", key: tea.KeyEnter, wantCall: "play second", wantStatus: "playing", projected: true},
		{name: "append", key: 'a', wantCall: "append second", wantStatus: "queued"},
		{name: "append failure", key: 'a', wantCall: "append second", err: errors.New("mpv rejected append")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := library.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tracks := []youtube.Track{{URL: "first", Title: "First"}, {URL: "second", Title: "Second"}}
			if _, err := store.CreateWithTracks("Favorites", tracks); err != nil {
				t.Fatal(err)
			}
			player := &spyPlaylistPlayer{err: tt.err}
			m := New(Deps{Library: store, Player: player})
			m.focus = focusPlaylistTracks
			m.playlistTrackCur = 1

			got, cmd := m.update(tea.KeyPressMsg{Code: tt.key})
			m = got.(Model)
			if cmd == nil {
				t.Fatal("playlist key did not schedule a player command")
			}
			if len(player.calls) != 0 {
				t.Fatalf("player was called before the command ran: %v", player.calls)
			}
			if m.tracks["second"].Title != "Second" {
				t.Fatalf("selected track metadata was lost: %+v", m.tracks["second"])
			}

			result := cmd()
			done, ok := result.(queueActionDoneMsg)
			if !ok {
				t.Fatalf("command result has type %T, want queueActionDoneMsg", result)
			}
			if len(player.calls) != 1 || player.calls[0] != tt.wantCall {
				t.Fatalf("player calls = %v, want [%s]", player.calls, tt.wantCall)
			}
			if done.projected != tt.projected || !errors.Is(done.err, tt.err) {
				t.Fatalf("command result = %+v, want projected=%v error=%v", done, tt.projected, tt.err)
			}
			got, _ = m.update(done)
			m = got.(Model)
			if tt.err != nil {
				if !m.statusErr || m.status != tt.err.Error() {
					t.Fatalf("failed command status = %q (error=%v)", m.status, m.statusErr)
				}
			} else if m.statusErr || !strings.Contains(m.status, tt.wantStatus) {
				t.Fatalf("command status = %q (error=%v), want %q", m.status, m.statusErr, tt.wantStatus)
			}
		})
	}
}

func TestPlaylistNamePasteStaysInNameInput(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusPlaylists
	got, _ := m.handlePlaylistKey(keyPress("c"))
	m = got.(Model)
	got, _ = m.Update(tea.PasteMsg{Content: "Morning"})
	m = got.(Model)
	if m.overlay != overlayName || m.nameInput.Value() != "Morning" || m.input.Value() != "" {
		t.Errorf("paste routing: overlay=%v name=%q search=%q", m.overlay, m.nameInput.Value(), m.input.Value())
	}
}

func TestSaveQueueAsPlaylist(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusQueue
	firstURL := "https://www.youtube.com/watch?v=one"
	secondURL := "https://www.youtube.com/watch?v=two"
	m.queue.entries = []mpv.PlaylistEntry{
		{Filename: firstURL, Title: "First"},
		{Filename: secondURL, Title: "Second"},
		{Filename: firstURL, Title: "First"},
	}
	got, _ := m.handleQueueKey(keyPress("S"))
	m = got.(Model)
	if m.overlay != overlayName {
		t.Fatalf("save queue: overlay=%v, want name input", m.overlay)
	}
	m.nameInput.SetValue("Imported")
	got, _ = m.handlePlaylistNameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	playlists := store.Playlists()
	if !atPane(m, focusPlaylists) || len(playlists) != 1 || len(playlists[0].Tracks) != 2 || playlists[0].Tracks[0].URL != firstURL || playlists[0].Tracks[1].URL != secondURL {
		t.Fatalf("saved queue = %+v, focus=%v overlay=%v", playlists, m.focus, m.overlay)
	}
}

func TestDeletePlaylistNeedsSecondPress(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("Keep"); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.focus = focusPlaylists
	got, _ := m.handlePlaylistKey(keyPress("D"))
	m = got.(Model)
	if len(store.Playlists()) != 1 || m.deletePlaylistPending != 0 {
		t.Fatalf("first D deleted playlist: %+v", store.Playlists())
	}
	got, _ = m.handlePlaylistKey(keyPress("D"))
	m = got.(Model)
	if len(store.Playlists()) != 0 || m.deletePlaylistPending != -1 {
		t.Fatalf("second D did not delete playlist: %+v", store.Playlists())
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
				m.results = []youtube.Track{{Title: "Result"}}
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
				m.keys.info.Copy = customBinding("x", "x", "copy url")
				m.keys.info.Close = customBinding("z", "z", "close")
				return m
			},

			duplicateHints: []string{"Track info — y copy URL, esc to close"},
			wantBindings:   func(m Model) []key.Binding { return []key.Binding{m.keys.info.Copy, m.keys.info.Close} },
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
		m.focus = focusPlaylists
		m.keys.playlists.Delete = customBinding("x", "alt+x", "delete playlist")
		got, _ := m.handlePlaylistKey(keyPress("x"))
		m = got.(Model)
		want := "press " + m.keys.playlists.Delete.Help().Key + " again to delete " + quote("Keep")
		if m.status != want {
			t.Errorf("delete confirmation = %q, want %q", m.status, want)
		}
		if m.deletePlaylistPending != 0 {
			t.Errorf("deletePlaylistPending = %d, want 0 after first press", m.deletePlaylistPending)
		}
	})
}

func TestPasteGoesToSearch(t *testing.T) {
	m := New(Deps{})
	m.focus = focusResults
	m.input.Blur()

	got, _ := m.Update(tea.PasteMsg{Content: "joe hisaishi"})
	gm := got.(Model)
	if !atPane(gm, focusSearch) {
		t.Errorf("focus = %v, want focusSearch", gm.focus)
	}
	if v := gm.input.Value(); v != "joe hisaishi" {
		t.Errorf("input = %q, want %q", v, "joe hisaishi")
	}
}

func TestGraphicsProbe(t *testing.T) {
	reply := func(id int, payload string) uv.KittyGraphicsEvent {
		return uv.KittyGraphicsEvent{Options: kitty.Options{ID: id}, Payload: []byte(payload)}
	}
	plainOK := reply(thumbnail.QueryID, "OK")
	placeholderOK := reply(thumbnail.PlaceholderQueryID, "OK")
	placeholderRejected := reply(thumbnail.PlaceholderQueryID, "ENOTSUPPORTED:unicode placeholders are not supported")
	da1 := uv.PrimaryDeviceAttributesEvent{62, 22}

	tests := []struct {
		name   string
		events []tea.Msg
		want   graphicsSupport
	}{
		{name: "Ghostty: both probes OK", events: []tea.Msg{plainOK, placeholderOK, da1}, want: graphicsPlaceholder},
		{name: "Zellij 0.45: placeholders rejected", events: []tea.Msg{plainOK, placeholderRejected, da1}, want: graphicsDirect},
		{name: "Zellij 0.44 or tmux: only DA1", events: []tea.Msg{da1}, want: graphicsNone},
		{name: "replies after DA1 are ignored", events: []tea.Msg{da1, plainOK, placeholderOK}, want: graphicsNone},
		{name: "undecided until DA1", events: []tea.Msg{plainOK, placeholderOK}, want: graphicsUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m tea.Model = New(Deps{Thumbnails: true})
			for _, ev := range tt.events {
				m, _ = m.Update(ev)
			}
			if got := m.(Model).thumb.graphics; got != tt.want {
				t.Errorf("graphics = %v, want %v", got, tt.want)
			}
		})
	}
}

func devicePickerModel(device string) Model {
	m := New(Deps{})
	m.width, m.height = 80, 30
	m.applyProperty(mpv.Event{Prop: mpv.PropAudioDevice, Data: json.RawMessage(device)})
	m.devices = []mpv.AudioDevice{
		{Name: "auto", Description: "Autoselect device"},
		{Name: "pipewire/DX5", Description: "DX5 II Headphones"},
	}
	return m
}

func TestDevicePickerMarksCurrent(t *testing.T) {
	tests := []struct {
		name      string
		device    string // mpv's audio-device property, JSON-encoded
		wantIndex int
	}{
		{name: "explicit device", device: `"pipewire/DX5"`, wantIndex: 1},
		{name: "mpv's auto", device: `"auto"`, wantIndex: 0},
		{name: "before the first event", device: `null`, wantIndex: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := devicePickerModel(tt.device)
			if !m.isCurrentDevice(m.devices[tt.wantIndex]) {
				t.Errorf("device %d not marked as current for %s", tt.wantIndex, tt.device)
			}
			if got := m.currentDeviceName(); got != m.devices[tt.wantIndex].Name {
				t.Errorf("currentDeviceName() = %q, want %q", got, m.devices[tt.wantIndex].Name)
			}
			if _, ok := m.currentDevice(); !ok {
				t.Errorf("currentDevice() = not found for %s", tt.device)
			}
		})
	}
}

func TestRenderDevices(t *testing.T) {
	m := devicePickerModel(`"pipewire/DX5"`)
	m.deviceCur = 1

	got := ansi.Strip(m.renderDevices(80, 20))
	for _, want := range []string{"● ", "DX5 II Headphones", "Autoselect device"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderDevices() = %q, want %q", got, want)
		}
	}

	m.devices = nil
	if got := ansi.Strip(m.renderDevices(80, 20)); !strings.Contains(got, "no output devices") {
		t.Errorf("renderDevices() = %q, want an empty-list hint", got)
	}
}

func TestPasteKeysReadClipboard(t *testing.T) {
	ctrlShiftV := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}
	if got := ctrlShiftV.String(); got != "ctrl+shift+v" {
		t.Fatalf("key string = %q, want ctrl+shift+v", got)
	}

	tests := []struct {
		name  string
		focus focus
	}{
		{name: "from search", focus: focusSearch},
		{name: "from results", focus: focusResults},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.focus = tt.focus

			got, cmd := m.Update(ctrlShiftV)
			gm := got.(Model)
			if !atPane(gm, focusSearch) {
				t.Errorf("focus = %v, want focusSearch", gm.focus)
			}
			if cmd == nil {
				t.Error("Update() cmd = nil, want clipboard read")
			}
			if v := gm.input.Value(); v != "" {
				t.Errorf("input = %q, want the key not typed as text", v)
			}
		})
	}
}

func playingModel(g graphicsSupport) Model {
	const url = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = g
	m.width, m.height = 100, 30
	m.idle, m.queue.pos = false, 0
	m.queue.entries = []mpv.PlaylistEntry{{Filename: url}}
	m.tracks[url] = youtube.Track{ID: "abc", Title: "Song", URL: url}
	m.thumb.video = "abc"
	return m
}

func TestApplyThumbPicksRendering(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 18))

	tests := []struct {
		name      string
		graphics  graphicsSupport
		wantKitty bool
	}{
		{name: "kitty placeholders", graphics: graphicsPlaceholder, wantKitty: true},
		{name: "kitty direct placement, e.g. Zellij 0.45", graphics: graphicsDirect, wantKitty: true},
		{name: "half-block fallback", graphics: graphicsNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cmd := playingModel(tt.graphics).update(thumbMsg{videoID: "abc", img: img})
			gm := got.(Model)

			if gotKitty := gm.thumb.id != 0; gotKitty != tt.wantKitty {
				t.Errorf("kitty image = %v, want %v", gotKitty, tt.wantKitty)
			}
			if gotArt := gm.thumb.art != ""; gotArt == tt.wantKitty {
				t.Errorf("half-block art = %v, want %v", gotArt, !tt.wantKitty)
			}
			if (cmd != nil) != tt.wantKitty {
				t.Errorf("cmd = %v, want out-of-band transmit only for kitty", cmd)
			}
			if w := lipgloss.Width(gm.renderNowPlaying()); w != 100 {
				t.Errorf("now playing width = %d, want 100", w)
			}
		})
	}
}

func TestThumbOriginIsInsideNowPlayingBox(t *testing.T) {
	for _, viz := range []bool{false, true} {
		m := playingModel(graphicsDirect)
		m.thumb.id, m.showViz = 16, viz

		at := m.thumbOrigin()
		lines := strings.Split(ansi.Strip(m.render()), "\n")
		if corner := []rune(lines[at.Y-1])[at.X-1]; corner != '╭' {
			t.Errorf("viz=%v: cell above-left of thumbOrigin %v = %q, want box corner", viz, at, corner)
		}
		if above := []rune(lines[at.Y-1])[at.X]; above != '─' {
			t.Errorf("viz=%v: cell above thumbOrigin = %q, want top border", viz, above)
		}
	}
}

func TestSyncPlacement(t *testing.T) {
	m := playingModel(graphicsDirect)
	m.thumb.id = 16

	if cmd := m.syncPlacement(); cmd == nil {
		t.Fatal("first sync: cmd = nil, want placement scheduled")
	}
	if cmd := m.syncPlacement(); cmd != nil {
		t.Error("unchanged layout: cmd != nil, want no re-placement")
	}

	m.showViz = !m.showViz // moves the now-playing box
	if cmd := m.syncPlacement(); cmd == nil {
		t.Error("layout moved: cmd = nil, want re-placement")
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !next.(Model).thumb.placed {
		t.Error("after resize: placed = false, want a placement rescheduled by Update")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height}); cmd == nil {
		t.Error("same-size resize: cmd = nil, want re-placement since the terminal drops placed images")
	}

	if _, cmd := m.update(placeMsg{id: 99, at: m.thumb.placedAt}); cmd != nil {
		t.Errorf("placeMsg for a replaced image: cmd = %v, want nil", cmd)
	}
	if _, cmd := m.update(placeMsg{id: 16, at: m.thumb.placedAt}); cmd == nil {
		t.Error("placeMsg for the current image: cmd = nil, want Put")
	}
}

type searchStub struct {
	tracks []youtube.Track
}

func (s searchStub) Search(_ context.Context, _ string, _ int) ([]youtube.Track, error) {
	return nil, errors.New("unexpected search")
}

func (s searchStub) Lookup(_ context.Context, _ string, _ int) ([]youtube.Track, error) {
	return s.tracks, nil
}

func TestSearchEnterImportsYouTubeLink(t *testing.T) {
	player := &spyPlaylistPlayer{}
	m := New(Deps{Player: player, Searcher: searchStub{tracks: []youtube.Track{
		{ID: "x1", Title: "one", URL: "https://www.youtube.com/watch?v=x1"},
	}}})
	m.input.SetValue("https://youtu.be/x1")

	got, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	gm := got.(Model)
	if !gm.searching {
		t.Error("searching = false, want the import in flight")
	}
	if !strings.Contains(gm.status, "importing") {
		t.Errorf("status = %q, want an importing note", gm.status)
	}

	if len(gm.imports) != 1 || gm.queue.tail != nil {
		t.Fatalf("imports = %+v, want a pending lookup without a queue slot", gm.imports)
	}

	lm, ok := fetchQueue(gm.deps.Searcher, youtube.Link{URL: "https://www.youtube.com/watch?v=x1"}, gm.activeRequest)().(lookupDoneMsg)
	if !ok {
		t.Fatal("fetchQueue did not return a lookupDoneMsg")
	}
	got, write := gm.update(lm)
	gm = got.(Model)
	if write == nil || len(gm.imports) != 0 {
		t.Fatalf("write = %v, imports = %+v, want the resolved import queued", write != nil, gm.imports)
	}
	qm, ok := write().(queueDoneMsg)
	if !ok || !slices.Equal(player.calls, []string{"append https://www.youtube.com/watch?v=x1"}) {
		t.Fatalf("calls = %v, want the resolved track appended", player.calls)
	}
	got, cmd := gm.update(qm)
	gm = got.(Model)
	if gm.searching {
		t.Error("searching = true, want the fetch finished")
	}
	if _, ok := gm.tracks["https://www.youtube.com/watch?v=x1"]; !ok {
		t.Errorf("tracks = %v, want the resolved track remembered", gm.tracks)
	}
	if got := gm.status; got != "1 track queued" {
		t.Errorf("status = %q, want 1 track queued", got)
	}
	if cmd != nil {
		t.Error("cmd != nil, want the write already completed")
	}
}

func TestQueueDoneQueuesTracks(t *testing.T) {
	m := New(Deps{})
	m.searching = true
	tracks := []youtube.Track{
		{ID: "a", Title: "one", URL: "https://www.youtube.com/watch?v=a"},
		{ID: "b", Title: "two", URL: "https://www.youtube.com/watch?v=b"},
	}

	got, cmd := m.update(queueDoneMsg{tracks: tracks})
	gm := got.(Model)
	if gm.searching {
		t.Error("searching = true, want the fetch finished")
	}
	if got := gm.status; got != "2 tracks queued" {
		t.Errorf("status = %q, want 2 tracks queued", got)
	}
	for _, tr := range tracks {
		if _, ok := gm.tracks[tr.URL]; !ok {
			t.Errorf("tracks missing %q", tr.URL)
		}
	}
	if cmd != nil {
		t.Error("cmd != nil, want the write already completed")
	}

	got, cmd = m.update(queueDoneMsg{err: errors.New("boom")})
	gm = got.(Model)
	if !gm.statusErr || gm.status != "boom" {
		t.Errorf("status = %q err = %v, want the error surfaced", gm.status, gm.statusErr)
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want none for a failed fetch", cmd)
	}
}

func TestFetchQueueCapsMixes(t *testing.T) {
	tracks := []youtube.Track{{ID: "seed", URL: "https://www.youtube.com/watch?v=seed"}}
	for i := range radioLimit + 4 {
		id := fmt.Sprintf("t%d", i)
		tracks = append(tracks, youtube.Track{ID: id, URL: "https://www.youtube.com/watch?v=" + id})
	}

	link := youtube.Link{URL: "https://www.youtube.com/watch?v=seed&list=RDseed", Mix: true}
	qm, ok := fetchQueue(searchStub{tracks: tracks}, link, 1)().(lookupDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want lookupDoneMsg", qm)
	}
	if len(qm.tracks) != radioLimit {
		t.Errorf("tracks = %d, want %d", len(qm.tracks), radioLimit)
	}
	if qm.tracks[0].ID != "seed" {
		t.Errorf("first track = %q, want the seed, which is what the link plays", qm.tracks[0].ID)
	}

	link = youtube.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	qm, ok = fetchQueue(searchStub{tracks: tracks}, link, 2)().(lookupDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want lookupDoneMsg", qm)
	}
	if len(qm.tracks) != radioLimit+5 {
		t.Errorf("tracks = %d, want the whole playlist", len(qm.tracks))
	}
}

func TestFetchQueueCapsLargePlaylistAndShowsLimit(t *testing.T) {
	tracks := make([]youtube.Track, youtube.MaxPlaylistItems+5)
	for i := range tracks {
		tracks[i] = youtube.Track{ID: fmt.Sprint(i), URL: fmt.Sprintf("https://www.youtube.com/watch?v=%d", i)}
	}
	appended := 0
	link := youtube.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	lookup, ok := fetchQueue(searchStub{tracks: tracks}, link, 1)().(lookupDoneMsg)
	if !ok {
		t.Fatal("fetchQueue did not return a lookupDoneMsg")
	}
	qm := queueImport(func(got []youtube.Track) error {
		appended = len(got)
		return nil
	}, lookup, queueTask{done: make(chan struct{})})().(queueDoneMsg)
	if len(qm.tracks) != youtube.MaxPlaylistItems || appended != youtube.MaxPlaylistItems || !qm.limitHit {
		t.Fatalf("queued %d tracks, appended %d, limitHit %v", len(qm.tracks), appended, qm.limitHit)
	}
	m := New(Deps{})
	m.activeRequest = 1
	updated, _ := m.update(qm)
	if got := updated.(Model).status; !strings.Contains(got, "import limit: 200") {
		t.Errorf("status = %q, want import-limit hint", got)
	}
}

func TestRadioKeySeedsFromCurrentTrack(t *testing.T) {
	m := playingModel(graphicsNone)
	m.focus = focusResults

	got, cmd := m.Update(tea.KeyPressMsg{Code: 'r'})
	gm := got.(Model)
	if !gm.searching {
		t.Error("searching = false, want the radio fetch in flight")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want a radio fetch")
	}
	if got := gm.status; got != "fetching radio…" {
		t.Errorf("status = %q, want fetching radio", got)
	}

	m = New(Deps{})
	m.focus = focusResults
	got, cmd = m.Update(tea.KeyPressMsg{Code: 'r'})
	gm = got.(Model)
	if gm.searching {
		t.Error("searching = true, want no fetch for an idle player")
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want none", cmd)
	}
}

func TestFetchRadioDropsSeedAndCaps(t *testing.T) {
	tracks := []youtube.Track{{ID: "seed", URL: "https://www.youtube.com/watch?v=seed"}}
	for i := range radioLimit + 1 {
		id := fmt.Sprintf("t%d", i)
		tracks = append(tracks, youtube.Track{ID: id, URL: "https://www.youtube.com/watch?v=" + id})
	}

	msg := fetchRadio(searchStub{tracks: tracks}, "seed", 1)()
	qm, ok := msg.(lookupDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want lookupDoneMsg", msg)
	}
	if qm.err != nil {
		t.Fatalf("err = %v", qm.err)
	}
	if len(qm.tracks) != radioLimit {
		t.Errorf("tracks = %d, want %d", len(qm.tracks), radioLimit)
	}
	for _, tr := range qm.tracks {
		if tr.ID == "seed" {
			t.Error("seed still queued, want it dropped from the mix")
		}
	}
}

func TestSearchCompletionKeepsNewestRequest(t *testing.T) {
	m := New(Deps{})
	first := m.nextRequest()
	m.searching = true
	second := m.nextRequest()
	m.searchRequest = second
	m.spinnerRequest = second
	m.searching = true
	m.setStatus("searching newest…")

	newest := youtube.Track{ID: "new", URL: "new"}
	got, _ := m.update(searchDoneMsg{requestID: second, query: "newest", tracks: []youtube.Track{newest}})
	m = got.(Model)
	got, _ = m.update(searchDoneMsg{requestID: first, query: "older", tracks: []youtube.Track{{ID: "old", URL: "old"}}})
	m = got.(Model)
	if len(m.results) != 1 || m.results[0].ID != newest.ID {
		t.Errorf("results = %+v, want only newest", m.results)
	}
	if m.status != "1 result for “newest”" || m.searching {
		t.Errorf("status = %q, searching = %v, want newest completion", m.status, m.searching)
	}
	if _, ok := m.tracks["old"]; ok {
		t.Error("older result was added to track metadata")
	}
}

func TestStaleCompletionPreservesActiveSpinnerAndStatus(t *testing.T) {
	m := New(Deps{})
	old := m.nextRequest()
	newest := m.nextRequest()
	m.searchRequest = newest
	m.spinnerRequest = newest
	m.searching = true
	m.setStatus("searching newest…")

	for _, msg := range []tea.Msg{
		searchDoneMsg{requestID: old, err: errors.New("old search failed")},
		queueDoneMsg{requestID: old, err: errors.New("old import failed")},
		queueActionDoneMsg{requestID: old, err: errors.New("old append failed")},
	} {
		got, _ := m.update(msg)
		m = got.(Model)
		if !m.searching || m.status != "searching newest…" || m.statusErr || m.activeRequest != newest {
			t.Fatalf("after %T: searching = %v, status = %q, error = %v", msg, m.searching, m.status, m.statusErr)
		}
	}
}

func TestQueueActionDoesNotDiscardPendingSearch(t *testing.T) {
	m := New(Deps{})
	searchID := m.nextRequest()
	m.searchRequest, m.spinnerRequest, m.searching = searchID, searchID, true
	queueID := m.nextRequest()
	m.setStatus("queueing a track…")

	got, _ := m.update(searchDoneMsg{requestID: searchID, query: "song", tracks: []youtube.Track{{ID: "song", URL: "song"}}})
	m = got.(Model)
	if len(m.results) != 1 || m.results[0].ID != "song" {
		t.Errorf("results = %+v, want pending search applied", m.results)
	}
	if m.searching || m.status != "queueing a track…" || m.activeRequest != queueID {
		t.Errorf("searching = %v, status = %q, active = %d", m.searching, m.status, m.activeRequest)
	}
}

type appendRecorder struct {
	*mpv.Player
	writes chan string
}

func (p appendRecorder) AppendAll(_ context.Context, urls []string) error {
	p.writes <- urls[0]
	return nil
}

func TestOverlappingImportsWriteInTriggerOrder(t *testing.T) {
	player := appendRecorder{writes: make(chan string, 2)}
	m := New(Deps{Player: player})
	first, second := m.nextRequest(), m.nextRequest()
	m.imports = []pendingImport{{requestID: first}, {requestID: second}}

	got, cmd := m.update(lookupDoneMsg{requestID: second, tracks: []youtube.Track{{URL: "second"}}})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("second import was queued before first resolved")
	}
	got, cmd = m.update(lookupDoneMsg{requestID: first, tracks: []youtube.Track{{URL: "first"}}})
	m = got.(Model)
	if cmd == nil || len(m.imports) != 0 {
		t.Fatalf("pending imports = %+v, want both queued", m.imports)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("cmd = %v, want both writes batched", batch)
	}
	// Start the writes in reverse: their reserved slots still keep trigger order.
	done := make(chan tea.Msg, 2)
	for _, write := range slices.Backward(batch) {
		go func() { done <- write() }()
	}
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-player.writes:
			if got != want {
				t.Fatalf("write = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q write", want)
		}
	}
	for range 2 {
		if err := (<-done).(queueDoneMsg).err; err != nil {
			t.Fatalf("import error = %v", err)
		}
	}
}

func TestRapidQueueMovesKeepSelectedTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 2

	for range 2 {
		got, cmd := m.handleQueueKey(keyPress("K"))
		if cmd == nil {
			t.Fatal("move command = nil")
		}
		m = got.(Model)
	}
	if m.queueCur != 0 {
		t.Errorf("cursor = %d, want 0", m.queueCur)
	}
	want := []string{"C", "A", "B"}
	for i, entry := range m.queue.entries {
		if entry.Filename != want[i] {
			t.Fatalf("queue[%d] = %q, want %q", i, entry.Filename, want[i])
		}
	}
	// mpv may report the first move after both keys were handled. That older
	// event must not roll back the locally projected second move.
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`)})
	if m.queue.entries[0].Filename != "C" {
		t.Errorf("stale playlist event replaced projected queue: %+v", m.queue.entries)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"C"},{"filename":"A"},{"filename":"B"}]`)})
	if m.queue.projection == nil {
		t.Error("projection cleared before command completion")
	}
}

func TestEmptyQueueNavigationCannotDelete(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	for _, key := range []string{"down", "j", "G", "d", "J", "enter"} {
		got, cmd := m.handleQueueKey(keyPress(key))
		m = got.(Model)
		if cmd != nil || m.queueCur < 0 || len(m.queue.entries) != 0 {
			t.Fatalf("after %q: cursor=%d queue=%+v cmd=%v", key, m.queueCur, m.queue.entries, cmd != nil)
		}
	}
}

func TestClearQueueProjectsEmptyAndIgnoresStalePlaylist(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	m.queue.pos, m.idle = 0, false
	m.timePos, m.duration = 10*time.Second, time.Minute
	got, cmd := m.handleQueueKey(keyPress("C"))
	m = got.(Model)
	if cmd == nil || len(m.queue.entries) != 0 || m.queueCur != 0 || m.queue.pos != -1 || !m.idle || m.timePos != 0 || m.duration != 0 {
		t.Fatalf("clear projection = queue %+v, cursor %d, pos %d, idle %v, time %v/%v", m.queue.entries, m.queueCur, m.queue.pos, m.idle, m.timePos, m.duration)
	}
	if m.queue.editsPending != 1 || m.queue.projection == nil {
		t.Fatal("clear did not reserve an authoritative queue refresh")
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"B"}]`)})
	if len(m.queue.entries) != 0 {
		t.Fatal("stale playlist event repopulated cleared queue")
	}
	got, refresh := m.update(queueActionDoneMsg{requestID: m.activeRequest, status: "queue cleared", projected: true})
	m = got.(Model)
	if refresh == nil || m.status != "queue cleared" {
		t.Fatalf("clear completion = status %q, refresh %v", m.status, refresh != nil)
	}
	got, _ = m.update(queueRefreshMsg{revision: m.queue.revision, entries: nil, pos: -1})
	m = got.(Model)
	if len(m.queue.entries) != 0 || m.queue.pos != -1 || m.queue.projection != nil {
		t.Fatalf("clear refresh = queue %+v, pos %d, projection %v", m.queue.entries, m.queue.pos, m.queue.projection)
	}
}

func TestClearQueueKeepsImportStillResolving(t *testing.T) {
	player := &spyPlaylistPlayer{}
	m := New(Deps{Player: player})
	m.input.SetValue("https://youtu.be/x1")
	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(Model)
	importID := m.activeRequest

	m.focus = focusQueue
	got, clear := m.handleQueueKey(keyPress("C"))
	m = got.(Model)
	cleared := make(chan tea.Msg, 1)
	go func() { cleared <- clear() }()
	select {
	case <-cleared:
	case <-time.After(time.Second):
		t.Fatal("clear waited on the unresolved import lookup")
	}

	got, write := m.update(lookupDoneMsg{requestID: importID, tracks: []youtube.Track{{URL: "x1"}}})
	m = got.(Model)
	if write == nil {
		t.Fatal("resolved import was not queued after clear")
	}
	if err := write().(queueDoneMsg).err; err != nil {
		t.Fatal(err)
	}
	if want := []string{"stop", "append x1"}; !slices.Equal(player.calls, want) {
		t.Errorf("calls = %v, want %v", player.calls, want)
	}
}

func TestQueueEnterReservesSlotAfterProjectedMove(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, move := m.handleQueueKey(keyPress("K"))
	m = got.(Model)
	if move == nil || m.queueCur != 0 {
		t.Fatal("move did not project B to index 0")
	}
	moveDone := m.queue.tail
	got, play := m.handleQueueKey(keyPress("enter"))
	m = got.(Model)
	if play == nil || m.queue.tail == moveDone {
		t.Fatal("play selection was not reserved behind the projected move")
	}
	if m.queue.editsPending != 1 {
		t.Errorf("pending projected edits = %d, want 1", m.queue.editsPending)
	}
}

func TestPlayNowBlocksIndexEditsUntilQueueRefresh(t *testing.T) {
	m := New(Deps{})
	m.focus = focusResults
	m.results = []youtube.Track{{URL: "D", Title: "D"}}
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}}
	m.queueCur = 1
	got, play := m.handleResultKey(keyPress("enter"))
	m = got.(Model)
	if play == nil || !m.queue.insertPending || m.queue.editsPending != 1 {
		t.Fatal("play-now did not reserve an insertion refresh")
	}
	m.focus = focusQueue
	for _, key := range []string{"d", "K", "J", "enter"} {
		got, cmd := m.handleQueueKey(keyPress(key))
		m = got.(Model)
		if cmd != nil || len(m.queue.entries) != 2 || m.queue.entries[1].Filename != "B" {
			t.Fatalf("%q edited a stale queue index", key)
		}
	}
	got, refresh := m.update(queueActionDoneMsg{requestID: m.activeRequest, projected: true})
	m = got.(Model)
	if refresh == nil || !m.queue.insertPending {
		t.Fatal("insertion was unblocked before mpv queue refresh")
	}
	got, _ = m.update(queueRefreshMsg{revision: m.queue.revision, entries: []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "D"}, {Filename: "B"}}, pos: 1})
	m = got.(Model)
	if m.queue.insertPending || len(m.queue.entries) != 3 || m.queue.entries[1].Filename != "D" {
		t.Fatalf("insertion refresh = %+v; pending = %v", m.queue.entries, m.queue.insertPending)
	}
	got, edit := m.handleQueueKey(keyPress("d"))
	m = got.(Model)
	if edit == nil || len(m.queue.entries) != 2 || m.queue.entries[1].Filename != "B" {
		t.Fatalf("delete did not target refreshed index: %+v", m.queue.entries)
	}
}

func TestOpposingQueueMovesIgnoreOldAndIntermediateEvents(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 2
	for _, key := range []string{"K", "J"} {
		got, cmd := m.handleQueueKey(keyPress(key))
		if cmd == nil {
			t.Fatalf("%s command = nil", key)
		}
		m = got.(Model)
	}
	if m.queueCur != 2 || m.queue.entries[2].Filename != "C" {
		t.Fatalf("projection = %+v cursor %d, want original order with C selected", m.queue.entries, m.queueCur)
	}
	for _, snapshot := range []string{
		`[{"filename":"A"},{"filename":"B"},{"filename":"C"}]`, // before either move
		`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`, // after only K
	} {
		m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(snapshot)})
		if m.queue.entries[2].Filename != "C" || m.queueCur != 2 {
			t.Fatalf("stale event %s changed projected selection: %+v cursor %d", snapshot, m.queue.entries, m.queueCur)
		}
	}
	for _, id := range []uint64{1, 2} {
		got, _ := m.update(queueActionDoneMsg{requestID: id, projected: true})
		m = got.(Model)
	}
	// Both mpv commands have now finished, but older property events can still
	// be queued for the TUI. Equality with the final order is not a barrier.
	for _, snapshot := range []string{
		`[{"filename":"A"},{"filename":"B"},{"filename":"C"}]`,
		`[{"filename":"A"},{"filename":"C"},{"filename":"B"}]`,
	} {
		m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(snapshot)})
		if m.queue.entries[2].Filename != "C" || m.queueCur != 2 {
			t.Fatalf("late event %s changed projected selection: %+v cursor %d", snapshot, m.queue.entries, m.queueCur)
		}
	}
	got, _ := m.update(queueRefreshMsg{revision: m.queue.revision, entries: []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}, pos: -1})
	m = got.(Model)
	if m.queue.entries[2].Filename != "C" || m.queueCur != 2 {
		t.Errorf("authoritative refresh = %+v cursor %d, want C selected", m.queue.entries, m.queueCur)
	}
	got, _ = m.handleQueueKey(keyPress("K"))
	m = got.(Model)
	if m.queue.entries[1].Filename != "C" || m.queueCur != 1 {
		t.Errorf("next move targeted wrong track: queue=%+v cursor=%d", m.queue.entries, m.queueCur)
	}
}

func TestPlaylistEventBurstSchedulesOneAuthoritativeRead(t *testing.T) {
	m := New(Deps{})
	m.queue.authoritative = true
	event := mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"}]`)}
	commands := 0
	for range 200 {
		if cmd := m.applyProperty(event); cmd != nil {
			commands++
		}
	}
	if commands != 1 {
		t.Fatalf("200 events scheduled %d debounce timers, want 1", commands)
	}
	got, cmd := m.update(queueDebounceMsg{version: 1})
	m = got.(Model)
	if cmd == nil || !m.queue.debouncePending || m.queue.refreshPending {
		t.Fatal("changed event generation did not extend debounce")
	}
	got, cmd = m.update(queueDebounceMsg{version: m.queue.eventVersion})
	m = got.(Model)
	if cmd == nil || !m.queue.refreshPending {
		t.Fatal("settled burst did not schedule one authoritative read")
	}
}

func TestRapidQueueDeletesAdvanceToNextTrack(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
	m.queueCur = 0

	for range 2 {
		got, cmd := m.handleQueueKey(keyPress("d"))
		if cmd == nil {
			t.Fatal("delete command = nil")
		}
		m = got.(Model)
	}
	if len(m.queue.entries) != 1 || m.queue.entries[0].Filename != "C" {
		t.Errorf("queue = %+v, want only C", m.queue.entries)
	}
}

func TestStalePlaylistEventDoesNotRestoreDeletedEntry(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{
		{Filename: "A"},
		{Filename: "B"},
		{Filename: "C"},
	}
	m.queueCur = 0
	got, cmd := m.handleQueueKey(keyPress("d"))
	if cmd == nil {
		t.Fatal("delete command = nil")
	}
	m = got.(Model)

	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":1,"filename":"A"},{"id":2,"filename":"B"},{"id":3,"filename":"C"}]`)})
	if len(m.queue.entries) != 2 || m.queue.entries[0].Filename != "B" {
		t.Fatalf("stale event restored deleted A: %+v", m.queue.entries)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":2,"filename":"B"},{"id":4,"filename":"D"},{"id":3,"filename":"C"}]`)})
	if len(m.queue.entries) != 2 || m.queue.entries[0].Filename != "B" {
		t.Errorf("event bypassed authoritative refresh: queue=%+v", m.queue.entries)
	}
	got, _ = m.update(queueActionDoneMsg{requestID: m.activeRequest, projected: true})
	m = got.(Model)
	got, _ = m.update(queueRefreshMsg{revision: m.queue.revision, entries: []mpv.PlaylistEntry{{Filename: "B"}, {Filename: "D"}, {Filename: "C"}}, pos: -1})
	m = got.(Model)
	if len(m.queue.entries) != 3 || m.queue.entries[1].Filename != "D" {
		t.Errorf("authoritative refresh missed insert: queue=%+v", m.queue.entries)
	}
}

func TestProjectedDeleteDistinguishesDuplicateURLs(t *testing.T) {
	m := New(Deps{})
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "same"}, {Filename: "same"}}
	got, _ := m.handleQueueKey(keyPress("d"))
	m = got.(Model)
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":1,"filename":"same"},{"id":2,"filename":"same"}]`)})
	if len(m.queue.entries) != 1 || m.queue.entries[0].Filename != "same" {
		t.Errorf("stale duplicate event restored removed entry: %+v", m.queue.entries)
	}
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"id":2,"filename":"same"},{"id":3,"filename":"same"}]`)})
	if len(m.queue.entries) != 1 {
		t.Errorf("duplicate event bypassed authoritative refresh: queue=%+v", m.queue.entries)
	}
}

func TestQueueWriteErrorIsReportedAfterWrite(t *testing.T) {
	m := New(Deps{Thumbnails: true})
	m.thumb.graphics = graphicsNone
	id := m.nextRequest()
	task := m.queue.reserve()
	want := errors.New("mpv rejected append")
	tracks := []youtube.Track{{ID: "x", Title: "resolved first", URL: "x"}, {ID: "y", Title: "resolved second", URL: "y"}}
	m.queue.entries, m.queue.pos, m.idle = []mpv.PlaylistEntry{{Filename: "x"}}, 0, false
	accepted := ""
	msg := queueImport(func(tracks []youtube.Track) error {
		accepted = tracks[0].URL // mpv accepted this entry before rejecting the next one.
		return want
	}, lookupDoneMsg{requestID: id, tracks: tracks}, task)()
	got, _ := m.update(msg)
	m = got.(Model)
	if !m.statusErr || m.status != want.Error() {
		t.Errorf("status = %q, error = %v, want write error", m.status, m.statusErr)
	}
	if accepted != "x" || m.tracks["x"].Title != "resolved first" || m.tracks["y"].Title != "resolved second" {
		t.Errorf("accepted = %q, metadata = %+v, want resolved tracks retained after partial write", accepted, m.tracks)
	}
	_, playing, ok := m.current()
	if !ok || playing.Title != "resolved first" {
		t.Errorf("current track = %+v, available = %v, want metadata for accepted entry", playing, ok)
	}
	if m.thumb.video != "x" {
		t.Errorf("thumbnail target = %q, want accepted track x after metadata arrives", m.thumb.video)
	}
}

func TestQueueRefreshPreservesEntryTitlesWithDuplicateURLs(t *testing.T) {
	m := New(Deps{})
	m.queue.authoritative = true
	entries := []mpv.PlaylistEntry{
		{Filename: "same", Title: "first title", Current: false},
		{Filename: "same", Title: "second title", Current: true, Playing: true},
	}
	got, _ := m.update(queueRefreshMsg{entries: entries, pos: 1})
	m = got.(Model)
	if len(m.queue.entries) != 2 || m.queue.entries[0].Title != "first title" || m.queue.entries[1].Title != "second title" {
		t.Fatalf("refreshed entries = %+v, want distinct titles", m.queue.entries)
	}
	if title := displayTitle(m.queue.entries[1], youtube.Track{}); title != "second title" {
		t.Errorf("display title = %q, want second title", title)
	}
	if m.queue.pos != 1 || !m.queue.entries[1].Playing {
		t.Errorf("position = %d, playing = %v, want second entry playing", m.queue.pos, m.queue.entries[1].Playing)
	}
}

func TestRestoredMetadataTitlesUnplayedQueueEntries(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{InitialTracks: map[string]youtube.Track{
		url: {URL: url, ID: "abc", Title: "Saved song", Channel: "Artist"},
	}})
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"https://www.youtube.com/watch?v=abc"}]`)})
	for _, detailed := range []bool{false, true} {
		if got := m.queueLine(0, m.queue.entries[0], 60, detailed); !strings.Contains(got, "Saved song") || strings.Contains(got, url) {
			t.Errorf("restored queue line (detailed=%v) = %q, want saved title", detailed, got)
		}
	}
}

func TestQueueTabShowsDetailedQueueFullWidth(t *testing.T) {
	m := mouseModel()
	m.help.SetWidth(m.width)
	m.input.Blur()
	m.tracks["u1"] = youtube.Track{Title: "queued 1", Channel: "Some Channel", Duration: 256 * time.Second}

	m.focus = focusResults
	split := ansi.Strip(m.render())
	if !strings.Contains(split, "song 00") || strings.Contains(split, "Some Channel") {
		t.Fatalf("results tab should show results beside a compact queue:\n%s", split)
	}

	m.focus = focusQueue
	got := ansi.Strip(m.render())
	if strings.Contains(got, "song 00") {
		t.Errorf("queue tab still renders search results:\n%s", got)
	}
	if !strings.Contains(got, "2 queued 1") || !strings.Contains(got, "Some Channel · 4:16") {
		t.Errorf("queue tab row lacks position, channel or length:\n%s", got)
	}
	if w, h := lipgloss.Width(m.render()), lipgloss.Height(m.render()); w > m.width || h > m.height {
		t.Errorf("render() size = %dx%d, want within %dx%d", w, h, m.width, m.height)
	}
}

func TestLinkImportSwitchesToQueueTab(t *testing.T) {
	m := New(Deps{})
	m.input.SetValue("https://www.youtube.com/playlist?list=PL123")
	got, _ := m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m = got.(Model); !atPane(m, focusQueue) {
		t.Errorf("focus after link import = %v, want queue", m.focus)
	}
}

func TestFailedQueueActionReleasesNextWrite(t *testing.T) {
	m := New(Deps{})
	first := m.queue.reserve()
	second := m.queue.reserve()
	want := errors.New("first append failed")
	order := make(chan string, 2)
	secondDone := make(chan tea.Msg, 1)
	go func() {
		secondDone <- queueAction(second, 2, "second queued", func(context.Context) error {
			order <- "second"
			return nil
		})()
	}()
	firstMsg := queueAction(first, 1, "first queued", func(context.Context) error {
		order <- "first"
		return want
	})().(queueActionDoneMsg)
	if !errors.Is(firstMsg.err, want) {
		t.Fatalf("first error = %v, want %v", firstMsg.err, want)
	}
	select {
	case msg := <-secondDone:
		if err := msg.(queueActionDoneMsg).err; err != nil {
			t.Fatalf("second error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second action remained blocked after first failed")
	}
	for _, want := range []string{"first", "second"} {
		if got := <-order; got != want {
			t.Fatalf("write order = %q, want %q", got, want)
		}
	}
}

func cellAt(t *testing.T, m Model, text string) image.Point {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.render()), "\n") {
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

func TestMouseEnablesCellMotionOnly(t *testing.T) {
	if got := New(Deps{}).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want cell motion so hovering never renders", got)
	}
}

func TestClickSelectsRowUnderPointer(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.resultCur = 35

	m = click(m, cellAt(t, m, "song 30"))
	if !atPane(m, focusResults) || m.resultCur != 30 {
		t.Errorf("click song 30: focus=%v resultCur=%d, want results/30", m.focus, m.resultCur)
	}

	m = click(m, cellAt(t, m, "queued 2"))
	if !atPane(m, focusQueue) || m.queueCur != 2 {
		t.Errorf("click queued 2: focus=%v queueCur=%d, want queue/2", m.focus, m.queueCur)
	}

	m = click(m, cellAt(t, m, "queued 2").Add(image.Pt(0, 3)))
	if !atPane(m, focusQueue) || m.queueCur != 2 {
		t.Errorf("click below queue: focus=%v queueCur=%d, want queue/2", m.focus, m.queueCur)
	}
}

func TestClickHeader(t *testing.T) {
	m := mouseModel()
	if !atPane(m, focusSearch) {
		t.Fatalf("new model focus = %v, want search", m.focus)
	}

	m = click(m, cellAt(t, m, "Queue"))
	if !atPane(m, focusQueue) || m.input.Focused() {
		t.Errorf("click Queue tab: focus=%v input focused=%v, want queue and blurred input", m.focus, m.input.Focused())
	}
	m = click(m, cellAt(t, m, "Playlists"))
	if !atPane(m, focusPlaylists) {
		t.Errorf("click Playlists tab: focus=%v", m.focus)
	}
	m = click(m, cellAt(t, m, "Playlists").Sub(image.Pt(1, 0))) // the gap between tabs
	if !atPane(m, focusPlaylists) {
		t.Errorf("click between tabs: focus=%v, want unchanged", m.focus)
	}
	m = click(m, cellAt(t, m, " / "))
	if !atPane(m, focusSearch) || !m.input.Focused() {
		t.Errorf("click search box: focus=%v input focused=%v", m.focus, m.input.Focused())
	}
}

func TestWheelMovesCursorOfPaneUnderPointer(t *testing.T) {
	m := mouseModel()
	at := cellAt(t, m, "queued 0")

	m = wheel(m, at, tea.MouseWheelDown)
	if !atPane(m, focusQueue) || m.queueCur != 1 || m.input.Focused() {
		t.Errorf("wheel down over queue: focus=%v queueCur=%d input focused=%v", m.focus, m.queueCur, m.input.Focused())
	}
	for range 5 {
		m = wheel(m, at, tea.MouseWheelDown)
	}
	if m.queueCur != 2 {
		t.Errorf("wheel past end: queueCur=%d, want 2", m.queueCur)
	}

	m.focus = focusResults
	m = wheel(m, cellAt(t, m, "song 00"), tea.MouseWheelUp)
	if !atPane(m, focusResults) || m.resultCur != 0 {
		t.Errorf("wheel up over results top: focus=%v resultCur=%d", m.focus, m.resultCur)
	}
}

func TestMouseInPlaylistPicker(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := store.CreateWithTracks(name, []youtube.Track{{Title: name + " song", URL: "https://www.youtube.com/watch?v=" + name}}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(Deps{Library: store})
	m.width, m.height = 100, 30
	m.focus = focusResults
	m.results = []youtube.Track{{Title: "pick me", URL: "https://www.youtube.com/watch?v=pick"}}
	got, _ := m.handleResultKey(keyPress("s"))
	m = got.(Model)

	m = click(m, cellAt(t, m, "Beta ("))
	if m.overlay != overlayPicker || m.playlistCur != 1 {
		t.Errorf("click Beta in picker: overlay=%v playlistCur=%d, want picker/1", m.overlay, m.playlistCur)
	}
	m = click(m, cellAt(t, m, "Beta song"))
	if m.overlay != overlayPicker {
		t.Errorf("click tracks pane in picker: overlay=%v, want picker kept", m.overlay)
	}
}

func TestMouseIgnoredWhileNamingPlaylist(t *testing.T) {
	m := mouseModel()
	m.overlay = overlayName
	m.input.Blur()
	m = click(m, image.Pt(1, 0))
	if m.overlay != overlayName {
		t.Errorf("click during name input: overlay=%v, want name input kept", m.overlay)
	}
}

type streamStub struct {
	*mpv.Player
	info mpv.StreamInfo
}

func (p streamStub) StreamInfo(context.Context) (mpv.StreamInfo, error) { return p.info, nil }

func TestInfoPanel(t *testing.T) {
	const watch = "https://www.youtube.com/watch?v=abc"
	info := mpv.StreamInfo{
		Path:   watch,
		Opened: "edl://!no_clip;%99%https://rr1.googlevideo.com/videoplayback?itag=251&mime=audio%2Fwebm&clen=4000000&dur=200",
		Codec:  "Opus (Opus Interactive Audio Codec)",
	}
	m := New(Deps{Player: streamStub{info: info}})
	m.width, m.height = 120, 40
	m.focus = focusQueue
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watch}}
	m.queue.pos, m.idle = 0, false
	m.tracks[watch] = youtube.Track{ID: "abc", Title: "Song", URL: watch}

	got, cmd := m.update(tea.KeyPressMsg{Code: 'i'})
	m = got.(Model)
	got, _ = m.update(cmd())
	m = got.(Model)
	if m.overlay != overlayInfo {
		t.Fatalf("overlay = %v, want info panel", m.overlay)
	}
	body := ansi.Strip(strings.Join(m.infoLines(), "\n"))
	for _, want := range []string{watch, "Opus (Opus", "itag 251 · audio/webm", "160 kbps average", "3.8 MiB"} {
		if !strings.Contains(body, want) {
			t.Errorf("info lines missing %q:\n%s", want, body)
		}
	}

	m.queue.entries = []mpv.PlaylistEntry{{Filename: "https://www.youtube.com/watch?v=next"}}
	if body := strings.Join(m.infoLines(), "\n"); strings.Contains(body, "itag") {
		t.Errorf("info shows the previous track's stream:\n%s", body)
	}
	if cmd := m.applyEvent(mpv.Event{Name: "file-loaded"}); cmd == nil {
		t.Error("file-loaded did not refresh the open info panel")
	}

	got, _ = m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m = got.(Model); !atPane(m, focusQueue) {
		t.Errorf("after esc: focus=%v overlay=%v, want queue", m.focus, m.overlay)
	}
}

func overlayModel(t *testing.T, origin focus) Model {
	t.Helper()
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const watch = "https://www.youtube.com/watch?v=abc"
	if _, err := store.CreateWithTracks("Keep", []youtube.Track{{Title: "Kept", URL: watch}}); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store, Player: streamStub{info: mpv.StreamInfo{Path: watch}}})
	m.width, m.height = 120, 40
	m.input.Blur()
	m.focus = origin
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watch, Title: "Song"}}
	m.queue.pos, m.idle = 0, false
	m.tracks[watch] = youtube.Track{ID: "abc", Title: "Song", URL: watch}
	m.devices = []mpv.AudioDevice{{Name: "auto", Description: "Autoselect device"}}
	return m
}

func press(t *testing.T, m Model, k tea.KeyPressMsg) Model {
	t.Helper()
	got, cmd := m.update(k)
	m = got.(Model)
	if k.Code == 'i' && cmd != nil {
		got, _ = m.update(cmd())
		m = got.(Model)
	}
	return m
}

func TestDevicePickerCloseRestoresOriginPane(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'o'}, {Code: 'q'}, {Code: tea.KeyEnter}} {
		t.Run(k.String(), func(t *testing.T) {
			m := overlayModel(t, focusQueue)
			got, _ := m.update(devicesMsg{devices: m.devices, open: true})
			m = got.(Model)
			if m.overlay != overlayDevices {
				t.Fatalf("overlay = %v, want device picker", m.overlay)
			}
			if m = press(t, m, k); !atPane(m, focusQueue) {
				t.Errorf("after %s: focus=%v overlay=%v, want queue", k, m.focus, m.overlay)
			}
		})
	}
}

func TestInfoCloseRestoresOriginPane(t *testing.T) {
	for name, origin := range map[string]focus{"queue": focusQueue, "playlists": focusPlaylists} {
		for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'i'}, {Code: 'q'}} {
			t.Run(name+"/"+k.String(), func(t *testing.T) {
				m := press(t, overlayModel(t, origin), tea.KeyPressMsg{Code: 'i'})
				if m.overlay != overlayInfo {
					t.Fatalf("overlay = %v, want info", m.overlay)
				}
				got, _ := m.update(k)
				if m = got.(Model); !atPane(m, origin) {
					t.Errorf("after %s: focus=%v overlay=%v, want %v", k, m.focus, m.overlay, origin)
				}
			})
		}
	}
}

func TestPlaylistPickerCloseRestoresOriginPane(t *testing.T) {
	m := press(t, overlayModel(t, focusQueue), tea.KeyPressMsg{Code: 's'})
	if m.overlay != overlayPicker {
		t.Fatalf("overlay = %v, want picker", m.overlay)
	}
	if m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}); !atPane(m, focusQueue) {
		t.Errorf("after esc: focus=%v overlay=%v, want queue", m.focus, m.overlay)
	}
}

func TestPlaylistNameCancelRestoresOriginPane(t *testing.T) {
	tests := []struct {
		name   string
		origin focus
		opens  []rune
	}{
		{name: "create from playlists", origin: focusPlaylists, opens: []rune{'c'}},
		{name: "create from picker opened in queue", origin: focusQueue, opens: []rune{'s', 'c'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := overlayModel(t, tt.origin)
			for _, r := range tt.opens {
				m = press(t, m, tea.KeyPressMsg{Code: r})
			}
			if m.overlay != overlayName {
				t.Fatalf("overlay = %v, want name input", m.overlay)
			}
			if m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}); !atPane(m, tt.origin) {
				t.Errorf("after esc: focus=%v overlay=%v, want %v", m.focus, m.overlay, tt.origin)
			}
		})
	}
}

func TestReplacedOverlayKeepsFirstOriginPane(t *testing.T) {
	m := press(t, overlayModel(t, focusQueue), tea.KeyPressMsg{Code: 's'})
	if m = press(t, m, tea.KeyPressMsg{Code: 'i'}); m.overlay != overlayInfo {
		t.Fatalf("overlay = %v, want info replacing picker", m.overlay)
	}
	if m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}); !atPane(m, focusQueue) {
		t.Errorf("after esc: focus=%v overlay=%v, want queue", m.focus, m.overlay)
	}
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

type spyTransportPlayer struct {
	*mpv.Player
	calls []string
}

func (p *spyTransportPlayer) TogglePause(context.Context) error {
	p.calls = append(p.calls, "toggle pause")
	return nil
}

func (p *spyTransportPlayer) Next(context.Context) error {
	p.calls = append(p.calls, "next")
	return nil
}

func (p *spyTransportPlayer) AddVolume(_ context.Context, delta int) error {
	if delta > 0 {
		p.calls = append(p.calls, "volume up")
	} else {
		p.calls = append(p.calls, "volume down")
	}
	return nil
}

func TestKeyAliases(t *testing.T) {
	const watch = "https://www.youtube.com/watch?v=abc"
	listModel := func(deps Deps, f focus) Model {
		m := New(deps)
		m.input.Blur()
		m.focus = f
		m.results = []youtube.Track{{URL: "r0"}, {URL: "r1"}, {URL: "r2"}}
		m.queue.entries = []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}}
		return m
	}
	playerCall := func(want string) func(t *testing.T, m Model, cmd tea.Cmd, player *spyTransportPlayer) {
		return func(t *testing.T, _ Model, cmd tea.Cmd, player *spyTransportPlayer) {
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

	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		setup func(t *testing.T, player *spyTransportPlayer) Model
		check func(t *testing.T, m Model, cmd tea.Cmd, player *spyTransportPlayer)
	}{
		{
			name: "space pauses",
			key:  tea.KeyPressMsg{Code: tea.KeySpace, Text: " "},
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				return listModel(Deps{Player: p}, focusResults)
			},
			check: playerCall("toggle pause"),
		},
		{
			name: "> skips to next track",
			key:  keyPress(">"),
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				return listModel(Deps{Player: p}, focusResults)
			},
			check: playerCall("next"),
		},
		{
			name: "= raises volume",
			key:  keyPress("="),
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				return listModel(Deps{Player: p}, focusResults)
			},
			check: playerCall("volume up"),
		},
		{
			name: "x removes the selected queue entry",
			key:  keyPress("x"),
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				m := listModel(Deps{Player: p}, focusQueue)
				m.queueCur = 1
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyTransportPlayer) {
				got := make([]string, len(m.queue.entries))
				for i, e := range m.queue.entries {
					got[i] = e.Filename
				}
				if !slices.Equal(got, []string{"A", "C"}) {
					t.Errorf("queue = %v, want [A C]", got)
				}
			},
		},
		{
			name: "tab leaves search",
			key:  tea.KeyPressMsg{Code: tea.KeyTab},
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				m := New(Deps{Player: p})
				m.input.SetValue("lofi")
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyTransportPlayer) {
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
			setup: func(t *testing.T, _ *spyTransportPlayer) Model {
				m := press(t, overlayModel(t, focusQueue), tea.KeyPressMsg{Code: 'i'})
				if m.overlay != overlayInfo {
					t.Fatalf("overlay = %v, want info", m.overlay)
				}
				return m
			},
			check: func(t *testing.T, m Model, cmd tea.Cmd, _ *spyTransportPlayer) {
				if cmd == nil {
					t.Fatal("cmd = nil, want a clipboard write")
				}
				if got, want := cmd(), tea.SetClipboard(watch)(); got != want {
					t.Errorf("cmd() = %#v, want clipboard write of %q", got, watch)
				}
			},
		},
		{
			name: "shift+insert pastes into search",
			key:  tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModShift},
			setup: func(t *testing.T, p *spyTransportPlayer) Model {
				if got := (tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModShift}).String(); got != "shift+insert" {
					t.Fatalf("key string = %q, want shift+insert", got)
				}
				return listModel(Deps{Player: p}, focusResults)
			},
			check: func(t *testing.T, m Model, cmd tea.Cmd, _ *spyTransportPlayer) {
				if !atPane(m, focusSearch) {
					t.Errorf("focus = %v, want focusSearch", m.focus)
				}
				// textinput.Paste reads the system clipboard, so compare instead of running it.
				if cmd == nil || reflect.ValueOf(cmd).Pointer() != reflect.ValueOf(textinput.Paste).Pointer() {
					t.Error("cmd is not the clipboard read")
				}
			},
		},
		{
			name: "k moves the cursor up",
			key:  keyPress("k"),
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				m := listModel(Deps{Player: p}, focusResults)
				m.resultCur = 2
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyTransportPlayer) {
				if m.resultCur != 1 {
					t.Errorf("resultCur = %d, want 1", m.resultCur)
				}
			},
		},
		{
			name: "g jumps to the top",
			key:  keyPress("g"),
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				m := listModel(Deps{Player: p}, focusResults)
				m.resultCur = 2
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyTransportPlayer) {
				if m.resultCur != 0 {
					t.Errorf("resultCur = %d, want 0", m.resultCur)
				}
			},
		},
		{
			name: "v toggles the spectrum",
			key:  keyPress("v"),
			setup: func(t *testing.T, p *spyTransportPlayer) Model {
				m := listModel(Deps{Player: p, Tap: spectrumStub{}}, focusResults)
				if !m.showViz {
					t.Fatal("showViz = false before toggling, want the spectrum shown with a tap")
				}
				return m
			},
			check: func(t *testing.T, m Model, _ tea.Cmd, _ *spyTransportPlayer) {
				if m.showViz {
					t.Error("showViz = true, want the spectrum toggled off")
				}
			},
		},
		{
			name: "q quits",
			key:  keyPress("q"),
			setup: func(_ *testing.T, p *spyTransportPlayer) Model {
				return listModel(Deps{Player: p}, focusResults)
			},
			check: func(t *testing.T, _ Model, cmd tea.Cmd, _ *spyTransportPlayer) {
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
			player := &spyTransportPlayer{}
			got, cmd := tt.setup(t, player).Update(tt.key)
			tt.check(t, got.(Model), cmd, player)
		})
	}
}

// Column headings are R4's wording; "Navigation and global" appears nowhere
// else, so it marks the full help as shown.
const (
	playbackHeading = "Playback"
	globalHeading   = "Navigation and global"
)

func rendered(m Model) string {
	return ansi.Strip(m.render())
}

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
			_, _, _, bodyHeight := m.layout()
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

func TestFullHelpKeepsEveryGroupVisibleAt40Columns(t *testing.T) {
	m := overlayModel(t, focusQueue)
	m.width = 40
	got := ansi.Strip(m.renderFullHelp(40))
	fullHelpGlobal := m.keys.global
	fullHelpGlobal.Quit = m.keys.closeHelp

	for _, group := range []struct {
		title string
		keys  help.KeyMap
	}{
		{title: playbackHeading, keys: m.keys.playback},
		{title: globalHeading, keys: fullHelpGlobal},
		{title: "Queue", keys: m.keys.queue},
	} {
		if !strings.Contains(got, group.title) {
			t.Errorf("40-column full help is missing the %q heading", group.title)
		}
		for _, b := range enabledEntries(group.keys) {
			if !listsEntry(got, b) {
				t.Errorf("40-column full help is missing %q from %s", helpEntry(b), group.title)
			}
		}
	}
}

func TestFullHelpAt24RowsWithSpectrumShowsQueueBindingsAndRestoresVisualizer(t *testing.T) {
	m := New(Deps{Tap: spectrumStub{}})
	m.width, m.height = 120, 24
	m.input.Blur()
	m.focus = focusQueue
	m.levels = []float64{1}
	if !m.showViz {
		t.Fatal("showViz = false with a spectrum tap, want true")
	}
	fullHelpGlobal := m.keys.global
	fullHelpGlobal.Quit = m.keys.closeHelp

	m = press(t, m, keyPress("?"))
	if !fullHelpShown(m) {
		t.Fatal("? did not open the full help")
	}
	if !m.showViz {
		t.Fatal("showViz changed while full help was open")
	}
	got := rendered(m)
	_, _, _, bodyHeight := m.layout()
	helpBody := ansi.Strip(m.renderFullHelp(bodyHeight))
	for _, group := range []struct {
		name string
		keys help.KeyMap
	}{
		{name: "Playback", keys: m.keys.playback},
		{name: "Navigation and global", keys: fullHelpGlobal},
		{name: "Queue", keys: m.keys.queue},
	} {
		for _, b := range enabledEntries(group.keys) {
			if !listsEntry(helpBody, b) {
				t.Errorf("24-row full help is missing %s binding %q", group.name, helpEntry(b))
			}
		}
	}
	if strings.Contains(got, "█") {
		t.Error("visualizer is rendered while full help is open")
	}

	m = press(t, m, keyPress("q"))
	if !m.showViz {
		t.Fatal("showViz changed after closing full help")
	}
	if !strings.Contains(rendered(m), "█") {
		t.Error("visualizer was not restored after closing full help")
	}
}

func TestFullHelpAt40x24AfterWindowSizeWithSpectrumShowsBindingsAndRestoresVisualizer(t *testing.T) {
	const watch = "https://www.youtube.com/watch?v=abc"
	m := New(Deps{Tap: spectrumStub{}})
	m.input.Blur()
	m.focus = focusQueue
	m.levels = []float64{1}
	m.queue.entries = []mpv.PlaylistEntry{{Filename: watch, Title: "Song"}}
	m.queue.pos, m.idle = 0, false
	m.tracks[watch] = youtube.Track{ID: "abc", Title: "Song", URL: watch}
	m.setStatus("status line text")
	got, _ := m.update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = got.(Model)
	if !m.showViz {
		t.Fatal("showViz = false with a spectrum tap, want true")
	}
	fullHelpGlobal := m.keys.global
	fullHelpGlobal.Quit = m.keys.closeHelp

	m = press(t, m, keyPress("?"))
	if !fullHelpShown(m) {
		t.Fatal("? did not open the full help")
	}
	if !m.showViz {
		t.Fatal("showViz changed while full help was open")
	}
	renderedHelp := rendered(m)
	if w, h := lipgloss.Width(m.render()), lipgloss.Height(m.render()); w > 40 || h > 24 {
		t.Errorf("render() size = %dx%d, want within 40x24", w, h)
	}
	for _, text := range []string{"▶ Song", "status line text"} {
		if !strings.Contains(renderedHelp, text) {
			t.Errorf("full help dropped %q (now-playing box / status line)", text)
		}
	}
	for _, group := range []struct {
		name string
		keys help.KeyMap
	}{
		{name: "Playback", keys: m.keys.playback},
		{name: "Navigation and global", keys: fullHelpGlobal},
		{name: "Queue", keys: m.keys.queue},
	} {
		for _, b := range enabledEntries(group.keys) {
			if !listsEntry(renderedHelp, b) {
				t.Errorf("40x24 full help is missing %s binding %q", group.name, helpEntry(b))
			}
		}
	}
	if strings.Contains(renderedHelp, "█") {
		t.Error("visualizer is rendered while full help is open")
	}

	m = press(t, m, keyPress("q"))
	if !m.showViz {
		t.Fatal("showViz changed after closing full help")
	}
	if !strings.Contains(rendered(m), "█") {
		t.Error("visualizer was not restored after closing full help")
	}
}

func TestFullHelpClosesWithQuestionMarkOrEsc(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{keyPress("?"), {Code: tea.KeyEscape}} {
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
	m.results = []youtube.Track{{Title: "A search result"}}
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
	if w, h := lipgloss.Width(m.render()), lipgloss.Height(m.render()); w > m.width || h > m.height {
		t.Errorf("render() size = %dx%d, want within %dx%d", w, h, m.width, m.height)
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
	player := &spyTransportPlayer{}
	m := overlayModel(t, focusQueue)
	m.deps.Player = player
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
		{"track info", func(t *testing.T, m Model) Model { return press(t, m, keyPress("i")) }, overlayInfo, "copy url"},
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
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
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
	searchBox := image.Pt(lipgloss.Width(renderTitle())+1, 0)
	m = press(t, m, keyPress("?"))

	for name, at := range map[string]image.Point{"pane row": row, "tab": tab, "search": searchBox} {
		got := click(m, at)
		if !atPane(got, focusResults) || got.queueCur != 0 || !fullHelpShown(got) {
			t.Errorf("click on %s: focus=%v queueCur=%d help=%v, want ignored", name, got.focus, got.queueCur, fullHelpShown(got))
		}
	}
	got := wheel(m, row, tea.MouseWheelDown)
	if !atPane(got, focusResults) || got.resultCur != 0 || got.queueCur != 0 {
		t.Errorf("wheel: focus=%v resultCur=%d queueCur=%d, want ignored", got.focus, got.resultCur, got.queueCur)
	}
}

func TestFullHelpListsVizOnlyWithSpectrum(t *testing.T) {
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
			m = resize(m, 120)
			m.input.Blur()
			m.focus = focusResults
			m = press(t, m, keyPress("?"))
			if !fullHelpShown(m) {
				t.Fatal("? did not open the full help")
			}
			viz := m.keys.global.Viz
			if got := listsEntry(rendered(m), viz); got != tc.want {
				t.Errorf("full help lists %q = %v, want %v", helpEntry(viz), got, tc.want)
			}
		})
	}
}

type spyOrderPlayer struct {
	*mpv.Player
	calls     []string
	repeats   []mpv.Repeat
	orders    [][]int
	reorderEr error
	playlist  []mpv.PlaylistEntry
}

func (p *spyOrderPlayer) SetRepeat(_ context.Context, r mpv.Repeat) error {
	p.calls = append(p.calls, "set repeat")
	p.repeats = append(p.repeats, r)
	return nil
}

func (p *spyOrderPlayer) Reorder(_ context.Context, _ int, _ string, order []int) error {
	p.calls = append(p.calls, "reorder")
	p.orders = append(p.orders, order)
	return p.reorderEr
}

func (p *spyOrderPlayer) Playlist(context.Context) ([]mpv.PlaylistEntry, int, error) {
	p.calls = append(p.calls, "playlist")
	return p.playlist, 1, nil
}

func (p *spyOrderPlayer) PlayIndex(context.Context, int) error {
	p.calls = append(p.calls, "play index")
	return nil
}

func (p *spyOrderPlayer) Seek(context.Context, time.Duration) error {
	p.calls = append(p.calls, "seek")
	return nil
}

// R1, R2: L asks mpv for the next mode; only mpv's loop-* reports change the display.
func TestRepeatKeyCyclesThroughMPV(t *testing.T) {
	for _, f := range []focus{focusResults, focusQueue, focusPlaylists} {
		player := &spyOrderPlayer{}
		m := New(Deps{Player: player})
		m.focus = f
		got, cmd := m.handleKey(keyPress("L"))
		m = got.(Model)
		if cmd == nil {
			t.Fatalf("focus %d: L returned no command", f)
		}
		cmd()
		if !slices.Equal(player.repeats, []mpv.Repeat{mpv.RepeatAll}) {
			t.Fatalf("focus %d: SetRepeat calls = %v, want [all]", f, player.repeats)
		}
		if strings.Contains(m.audioLine(), "repeat") {
			t.Fatalf("focus %d: display changed before mpv reported: %q", f, m.audioLine())
		}
	}

	player := &spyOrderPlayer{}
	m := New(Deps{Player: player})
	m.focus = focusResults
	steps := []struct {
		prop, value string
		display     string
		next        mpv.Repeat
	}{
		{mpv.PropLoopPlaylist, `"inf"`, "repeat all", mpv.RepeatOne},
		{mpv.PropLoopFile, `"inf"`, "repeat one", mpv.RepeatOff},
		{mpv.PropLoopPlaylist, `false`, "repeat one", mpv.RepeatOff},
		{mpv.PropLoopFile, `false`, "", mpv.RepeatAll},
	}
	for _, step := range steps {
		m.applyProperty(mpv.Event{Name: "property-change", Prop: step.prop, Data: json.RawMessage(step.value)})
		line := m.audioLine()
		if step.display == "" && strings.Contains(line, "repeat") || step.display != "" && !strings.Contains(line, step.display) {
			t.Fatalf("after %s=%s audio line = %q, want %q", step.prop, step.value, line, step.display)
		}
		got, cmd := m.handleKey(keyPress("L"))
		m = got.(Model)
		cmd()
		if last := player.repeats[len(player.repeats)-1]; last != step.next {
			t.Fatalf("after %s=%s L requested %v, want %v", step.prop, step.value, last, step.next)
		}
	}
}

func shuffleModel(player Player, pos int, names ...string) Model {
	m := New(Deps{Player: player})
	m.focus = focusQueue
	m.rng = rand.New(rand.NewPCG(1, 2))
	for _, n := range names {
		m.queue.entries = append(m.queue.entries, mpv.PlaylistEntry{Filename: n})
	}
	m.queue.pos = pos
	m.idle = pos < 0
	return m
}

func filenames(entries []mpv.PlaylistEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Filename
	}
	return names
}

// R4, R5: the prefix and current track stay put, the tail is permuted, and nothing restarts playback.
func TestShuffleKeepsCurrentAndPrefix(t *testing.T) {
	player := &spyOrderPlayer{}
	m := shuffleModel(player, 1, "A", "B", "C", "D", "E")
	m.queueCur = 1
	got, cmd := m.handleKey(keyPress("Z"))
	m = got.(Model)
	if cmd == nil {
		t.Fatal("Z returned no command")
	}
	view := filenames(m.queue.entries)
	if view[0] != "A" || view[1] != "B" {
		t.Fatalf("view = %v, want A, B in place", view)
	}
	if tail := slices.Sorted(slices.Values(view[2:])); !slices.Equal(tail, []string{"C", "D", "E"}) {
		t.Fatalf("view tail = %v, want a permutation of C, D, E", view[2:])
	}
	if cur, _, ok := m.current(); !ok || cur.Filename != "B" || m.queue.pos != 1 {
		t.Fatalf("current = %+v (pos %d), want B at 1", cur, m.queue.pos)
	}
	if m.queue.editsPending != 1 || m.queue.projection == nil {
		t.Fatal("shuffle was not projected through the queue-sync write path")
	}
	msg := cmd().(queueActionDoneMsg)
	if msg.err != nil || !msg.projected {
		t.Fatalf("reorder msg = %+v", msg)
	}
	if !slices.Equal(player.calls, []string{"reorder"}) {
		t.Fatalf("player calls = %v, want only reorder", player.calls)
	}
	order := player.orders[0]
	for i, from := range order {
		if view[i] != []string{"A", "B", "C", "D", "E"}[from] {
			t.Fatalf("view %v disagrees with sent order %v", view, order)
		}
	}
	// A playlist event from before the reorder must not undo the projected order.
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"A"},{"filename":"B"},{"filename":"C"},{"filename":"D"},{"filename":"E"}]`)})
	if !slices.Equal(filenames(m.queue.entries), view) {
		t.Fatalf("stale event replaced shuffled view: %v", filenames(m.queue.entries))
	}
}

// R4: with nothing playing the whole queue is shuffled.
func TestShuffleWithoutCurrentShufflesAll(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	moved := false
	for seed := range uint64(20) {
		player := &spyOrderPlayer{}
		m := shuffleModel(player, -1, names...)
		m.rng = rand.New(rand.NewPCG(seed, seed))
		got, cmd := m.handleKey(keyPress("Z"))
		m = got.(Model)
		if cmd == nil {
			t.Fatal("Z returned no command")
		}
		view := filenames(m.queue.entries)
		if !slices.Equal(slices.Sorted(slices.Values(view)), names) {
			t.Fatalf("view = %v, want a permutation of %v", view, names)
		}
		if view[0] != "A" {
			moved = true
		}
	}
	if !moved {
		t.Error("index 0 never moved across 20 seeds; the whole queue was not shuffled")
	}
}

// R5: the cursor follows the selected track to its new index.
func TestShuffleKeepsSelectionOnTrack(t *testing.T) {
	for seed := range uint64(10) {
		m := shuffleModel(&spyOrderPlayer{}, 0, "A", "B", "C", "D", "E", "F")
		m.rng = rand.New(rand.NewPCG(seed, 7))
		m.queueCur = 3
		got, _ := m.handleKey(keyPress("Z"))
		m = got.(Model)
		if sel := m.queue.entries[m.queueCur].Filename; sel != "D" {
			t.Fatalf("seed %d: selection = %q at %d, want D", seed, sel, m.queueCur)
		}
	}
}

// R5: fewer than two tracks to reorder leaves the queue alone with a status message.
func TestShuffleNeedsTwoTracks(t *testing.T) {
	for _, tt := range []struct {
		pos   int
		names []string
	}{
		{1, []string{"A", "B", "C"}},
		{2, []string{"A", "B", "C"}},
		{-1, []string{"A"}},
		{-1, nil},
	} {
		player := &spyOrderPlayer{}
		m := shuffleModel(player, tt.pos, tt.names...)
		m.status = ""
		got, cmd := m.handleKey(keyPress("Z"))
		m = got.(Model)
		if cmd != nil || len(player.calls) != 0 || m.queue.editsPending != 0 {
			t.Fatalf("pos %d %v: shuffle issued a write", tt.pos, tt.names)
		}
		if !slices.Equal(filenames(m.queue.entries), filenames(shuffleModel(nil, tt.pos, tt.names...).queue.entries)) {
			t.Fatalf("pos %d %v: queue changed to %v", tt.pos, tt.names, filenames(m.queue.entries))
		}
		if m.status == "" {
			t.Fatalf("pos %d %v: no status message", tt.pos, tt.names)
		}
	}
}

// R5: a failed reorder reports the error and resyncs the queue from mpv.
func TestShuffleErrorResyncsFromMPV(t *testing.T) {
	want := errors.New("playlist-move failed")
	original := []mpv.PlaylistEntry{{Filename: "A"}, {Filename: "B"}, {Filename: "C"}, {Filename: "D"}}
	player := &spyOrderPlayer{reorderEr: want, playlist: original}
	m := shuffleModel(player, 1, "A", "B", "C", "D")
	got, cmd := m.handleKey(keyPress("Z"))
	m = got.(Model)
	if cmd == nil {
		t.Fatal("Z returned no command")
	}
	got, refresh := m.update(cmd())
	m = got.(Model)
	if !m.statusErr || !strings.Contains(m.status, want.Error()) {
		t.Fatalf("status = %q (err %v), want reorder error", m.status, m.statusErr)
	}
	if refresh == nil {
		t.Fatal("failed reorder scheduled no resync")
	}
	got, _ = m.update(refresh())
	m = got.(Model)
	if !slices.Equal(filenames(m.queue.entries), filenames(original)) || m.queue.projection != nil {
		t.Fatalf("queue after resync = %v, want mpv's %v", filenames(m.queue.entries), filenames(original))
	}
}

// R5: playlist-pos only follows moves once mpv reports them, so Z waits until
// earlier edits are confirmed instead of shuffling from a stale current index.
func TestShuffleWaitsForPendingEdits(t *testing.T) {
	player := &spyOrderPlayer{}
	m := shuffleModel(player, 0, "A", "B", "C", "D", "E")
	m.queueCur = 0
	got, _ := m.handleKey(keyPress("J"))
	m = got.(Model)
	moved := filenames(m.queue.entries)

	got, cmd := m.handleKey(keyPress("Z"))
	m = got.(Model)
	if cmd != nil || len(player.orders) != 0 || !slices.Equal(filenames(m.queue.entries), moved) {
		t.Fatalf("Z during a pending move issued a reorder: cmd %v, orders %v, view %v", cmd != nil, player.orders, filenames(m.queue.entries))
	}

	got, _ = m.update(queueActionDoneMsg{projected: true})
	m = got.(Model)
	got, cmd = m.handleKey(keyPress("Z"))
	m = got.(Model)
	if cmd != nil || len(player.orders) != 0 || !slices.Equal(filenames(m.queue.entries), moved) {
		t.Fatalf("Z before the move was read back issued a reorder: cmd %v, orders %v, view %v", cmd != nil, player.orders, filenames(m.queue.entries))
	}
}

func TestShuffleKeyOnlyInQueuePane(t *testing.T) {
	for _, f := range []focus{focusResults, focusPlaylists, focusPlaylistTracks} {
		player := &spyOrderPlayer{}
		m := shuffleModel(player, 0, "A", "B", "C", "D")
		m.focus = f
		got, cmd := m.handleKey(keyPress("Z"))
		m = got.(Model)
		if cmd != nil || len(player.calls) != 0 || !slices.Equal(filenames(m.queue.entries), []string{"A", "B", "C", "D"}) {
			t.Fatalf("focus %d: Z acted outside the Queue pane", f)
		}
	}
}

// R6 [derived]: L is a playback binding and Z a Queue binding; both appear in full help, not the footer.
func TestFullHelpListsRepeatAndQueueShuffle(t *testing.T) {
	m := press(t, overlayModel(t, focusQueue), keyPress("?"))
	got := rendered(m)
	for _, entry := range []key.Binding{
		key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "repeat")),
		key.NewBinding(key.WithKeys("Z"), key.WithHelp("Z", "shuffle")),
	} {
		if !listsEntry(got, entry) {
			t.Errorf("Queue full help is missing %q:\n%s", helpEntry(entry), got)
		}
	}
	footer := footerHelp(overlayModel(t, focusQueue))
	for _, entry := range []string{"L repeat", "Z shuffle"} {
		if strings.Contains(footer, entry) {
			t.Errorf("Queue short help %q lists %q", footer, entry)
		}
	}
}

func filterModel() (Model, *spyPlaylistPlayer) {
	player := &spyPlaylistPlayer{}
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

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = press(t, m, keyPress(string(r)))
	}
	return m
}

func selectedTitle(m Model) string {
	tr, _ := m.selectedResult()
	return tr.Title
}

func renderedRows(m Model) string {
	return ansi.Strip(m.render())
}

var (
	escKey   = tea.KeyPressMsg{Code: tea.KeyEscape}
	upKey    = tea.KeyPressMsg{Code: tea.KeyUp}
	ctrlJKey = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	ctrlKKey = tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
)

// R1
func TestFilterKeyIgnoredWithoutResults(t *testing.T) {
	m, _ := filterModel()
	m.results = nil
	m = press(t, m, keyPress("f"))
	if m.filterInput.Focused() || m.filterInput.Value() != "" {
		t.Errorf("f without results: filter focused=%v value=%q, want closed and empty", m.filterInput.Focused(), m.filterInput.Value())
	}
}

// R1
func TestFilterKeyOpensAndFocusesInput(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	if !m.filterInput.Focused() || !atPane(m, focusResults) {
		t.Fatalf("after f: filter focused=%v focus=%v, want focused filter in results", m.filterInput.Focused(), m.focus)
	}
	if m.filterInput.Value() != "" {
		t.Errorf("filter value = %q, want empty: f opens, it is not typed", m.filterInput.Value())
	}
}

// R2
func TestFilterNarrowsOnEveryKeystroke(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "l")
	if got := renderedRows(m); strings.Contains(got, "rock") || !strings.Contains(got, "lofi beats") {
		t.Errorf("after typing l: rows = %q, want rock hidden and lofi beats shown", got)
	}
	m = typeText(t, m, "ofi b")
	if got := renderedRows(m); strings.Contains(got, "rock") || !strings.Contains(got, "lofi beats") {
		t.Errorf("after typing lofi b: rows = %q, want only lofi beats", got)
	}
	if got := selectedTitle(m); got != "lofi beats" {
		t.Errorf("selected = %q, want the only visible row lofi beats", got)
	}
}

// R3
func TestFilterInputMovesSelectionWithoutLeaving(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	steps := []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{keyPress("down"), "lofi beats"},
		{upKey, "lofi"},
		{ctrlJKey, "lofi beats"},
		{ctrlKKey, "lofi"},
	}
	for _, s := range steps {
		m = press(t, m, s.key)
		if got := selectedTitle(m); got != s.want {
			t.Errorf("after %s: selected = %q, want %q", s.key, got, s.want)
		}
		if !m.filterInput.Focused() || m.filterInput.Value() != "lofi" {
			t.Errorf("after %s: filter focused=%v value=%q, want still typing lofi", s.key, m.filterInput.Focused(), m.filterInput.Value())
		}
	}
}

func TestFilterInputBlocksGlobalKeys(t *testing.T) {
	m, player := filterModel()
	m = press(t, m, keyPress("f"))
	got, cmd := m.update(keyPress("q"))
	m = got.(Model)
	if cmd != nil {
		t.Errorf("q in filter returned a command, want it typed")
	}
	m = typeText(t, m, "/a?")
	if m.filterInput.Value() != "q/a?" || !m.filterInput.Focused() || !atPane(m, focusResults) || m.fullHelp {
		t.Errorf("filter value=%q focused=%v focus=%v fullHelp=%v, want q/a? typed into the open filter",
			m.filterInput.Value(), m.filterInput.Focused(), m.focus, m.fullHelp)
	}
	if len(player.calls) != 0 {
		t.Errorf("player calls = %v, want none", player.calls)
	}
}

// R4
func TestFilterEnterCommitsAndKeepsFilter(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	m = press(t, m, keyPress("enter"))
	if m.filterInput.Focused() || !atPane(m, focusResults) {
		t.Errorf("after enter: filter focused=%v focus=%v, want results list focused", m.filterInput.Focused(), m.focus)
	}
	if m.filterInput.Value() != "lofi" || strings.Contains(renderedRows(m), "rock") {
		t.Errorf("after enter: value=%q, want lofi kept and rock hidden", m.filterInput.Value())
	}
}

// R4
func TestFilterEscClearsFromInputAndList(t *testing.T) {
	m, _ := filterModel()
	m.filterInput.Focus()
	m.filterInput.SetValue("lofi")
	m = press(t, m, escKey)
	if m.filterInput.Focused() || m.filterInput.Value() != "" || !strings.Contains(renderedRows(m), "rock") {
		t.Errorf("esc in input: focused=%v value=%q, want closed, cleared, rock shown", m.filterInput.Focused(), m.filterInput.Value())
	}

	m.filterInput.SetValue("lofi")
	m = press(t, m, escKey)
	if m.filterInput.Value() != "" || !strings.Contains(renderedRows(m), "rock") || !atPane(m, focusResults) {
		t.Errorf("esc in list: value=%q focus=%v, want cleared, rock shown, results focused", m.filterInput.Value(), m.focus)
	}
}

// R5
func TestFilteredEnqueueUsesVisibleRow(t *testing.T) {
	m, player := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	m = press(t, m, keyPress("enter"))
	m = press(t, m, keyPress("j"))
	_, cmd := m.update(keyPress("a"))
	if cmd == nil {
		t.Fatal("a returned no command, want an append")
	}
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("append did not run")
	}
	if !slices.Equal(player.calls, []string{"append c"}) {
		t.Errorf("calls = %v, want C (lofi beats) appended", player.calls)
	}
}

// R5
func TestFilteredNavigationBounds(t *testing.T) {
	m, _ := filterModel()
	m.filterInput.SetValue("lofi")
	m = press(t, m, keyPress("G"))
	if got := selectedTitle(m); got != "lofi beats" || m.resultCur != 1 {
		t.Errorf("G: selected=%q cur=%d, want lofi beats at row 1", got, m.resultCur)
	}
	m = press(t, m, keyPress("j"))
	if m.resultCur != 1 {
		t.Errorf("j past end: cur=%d, want 1", m.resultCur)
	}
	m = press(t, m, keyPress("g"))
	if got := selectedTitle(m); got != "lofi" {
		t.Errorf("g: selected=%q, want lofi", got)
	}
}

// R5
func TestFilteredClickSelectsShownTrack(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.filterInput.SetValue("3")
	m = click(m, cellAt(t, m, "song 35"))
	if got := selectedTitle(m); !atPane(m, focusResults) || got != "song 35" {
		t.Errorf("click song 35: focus=%v selected=%q, want results/song 35", m.focus, got)
	}
}

// R5
func TestFilteredWheelBoundedByVisibleRows(t *testing.T) {
	m := mouseModel()
	m.focus = focusResults
	m.input.Blur()
	m.filterInput.SetValue("3")
	at := cellAt(t, m, "song 30")
	for range 20 {
		m = wheel(m, at, tea.MouseWheelDown)
	}
	if got := selectedTitle(m); m.resultCur != 12 || got != "song 39" {
		t.Errorf("wheel past end: cur=%d selected=%q, want row 12 song 39 (13 rows contain 3)", m.resultCur, got)
	}
}

// R6
func TestNewSearchClearsFilter(t *testing.T) {
	m, _ := filterModel()
	m.filterInput.Focus()
	m.filterInput.SetValue("lofi")
	m.searchRequest = m.nextRequest()
	got, _ := m.update(searchDoneMsg{requestID: m.searchRequest, query: "new", tracks: []youtube.Track{
		{Title: "jazz", URL: "j"}, {Title: "blues", URL: "k"},
	}})
	m = got.(Model)
	if m.filterInput.Value() != "" || !m.filterInput.Focused() {
		t.Errorf("after new results: filter value=%q focused=%v, want cleared and still focused", m.filterInput.Value(), m.filterInput.Focused())
	}
	if rows := renderedRows(m); !strings.Contains(rows, "jazz") || !strings.Contains(rows, "blues") {
		t.Errorf("rows = %q, want all new results shown", rows)
	}
}

func resultsTitleLine(t *testing.T, m Model) string {
	t.Helper()
	for line := range strings.SplitSeq(renderedRows(m), "\n") {
		if strings.Contains(line, "│"+resultsTitle) {
			return line
		}
	}
	t.Fatalf("no Results title line in %q", renderedRows(m))
	return ""
}

// R3
func TestFilterTitleCountsMatchedOverTotal(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	if got := resultsTitleLine(t, m); !strings.Contains(got, "2/3") {
		t.Errorf("title = %q, want count 2/3", got)
	}
}

// R3
func TestFilterWithoutMatchesSaysSo(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "jazz")
	if got := resultsTitleLine(t, m); !strings.Contains(got, "0/3") {
		t.Errorf("title = %q, want count 0/3", got)
	}
	if got := renderedRows(m); !strings.Contains(got, "no matches") {
		t.Errorf("rows = %q, want no matches", got)
	}
}

// R3
func TestFilterHighlightsMatchedSubstrings(t *testing.T) {
	m, _ := filterModel()
	m.results[2].Channel = "Lofi Girl"
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	raw := m.render()
	if want := matchStyle.Render("lofi") + " beats"; !strings.Contains(raw, want) {
		t.Errorf("render lacks %q: lofi highlighted, beats plain", want)
	}
	if want := matchStyle.Inherit(dimStyle).Render("Lofi"); !strings.Contains(raw, want) {
		t.Errorf("render lacks %q: channel match highlighted over dim", want)
	}
	if want := matchStyle.Inherit(cursorStyle).Render("lofi"); !strings.Contains(raw, want) {
		t.Errorf("render lacks %q: selected row keeps its highlight", want)
	}
	if strings.Contains(raw, matchStyle.Render(" beats")) || strings.Contains(raw, matchStyle.Render("lofi beats")) {
		t.Error("non-matching text is highlighted")
	}
}

// R3
func TestNoCountOrHighlightWithoutFilter(t *testing.T) {
	m, _ := filterModel()
	if got := resultsTitleLine(t, m); strings.Contains(got, "/3") {
		t.Errorf("title = %q, want no count without a filter", got)
	}
	if strings.Contains(m.render(), matchStyle.Render("lofi")) {
		t.Error("render highlights lofi without a filter")
	}
}

// R3
func TestFilterHighlightSurvivesTruncation(t *testing.T) {
	m, _ := filterModel()
	m = resize(m, 60)
	m.results[2].Title = "lofi " + strings.Repeat("x", 100) + " tail"
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi tail")
	_, _, leftW := m.bodyPanes()
	pane := m.renderPanes(20)
	for i, line := range strings.Split(pane, "\n") {
		if w := lipgloss.Width(line); w != m.width {
			t.Errorf("line %d width = %d, want %d: %q", i, w, m.width, ansi.Strip(line))
		}
		if left := ansi.Cut(line, 0, leftW); lipgloss.Width(left) > leftW {
			t.Errorf("line %d results part wider than pane %d", i, leftW)
		}
	}
	if lipgloss.Height(pane) != 20 {
		t.Errorf("pane height = %d, want 20", lipgloss.Height(pane))
	}
	if !strings.Contains(pane, matchStyle.Inherit(cursorStyle).Render("lofi")) {
		t.Error("truncated row lost the lofi highlight")
	}
	if row := ansi.Strip(strings.Split(pane, "\n")[2]); !strings.HasPrefix(row, "│lofi xxx") || strings.Contains(row, "tail") {
		t.Errorf("row = %q, want lofi truncated before tail", row)
	}
}

// R7
func TestResultsHelpListsFilterAndClearOnlyWhenFiltered(t *testing.T) {
	m, _ := filterModel()
	got := footerHelp(resize(m, 500))
	if !strings.Contains(got, "f filter") {
		t.Errorf("results help %q is missing f filter", got)
	}
	if strings.Contains(got, "esc clear filter") {
		t.Errorf("results help %q offers esc clear filter with no filter applied", got)
	}

	m.filterInput.SetValue("lofi")
	got = footerHelp(resize(m, 500))
	if !strings.Contains(got, "f filter") || !strings.Contains(got, "esc clear filter") {
		t.Errorf("filtered results help %q, want f filter and esc clear filter", got)
	}

	m.fullHelp = true
	full := ansi.Strip(m.renderFullHelp(30))
	for _, b := range []key.Binding{m.keys.results.Filter, m.keys.results.ClearFilter} {
		if !listsEntry(full, b) {
			t.Errorf("filtered results full help is missing %q:\n%s", helpEntry(b), full)
		}
	}
}

// R7
func TestFilterInputHelpShowsFilterKeysNotResultKeys(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	got := footerHelp(resize(m, 500))
	for _, want := range []string{"↑/ctrl+k up", "↓/ctrl+j down", "enter apply filter", "esc clear filter", "? more"} {
		if !strings.Contains(got, want) {
			t.Errorf("filter input help %q is missing %q", got, want)
		}
	}
	for _, unwanted := range []string{"a queue", "s save", "f filter", "enter play now"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("filter input help %q shows the results key %q", got, unwanted)
		}
	}
}

func newResults(t *testing.T, m Model) Model {
	t.Helper()
	m.searchRequest = m.nextRequest()
	got, _ := m.update(searchDoneMsg{requestID: m.searchRequest, query: "new", tracks: []youtube.Track{
		{Title: "jazz", URL: "j"}, {Title: "blues", URL: "k"},
	}})
	return got.(Model)
}

// R6
func TestNewSearchKeepsTypingInFilter(t *testing.T) {
	m, player := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lo")
	m = newResults(t, m)
	got, cmd := m.update(keyPress("q"))
	m = got.(Model)
	if cmd != nil {
		t.Error("q after new results returned a command, want it typed into the filter")
	}
	if m.filterInput.Value() != "q" || !m.filterInput.Focused() || !atPane(m, focusResults) {
		t.Errorf("filter value=%q focused=%v focus=%v, want q typed into the still-open filter",
			m.filterInput.Value(), m.filterInput.Focused(), m.focus)
	}
	if len(player.calls) != 0 {
		t.Errorf("player calls = %v, want none", player.calls)
	}
}

// R6
func TestNewSearchLeavesUnfocusedFilterClosed(t *testing.T) {
	m, _ := filterModel()
	m.filterInput.SetValue("lofi")
	m = newResults(t, m)
	if m.filterInput.Value() != "" || m.filterInput.Focused() || m.filterOpen() {
		t.Errorf("filter value=%q focused=%v, want cleared and closed", m.filterInput.Value(), m.filterInput.Focused())
	}
}

func TestFilterPasteReadsClipboardIntoFilter(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	got, cmd := m.update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	m = got.(Model)
	// textinput.Paste reads the system clipboard, so compare instead of running it.
	if cmd == nil || reflect.ValueOf(cmd).Pointer() != reflect.ValueOf(textinput.Paste).Pointer() {
		t.Fatal("ctrl+v in filter: cmd is not the clipboard read")
	}
	if !m.filterInput.Focused() || !atPane(m, focusResults) {
		t.Fatalf("ctrl+v left the filter: focused=%v focus=%v", m.filterInput.Focused(), m.focus)
	}

	// The clipboard result type is unexported; build one carrying known text.
	msg := reflect.New(reflect.TypeOf(textinput.Paste())).Elem()
	if msg.Kind() != reflect.String {
		t.Skipf("clipboard unavailable: %v", msg.Type())
	}
	msg.SetString("lofi")
	got, _ = m.update(msg.Interface())
	m = got.(Model)
	if m.filterInput.Value() != "lofi" || m.input.Value() != "" {
		t.Errorf("clipboard paste: filter=%q search=%q, want lofi in the filter only", m.filterInput.Value(), m.input.Value())
	}
}

func TestFilterWidthIgnoresTabAtResize(t *testing.T) {
	m, _ := filterModel()
	want := resize(m, 100).filterInput.Width()
	m.focus = focusPlaylists
	m = resize(m, 100)
	m.focus = focusResults
	m = press(t, m, keyPress("f"))
	if got := m.filterInput.Width(); got != want {
		t.Errorf("filter width after resizing on Playlists = %d, want %d as when resized on Results", got, want)
	}
}

// R4
func TestFilterClearKeepsSelectedTrack(t *testing.T) {
	for _, commit := range []bool{false, true} {
		m, _ := filterModel()
		m = press(t, m, keyPress("f"))
		m = typeText(t, m, "lofi")
		m = press(t, m, keyPress("down"))
		if commit {
			m = press(t, m, keyPress("enter"))
		}
		m = press(t, m, escKey)
		if m.resultCur != 2 || selectedTitle(m) != "lofi beats" {
			t.Errorf("commit=%v: after esc cur=%d selected=%q, want lofi beats at 2", commit, m.resultCur, selectedTitle(m))
		}
	}
}

func TestHighlightSurvivesControlBytes(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		spans []span
		width int
		want  int
	}{
		{"trailing escape", "abcdef\x1b[0m", nil, 5, 5},
		{"inner escapes", "abc\x1b[31mred\x1b[0m tail", nil, 3, 3},
		{"escape before span", "x\x1b[0mlofi", []span{{5, 9}}, 20, 8},
		{"wide runes", "日本語日本語", nil, 5, 5},
		{"fits", "ab\x07c", nil, 5, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highlight(tt.s, tt.spans, tt.width, lipgloss.NewStyle())
			visible := ansi.Strip(got)
			if strings.ContainsFunc(visible, unicode.IsControl) {
				t.Errorf("highlight(%q) = %q, want no control bytes", tt.s, got)
			}
			if w := lipgloss.Width(got); w != tt.want {
				t.Errorf("highlight(%q, %d) width = %d (%q), want %d", tt.s, tt.width, w, visible, tt.want)
			}
			if tt.spans != nil && !strings.Contains(got, matchStyle.Render("lofi")) {
				t.Errorf("highlight(%q) = %q, want lofi highlighted", tt.s, got)
			}
		})
	}
}

func TestResultsRenderWithEscapeInChannel(t *testing.T) {
	m, _ := filterModel()
	m = resize(m, 100)
	m.results[0].Channel = "chanchanchanchanchan1\x1b[0m"
	m.results[1].Title = "abc\x1b[31mred\x1b[0m tail"
	pane := m.renderPanes(20)
	for i, line := range strings.Split(pane, "\n") {
		if w := lipgloss.Width(line); w != m.width {
			t.Errorf("line %d width = %d, want %d: %q", i, w, m.width, ansi.Strip(line))
		}
	}
}

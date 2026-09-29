package tui

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/youtube"
)

func matchIndices(ms []filterMatch) []int {
	out := make([]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.index)
	}
	return out
}

func TestFilterResults(t *testing.T) {
	tracks := []youtube.Track{
		{Title: "Lofi Hip Hop Radio", Channel: "Lofi Girl"},
		{Title: "Rock Classics", Channel: "Rock Channel"},
		{Title: "lofi beats to study", Channel: "ChilledCow"},
		{Title: "Jazz Night", Channel: "LOFI jazz collective"},
	}
	tests := []struct {
		name  string
		query string
		want  []int
	}{
		{name: "case-insensitive channel match", query: "chilledcow", want: []int{2}},
		{name: "channel match upper-case query", query: "COLLECTIVE", want: []int{3}},
		{name: "title or channel, order preserved", query: "lofi", want: []int{0, 2, 3}},
		{name: "multi-term AND", query: "lofi study", want: []int{2}},
		{name: "multi-term AND any order", query: "study lofi", want: []int{2}},
		{name: "terms may match across title and channel", query: "jazz night lofi", want: []int{3}},
		{name: "one term missing excludes row", query: "rock lofi", want: []int{}},
		{name: "extra spaces between terms", query: "  study   lofi ", want: []int{2}},
		{name: "empty query matches all", query: "", want: []int{0, 1, 2, 3}},
		{name: "whitespace query matches all", query: "   ", want: []int{0, 1, 2, 3}},
		{name: "no match", query: "metal", want: []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchIndices(filterResults(tracks, tt.query))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("filterResults(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestFilterResultsSpans(t *testing.T) {
	tracks := []youtube.Track{{Title: "Lofi Beats", Channel: "Chill LOFI"}}
	got := filterResults(tracks, "lofi beat")
	want := []filterMatch{{
		index:   0,
		title:   []span{{0, 4}, {5, 9}},
		channel: []span{{6, 10}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterResults spans = %+v, want %+v", got, want)
	}
}

// results-filter R1
func TestFilterKeyIgnoredWithoutResults(t *testing.T) {
	m, _ := filterModel()
	m.results.tracks = nil
	m = press(t, m, keyPress("f"))
	if m.results.filter.Focused() || m.results.filter.Value() != "" {
		t.Errorf("f without results: filter focused=%v value=%q, want closed and empty", m.results.filter.Focused(), m.results.filter.Value())
	}
}

// results-filter R1
func TestFilterKeyOpensAndFocusesInput(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	if !m.results.filter.Focused() || !atPane(m, focusResults) {
		t.Fatalf("after f: filter focused=%v focus=%v, want focused filter in results", m.results.filter.Focused(), m.focus)
	}
	if m.results.filter.Value() != "" {
		t.Errorf("filter value = %q, want empty: f opens, it is not typed", m.results.filter.Value())
	}
}

// results-filter R2
func TestFilterNarrowsOnEveryKeystroke(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "l")
	if got := rendered(m); strings.Contains(got, "rock") || !strings.Contains(got, "lofi beats") {
		t.Errorf("after typing l: rows = %q, want rock hidden and lofi beats shown", got)
	}
	m = typeText(t, m, "ofi b")
	if got := rendered(m); strings.Contains(got, "rock") || !strings.Contains(got, "lofi beats") {
		t.Errorf("after typing lofi b: rows = %q, want only lofi beats", got)
	}
	if got := selectedTitle(m); got != "lofi beats" {
		t.Errorf("selected = %q, want the only visible row lofi beats", got)
	}
}

// results-filter R3
func TestFilterInputMovesSelectionWithoutLeaving(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	steps := []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{keyPress("down"), "lofi beats"},
		{keyPress("up"), "lofi"},
		{tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}, "lofi beats"},
		{tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}, "lofi"},
	}
	for _, s := range steps {
		m = press(t, m, s.key)
		if got := selectedTitle(m); got != s.want {
			t.Errorf("after %s: selected = %q, want %q", s.key, got, s.want)
		}
		if !m.results.filter.Focused() || m.results.filter.Value() != "lofi" {
			t.Errorf("after %s: filter focused=%v value=%q, want still typing lofi", s.key, m.results.filter.Focused(), m.results.filter.Value())
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
	if m.results.filter.Value() != "q/a?" || !m.results.filter.Focused() || !atPane(m, focusResults) || m.fullHelp {
		t.Errorf("filter value=%q focused=%v focus=%v fullHelp=%v, want q/a? typed into the open filter",
			m.results.filter.Value(), m.results.filter.Focused(), m.focus, m.fullHelp)
	}
	if len(player.calls) != 0 {
		t.Errorf("player calls = %v, want none", player.calls)
	}
}

// results-filter R4
func TestFilterEnterCommitsAndKeepsFilter(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi")
	m = press(t, m, keyPress("enter"))
	if m.results.filter.Focused() || !atPane(m, focusResults) {
		t.Errorf("after enter: filter focused=%v focus=%v, want results list focused", m.results.filter.Focused(), m.focus)
	}
	if m.results.filter.Value() != "lofi" || strings.Contains(rendered(m), "rock") {
		t.Errorf("after enter: value=%q, want lofi kept and rock hidden", m.results.filter.Value())
	}
}

// results-filter R4
func TestFilterEscClearsFromInputAndList(t *testing.T) {
	m, _ := filterModel()
	m.results.filter.Focus()
	m.results.filter.SetValue("lofi")
	m = press(t, m, keyPress("esc"))
	if m.results.filter.Focused() || m.results.filter.Value() != "" || !strings.Contains(rendered(m), "rock") {
		t.Errorf("esc in input: focused=%v value=%q, want closed, cleared, rock shown", m.results.filter.Focused(), m.results.filter.Value())
	}

	m.results.filter.SetValue("lofi")
	m = press(t, m, keyPress("esc"))
	if m.results.filter.Value() != "" || !strings.Contains(rendered(m), "rock") || !atPane(m, focusResults) {
		t.Errorf("esc in list: value=%q focus=%v, want cleared, rock shown, results focused", m.results.filter.Value(), m.focus)
	}
}

// results-filter R5
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

// results-filter R5
func TestFilteredNavigationBounds(t *testing.T) {
	m, _ := filterModel()
	m.results.filter.SetValue("lofi")
	m = press(t, m, keyPress("G"))
	if got := selectedTitle(m); got != "lofi beats" || m.results.cur != 1 {
		t.Errorf("G: selected=%q cur=%d, want lofi beats at row 1", got, m.results.cur)
	}
	m = press(t, m, keyPress("j"))
	if m.results.cur != 1 {
		t.Errorf("j past end: cur=%d, want 1", m.results.cur)
	}
	m = press(t, m, keyPress("g"))
	if got := selectedTitle(m); got != "lofi" {
		t.Errorf("g: selected=%q, want lofi", got)
	}
}

func resultsTitleLine(t *testing.T, m Model) string {
	t.Helper()
	for line := range strings.SplitSeq(rendered(m), "\n") {
		if strings.Contains(line, "─ "+resultsTitle) {
			return line
		}
	}
	t.Fatalf("no Results title line in %q", rendered(m))
	return ""
}

// results-filter R3
func TestFilterTitleCountsMatchedOverTotal(t *testing.T) {
	for _, tt := range []struct {
		query, count string
		noMatches    bool
	}{
		{query: "", count: ""},
		{query: "lofi", count: "2/3"},
		{query: "jazz", count: "0/3", noMatches: true},
	} {
		t.Run(cmp.Or(tt.query, "no filter"), func(t *testing.T) {
			m, _ := filterModel()
			if tt.query != "" {
				m = typeText(t, press(t, m, keyPress("f")), tt.query)
			}
			got := resultsTitleLine(t, m)
			if tt.count == "" && strings.Contains(got, "/3") || !strings.Contains(got, tt.count) {
				t.Errorf("title = %q, want count %q", got, tt.count)
			}
			if got := strings.Contains(rendered(m), "no matches"); got != tt.noMatches {
				t.Errorf("rows say no matches = %v, want %v", got, tt.noMatches)
			}
		})
	}
}

// results-filter R3
func TestFilterHighlightsMatchedSubstrings(t *testing.T) {
	m, _ := filterModel()
	m.results.tracks[2].Channel = "Lofi Girl"
	if strings.Contains(m.render(), matchStyle.Render("lofi")) {
		t.Error("render highlights lofi without a filter")
	}
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

// results-filter R3
func TestFilterHighlightSurvivesTruncation(t *testing.T) {
	m, _ := filterModel()
	m = resize(m, 60)
	m.results.tracks[2].Title = "lofi " + strings.Repeat("x", 100) + " tail"
	m = press(t, m, keyPress("f"))
	m = typeText(t, m, "lofi tail")
	leftW := m.paneRect(paneResults).Dx()
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
	if row := ansi.Strip(strings.Split(pane, "\n")[2]); !strings.HasPrefix(row, "│▌lofi xxx") || strings.Contains(row, "tail") {
		t.Errorf("row = %q, want lofi truncated before tail", row)
	}
}

// results-filter R6: new results clear the filter; a filter being typed stays
// open for the next keystroke, a committed one closes.
func TestNewResultsClearFilter(t *testing.T) {
	for _, typing := range []bool{true, false} {
		t.Run(fmt.Sprintf("typing=%v", typing), func(t *testing.T) {
			m, player := filterModel()
			m.results.filter.SetValue("lofi")
			if typing {
				m.results.filter.Focus()
			}
			m.searchRequest = m.nextRequest()
			got, _ := m.update(searchDoneMsg{requestID: m.searchRequest, query: "new", tracks: []youtube.Track{
				{Title: "jazz", URL: "j"}, {Title: "blues", URL: "k"},
			}})
			m = got.(Model)
			if m.results.filter.Value() != "" || m.results.filter.Focused() != typing || m.results.filterOpen() != typing {
				t.Errorf("filter value=%q focused=%v open=%v, want cleared with focused=open=%v",
					m.results.filter.Value(), m.results.filter.Focused(), m.results.filterOpen(), typing)
			}
			if rows := rendered(m); !strings.Contains(rows, "jazz") || !strings.Contains(rows, "blues") {
				t.Errorf("rows = %q, want all new results shown", rows)
			}
			if !typing {
				return
			}
			got, cmd := m.update(keyPress("q"))
			m = got.(Model)
			if cmd != nil || m.results.filter.Value() != "q" || !atPane(m, focusResults) || len(player.calls) != 0 {
				t.Errorf("q after new results: cmd=%v filter=%q focus=%v calls=%v, want it typed into the filter",
					cmd != nil, m.results.filter.Value(), m.focus, player.calls)
			}
		})
	}
}

func TestFilterPasteReadsClipboardIntoFilter(t *testing.T) {
	m, _ := filterModel()
	m = press(t, m, keyPress("f"))
	got, cmd := m.update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	m = got.(Model)
	if !isClipboardRead(cmd) {
		t.Fatal("ctrl+v in filter: cmd is not the clipboard read")
	}
	if !m.results.filter.Focused() || !atPane(m, focusResults) {
		t.Fatalf("ctrl+v left the filter: focused=%v focus=%v", m.results.filter.Focused(), m.focus)
	}

	// The clipboard result type is unexported; build one carrying known text.
	msg := reflect.New(reflect.TypeOf(textinput.Paste())).Elem()
	if msg.Kind() != reflect.String {
		t.Skipf("clipboard unavailable: %v", msg.Type())
	}
	msg.SetString("lofi")
	got, _ = m.update(msg.Interface())
	m = got.(Model)
	if m.results.filter.Value() != "lofi" || m.input.Value() != "" {
		t.Errorf("clipboard paste: filter=%q search=%q, want lofi in the filter only", m.results.filter.Value(), m.input.Value())
	}
}

func TestFilterWidthIgnoresTabAtResize(t *testing.T) {
	m, _ := filterModel()
	want := resize(m, 100).results.filter.Width()
	m.focus = focusPlaylists
	m = resize(m, 100)
	m.focus = focusResults
	m = press(t, m, keyPress("f"))
	if got := m.results.filter.Width(); got != want {
		t.Errorf("filter width after resizing on Playlists = %d, want %d as when resized on Results", got, want)
	}
}

// results-filter R4
func TestFilterClearKeepsSelectedTrack(t *testing.T) {
	for _, commit := range []bool{false, true} {
		m, _ := filterModel()
		m = press(t, m, keyPress("f"))
		m = typeText(t, m, "lofi")
		m = press(t, m, keyPress("down"))
		if commit {
			m = press(t, m, keyPress("enter"))
		}
		m = press(t, m, keyPress("esc"))
		if m.results.cur != 2 || selectedTitle(m) != "lofi beats" {
			t.Errorf("commit=%v: after esc cur=%d selected=%q, want lofi beats at 2", commit, m.results.cur, selectedTitle(m))
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
		{"hangul fillers", "\u3164\u3164abc\u3164", nil, 5, 3},
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

// results-filter R7
func TestResultsHelpListsFilterAndClearOnlyWhenFiltered(t *testing.T) {
	m, _ := filterModel()
	got := footerHelp(resize(m, 500))
	if !strings.Contains(got, "f filter") {
		t.Errorf("results help %q is missing f filter", got)
	}
	if strings.Contains(got, "esc clear filter") {
		t.Errorf("results help %q offers esc clear filter with no filter applied", got)
	}

	m.results.filter.SetValue("lofi")
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

// results-filter R7
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

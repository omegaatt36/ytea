package tui

import (
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Colors are ANSI palette indexes, so the terminal's theme decides the actual
// shades. Only 1-8 are used: some themes (Flexoki) make 9-15 darker, not
// brighter, so they cannot carry emphasis. No style sets a background except
// the brand, since explicit cell backgrounds stay opaque over a translucent
// terminal.
var (
	accent  = lipgloss.Color("5")
	subtle  = lipgloss.Color("8")
	muted   = lipgloss.Color("7")
	playing = lipgloss.Color("2")
	danger  = lipgloss.Color("1")
	match   = lipgloss.Color("3")

	brandStyle    = lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(accent).Padding(0, 1)
	tabStyle      = lipgloss.NewStyle().Foreground(subtle).Padding(0, 1)
	tabActive     = lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 1)
	headStyle     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle      = lipgloss.NewStyle().Foreground(subtle)
	mutedStyle    = lipgloss.NewStyle().Foreground(muted)
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	cursorDim     = mutedStyle
	cursorMark    = lipgloss.NewStyle().Foreground(accent)
	playingStyle  = lipgloss.NewStyle().Foreground(playing)
	errorStyle    = lipgloss.NewStyle().Foreground(danger)
	nowTitleStyle = lipgloss.NewStyle().Bold(true)
	matchStyle    = lipgloss.NewStyle().Bold(true).Foreground(match)

	vizGradient = []string{"4", "6", "2", "3", "5", "1"}
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	// Cell motion reports clicks and the wheel but not bare movement, so an idle
	// pointer costs no renders. Suspend mouse capture while track info is open
	// so the terminal handles text selection and link clicks natively.
	if m.overlay == overlayInfo {
		v.MouseMode = tea.MouseModeNone
	} else {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.Cursor = m.cursor()
	v.WindowTitle = "ytea"
	if e, t, ok := m.current(); ok {
		v.WindowTitle = "ytea — " + displayTitle(e, t)
		// Ghostty draws OSC 9;4 progress in the tab/titlebar, so playback progress
		// stays visible while the terminal is in the background.
		if m.player.duration > 0 && !t.Live {
			state := tea.ProgressBarDefault
			if m.player.paused {
				state = tea.ProgressBarWarning
			}
			v.ProgressBar = tea.NewProgressBar(state, int(100*m.player.timePos/m.player.duration))
		}
	}
	return v
}

// The terminal draws and blinks the cursor itself, so an idle input costs no
// renders.
func (m Model) cursor() *tea.Cursor {
	var (
		c  *tea.Cursor
		at image.Point
	)
	switch {
	case m.overlay == overlayNone && m.focus == focusSearch:
		c, at = m.input.Cursor(), image.Pt(lipgloss.Width(brand()+" "), 0)
	case m.inFilter():
		c, at = m.results.filter.Cursor(), m.paneRect(paneResults).Min.Add(image.Pt(boxInset, 1))
	case m.overlay == overlayName:
		d, _ := m.dialog(m.screen().body)
		c, at = m.nameInput.Cursor(), d.rect.Min.Add(image.Pt(boxInset, 1))
	}
	if c != nil {
		c.X, c.Y = c.X+at.X, c.Y+at.Y
	}
	return c
}

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}
	s := m.screen()
	return strings.Join([]string{m.renderHeader(), m.renderBody(s.body), m.renderPlayer(), m.renderFooter()}, "\n")
}

const (
	headerRows = 1
	footerRows = 1
	playerRows = thumbRows + 2
)

// screen assigns every row once; rendering, hit testing, cursor placement and
// direct thumbnail placement all read it, so they cannot disagree.
type screen struct {
	body, player image.Rectangle
}

func (m Model) screen() screen {
	bodyH := max(3, m.height-headerRows-playerRows-footerRows)
	body := image.Rect(0, headerRows, m.width, headerRows+bodyH)
	return screen{body: body, player: image.Rect(0, body.Max.Y, m.width, body.Max.Y+playerRows)}
}

func (m Model) thumbOrigin() image.Point {
	return m.screen().player.Min.Add(image.Pt(boxInset, 1))
}

// renderBody draws a dialog over the panes it was opened from, so the context
// stays visible around it.
func (m Model) renderBody(r image.Rectangle) string {
	if m.fullHelp {
		return m.renderFullHelp(r.Dy())
	}
	panes := m.renderPanes(r.Dy())
	d, ok := m.dialog(r)
	if !ok {
		return panes
	}
	at := d.rect.Min.Sub(r.Min)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(panes),
		lipgloss.NewLayer(m.renderDialog(d.rect.Dx(), d.rect.Dy())).X(at.X).Y(at.Y).Z(1),
	).Render()
}

type tab struct {
	name  string
	focus focus
}

var tabs = []tab{
	{"Results", focusResults},
	{"Queue", focusQueue},
	{"Playlists", focusPlaylists},
	{"History", focusHistory},
}

const (
	tabGap = " "
	// minSearchWidth is the narrowest search box the tabs may squeeze it to;
	// below it the tabs give way, since the Tab key reaches them anyway.
	minSearchWidth = 16
	spinnerCells   = 2
)

func brand() string {
	return brandStyle.Render("ytea")
}

func (m Model) renderHeader() string {
	left := brand() + " " + m.input.View()
	if m.searching {
		left += " " + m.spinner.View()
	}
	tabs := m.renderTabs()
	return fit(left, m.width-lipgloss.Width(tabs)) + tabs
}

// searchWidth sizes the search input so the header never wraps.
func (m Model) searchWidth() int {
	return max(1, m.tabsX()-lipgloss.Width(brand()+" "+m.input.Prompt)-spinnerCells-1)
}

func (m Model) tabsX() int {
	w := lipgloss.Width(strings.Join(m.tabLabels(), tabGap))
	if m.width-w-lipgloss.Width(brand()+" "+m.input.Prompt)-spinnerCells-1 < minSearchWidth {
		return m.width
	}
	return m.width - w
}

func (m Model) renderTabs() string {
	if m.tabsX() >= m.width {
		return ""
	}
	return strings.Join(m.tabLabels(), tabGap)
}

func (m Model) activeTab() focus {
	if m.overlay == overlayPicker {
		return focusPlaylists
	}
	switch m.focus {
	case focusQueue:
		return focusQueue
	case focusPlaylists, focusPlaylistTracks:
		return focusPlaylists
	case focusHistory:
		return focusHistory
	}
	return focusResults
}

func (m Model) tabLabels() []string {
	active := m.activeTab()
	labels := make([]string, len(tabs))
	for i, t := range tabs {
		labels[i] = tabStyle.Render(t.name)
		if t.focus == active {
			labels[i] = tabActive.Render(t.name)
		}
	}
	return labels
}

func (m Model) tabAt(x int) (focus, bool) {
	x -= m.tabsX()
	if x < 0 {
		return 0, false
	}
	for i, label := range m.tabLabels() {
		w := lipgloss.Width(label)
		if x < w {
			return tabs[i].focus, true
		}
		x -= w + len(tabGap)
	}
	return 0, false
}

// The status lives on the player's bottom edge, so the footer is keys only.
func (m Model) renderFooter() string {
	if m.fullHelp {
		return ansi.Truncate(m.help.ShortHelpView([]key.Binding{m.keys.closeHelp}), m.width, "…")
	}
	// "? more" is rendered apart from the context entries, since truncation
	// drops trailing entries and it must stay visible.
	more := m.help.ShortHelpView([]key.Binding{m.keys.global.Help})
	sep := m.help.Styles.ShortSeparator.Render(m.help.ShortSeparator)
	if ctx := m.contextHelp(m.help.Width() - lipgloss.Width(sep+more)); ctx != "" {
		return ctx + sep + more
	}
	ellipsis := m.help.Styles.Ellipsis.Inline(true).Render(m.help.Ellipsis)
	for _, separator := range []string{sep, " ", ""} {
		if candidate := ellipsis + separator + more; lipgloss.Width(candidate) <= m.width {
			return candidate
		}
	}
	return more
}

// bubbles help (v2.2.1) appends an overflowing entry when its " …" tail would
// not fit either, so shrink the width until the output actually fits.
func (m Model) contextHelp(width int) string {
	_, keys := m.contextKeys()
	h := m.help
	if h.Width() == 0 {
		return h.View(keys)
	}
	for w := width; w > 0; w-- {
		h.SetWidth(w)
		if s := h.View(keys); lipgloss.Width(s) <= width {
			return s
		}
	}
	return ""
}

func (m Model) renderFullHelp(height int) string {
	ctxTitle, ctx := m.contextKeys()
	fullHelpGlobal := m.keys.global
	// q closes help here, while the normal-mode binding remains q quit.
	fullHelpGlobal.Quit = m.keys.closeHelp
	groups := []struct {
		title string
		keys  help.KeyMap
	}{
		{"Playback", m.keys.playback},
		{"Navigation and global", fullHelpGlobal},
		{ctxTitle, ctx},
	}
	innerW := m.width - 2*boxInset
	columns := make([]string, 0, 2*len(groups)-1)
	for i, g := range groups {
		view := headStyle.Render(g.title) + "\n" + m.help.FullHelpView([][]key.Binding{slices.Concat(g.keys.FullHelp()...)})
		if i > 0 {
			columns = append(columns, "    ")
		}
		columns = append(columns, view)
	}
	if horizontal := lipgloss.JoinHorizontal(lipgloss.Top, columns...); lipgloss.Width(horizontal) <= innerW {
		return box("Keys", "", padRows(strings.Split(horizontal, "\n")), true, m.width, height)
	}

	lines := make([]string, 0)
	for _, g := range groups {
		line := headStyle.Render(g.title)
		hasEntry := false
		for _, binding := range slices.Concat(g.keys.FullHelp()...) {
			if !binding.Enabled() {
				continue
			}
			details := binding.Help()
			entry := m.help.Styles.FullKey.Inline(true).Render(details.Key) + " " + m.help.Styles.FullDesc.Inline(true).Render(details.Desc)
			separator := "  "
			if !hasEntry {
				separator = " "
			}
			if lipgloss.Width(line+separator+entry) <= innerW {
				line += separator + entry
			} else {
				lines = append(lines, line)
				line = entry
			}
			hasEntry = true
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return box("Keys", "", padRows(lines), true, m.width, height)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

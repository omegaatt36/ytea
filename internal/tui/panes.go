package tui

import (
	"fmt"
	"image"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/youtube"
)

// boxInset is a box's border plus the one-cell margin inside it.
const boxInset = 2

// box frames rows in a rounded border whose top edge carries the title and
// whose bottom edge carries the note, so neither costs a content row.
func box(title, note string, rows []string, focused bool, width, height int) string {
	border, label := dimStyle, dimStyle.Bold(true)
	if focused {
		border, label = lipgloss.NewStyle().Foreground(accent), headStyle
	}
	if title != "" {
		title = label.Render(title)
	}
	innerW := max(0, width-2)
	lines := make([]string, 0, height)
	lines = append(lines, edge(border, "╭", "╮", title, innerW))
	for i := range max(0, height-2) {
		row := ""
		if i < len(rows) {
			row = rows[i]
		}
		lines = append(lines, border.Render("│")+fit(row, innerW)+border.Render("│"))
	}
	lines = append(lines, edge(border, "╰", "╯", note, innerW))
	return strings.Join(lines, "\n")
}

func edge(border lipgloss.Style, left, right, label string, innerW int) string {
	if lipgloss.Width(label) == 0 || innerW < 6 {
		return border.Render(left + strings.Repeat("─", innerW) + right)
	}
	label = ansi.Truncate(label, innerW-4, "…")
	return border.Render(left+"─ ") + label + border.Render(" "+strings.Repeat("─", innerW-3-lipgloss.Width(label))+right)
}

// fit cuts or pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func padRows(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = " " + l
	}
	return out
}

type listPane int

const (
	paneNone listPane = iota
	paneResults
	paneQueue
	panePlaylists
	panePlaylistTracks
	paneDevices
)

type paneBox struct {
	pane listPane
	rect image.Rectangle
}

// panes lays out the active tab's lists in r.
func (m Model) panes(r image.Rectangle) []paneBox {
	split := func(left, right listPane, leftW int) []paneBox {
		mid := r.Min.X + leftW
		return []paneBox{
			{left, image.Rect(r.Min.X, r.Min.Y, mid, r.Max.Y)},
			{right, image.Rect(mid, r.Min.Y, r.Max.X, r.Max.Y)},
		}
	}
	switch m.activeTab() {
	case focusQueue:
		return []paneBox{{paneQueue, r}}
	case focusPlaylists:
		return split(panePlaylists, panePlaylistTracks, r.Dx()*2/5)
	}
	if w := resultsWidth(r.Dx()); w < r.Dx() {
		return split(paneResults, paneQueue, w)
	}
	return []paneBox{{paneResults, r}}
}

// Below splitMinWidth the queue beside Results would be too narrow to read;
// the Queue tab still shows it.
const splitMinWidth = 72

func resultsWidth(width int) int {
	if width < splitMinWidth {
		return width
	}
	return width * 3 / 5
}

func (m Model) paneRect(p listPane) image.Rectangle {
	for _, b := range m.panes(m.screen().body) {
		if b.pane == p {
			return b.rect
		}
	}
	return image.Rectangle{}
}

func (m Model) renderPanes(height int) string {
	boxes := m.panes(image.Rect(0, 0, m.width, height))
	views := make([]string, len(boxes))
	for i, b := range boxes {
		views[i] = m.list(b.pane).render(b.rect.Dx(), b.rect.Dy())
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, views...)
}

// paneAt maps a cell to the list under it and the row index there, or -1
// off the rows. An open dialog takes every click meant for the panes below.
func (m Model) paneAt(x, y int) (listPane, int) {
	s := m.screen()
	boxes := m.panes(s.body)
	if d, ok := m.dialog(s.body); ok {
		boxes = []paneBox{d}
	}
	pt := image.Pt(x, y)
	for _, b := range boxes {
		if !pt.In(b.rect) || b.pane == paneNone {
			continue
		}
		l := m.list(b.pane)
		row := y - b.rect.Min.Y - 1 - len(l.head)
		visible := l.visible(b.rect.Dy())
		if row < 0 || row >= visible {
			return b.pane, -1
		}
		if i := scrollStart(l.cursor, visible) + row; i < l.n {
			return b.pane, i
		}
		return b.pane, -1
	}
	return paneNone, -1
}

func (m Model) listCursor(p listPane) (cursor, n int) {
	switch p {
	case paneResults:
		return m.results.cur, len(m.results.rows())
	case paneQueue:
		return m.queueCur, len(m.queue.entries)
	case panePlaylists:
		return m.playlistCur, len(m.playlists)
	case panePlaylistTracks:
		return m.playlistTrackCur, len(m.selectedPlaylistTracks())
	case paneDevices:
		return m.deviceCur, len(m.devices)
	default:
		return 0, 0
	}
}

// list is one scrolling list box. head rows stay above the scrolled rows.
type list struct {
	title, empty string
	head         []string
	n, cursor    int
	focused      bool
	// row renders item i in w cells; selected rows draw on the cursor bar.
	row func(i, w int, selected bool) string
}

func (l list) visible(height int) int {
	return max(0, height-2-len(l.head))
}

func (l list) render(width, height int) string {
	visible := l.visible(height)
	innerW := width - 2
	rows := slices.Clone(l.head)
	if l.n == 0 {
		rows = append(rows, " "+dimStyle.Render(l.empty))
	}
	start := scrollStart(l.cursor, visible)
	for i := start; i < min(l.n, start+visible); i++ {
		selected := i == l.cursor
		rows = append(rows, listRow(l.row(i, innerW-2, selected && l.focused), innerW, selected, l.focused))
	}
	var note string
	if l.n > visible && visible > 0 {
		note = dimStyle.Render(fmt.Sprintf("%d/%d", l.cursor+1, l.n))
	}
	return box(l.title, note, rows, l.focused, width, height)
}

// listRow keeps the cursor visible in an unfocused list as a thin mark, so
// the selection survives moving focus to the neighbouring pane.
func listRow(content string, w int, selected, focused bool) string {
	inner := max(0, w-2)
	switch {
	case selected && focused:
		content = ansi.Truncate(content, inner, "…")
		return cursorMark.Render("▌") + content + cursorStyle.Render(strings.Repeat(" ", inner-lipgloss.Width(content)+1))
	case selected:
		return dimStyle.Render("▏") + fit(content, inner) + " "
	}
	return " " + fit(content, inner) + " "
}

func scrollStart(cursor, visible int) int {
	if cursor >= visible {
		return cursor - visible + 1
	}
	return 0
}

// dialogOpen means the panes are only context behind a dialog.
func (m Model) dialogOpen() bool {
	return m.overlay == overlayDevices || m.overlay == overlayInfo || m.overlay == overlayName
}

func (m Model) list(p listPane) list {
	cursor, n := m.listCursor(p)
	l := list{n: n, cursor: cursor}
	styles := func(selected bool) (text, dim lipgloss.Style) {
		if selected {
			return cursorStyle, cursorDim
		}
		return lipgloss.NewStyle(), dimStyle
	}
	switch p {
	case paneResults:
		rows := m.results.rows()
		l.title, l.empty = resultsTitle, "press "+m.keys.global.Search.Help().Key+" to search"
		if len(m.results.tracks) > 0 {
			l.title = fmt.Sprintf("%s (%d)", resultsTitle, len(m.results.tracks))
		}
		if m.results.filterOpen() {
			l.title = fmt.Sprintf("%s (%d/%d)", resultsTitle, len(rows), len(m.results.tracks))
			l.head = []string{" " + m.results.filter.View()}
			if len(m.results.tracks) > 0 {
				l.empty = "no matches"
			}
		}
		l.focused = m.focus == focusResults && !m.dialogOpen()
		lenW := lengthWidth(m.results.tracks)
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			return trackRow(m.results.tracks[rows[i].index], rows[i], w, lenW, text, dim)
		}
	case paneQueue:
		detailed := m.activeTab() == focusQueue
		l.title = fmt.Sprintf("Queue (%d)", len(m.queue.entries))
		l.empty = "empty — press " + m.keys.results.Enqueue.Help().Key + " on a result"
		l.focused = m.focus == focusQueue && !m.dialogOpen()
		tracks := m.queueTracks()
		lenW := lengthWidth(tracks)
		l.row = func(i, w int, selected bool) string {
			return m.queueLine(i, tracks[i], w, lenW, detailed, selected)
		}
	case panePlaylists:
		l.title = fmt.Sprintf("Playlists (%d)", len(m.playlists))
		if m.overlay == overlayPicker {
			l.title = "Save to playlist"
		}
		l.empty = "press " + m.keys.playlists.Create.Help().Key + " to create"
		l.focused = (m.overlay == overlayPicker || m.focus == focusPlaylists) && !m.dialogOpen()
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			pl := m.playlists[i]
			count := dim.Render(" " + strconv.Itoa(len(pl.Tracks)))
			return pad(text.Render(ansi.Truncate(stripControl(pl.Name), w-lipgloss.Width(count), "…")), w-lipgloss.Width(count), text) + count
		}
	case panePlaylistTracks:
		tracks := m.selectedPlaylistTracks()
		l.title, l.empty = "Tracks", "empty playlist"
		if m.playlistCur < len(m.playlists) {
			l.title = fmt.Sprintf("%s (%d)", m.playlists[m.playlistCur].Name, len(tracks))
		}
		l.focused = m.focus == focusPlaylistTracks && m.overlay == overlayNone
		lenW := lengthWidth(tracks)
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			return trackRow(tracks[i], filterMatch{}, w, lenW, text, dim)
		}
	case paneDevices:
		l.title, l.empty, l.focused = "Output device", "no output devices", true
		l.row = func(i, w int, selected bool) string {
			text, _ := styles(selected)
			d := m.devices[i]
			mark := "  "
			if m.isCurrentDevice(d) {
				text, mark = text.Foreground(playing), "● "
			}
			return text.Render(ansi.Truncate(mark+d.Label(), w, "…"))
		}
	case paneNone:
	}
	return l
}

const resultsTitle = "Results"

// trackRow sets a track out in columns: title, then channel when there is
// room for it, then the length flush right in lenW cells.
func trackRow(t youtube.Track, match filterMatch, w, lenW int, text, dim lipgloss.Style) string {
	right := dim.Render(fmt.Sprintf("  %*s", lenW, trackLength(t)))
	if w >= 48 {
		chanW := min(24, w/4)
		right = text.Render("  ") + pad(highlight(t.Channel, match.channel, chanW, dim), chanW, dim) + right
	}
	titleW := max(1, w-lipgloss.Width(right))
	return pad(highlight(t.Title, match.title, titleW, text), titleW, text) + right
}

func trackLength(t youtube.Track) string {
	if t.Live {
		return "LIVE"
	}
	return formatDuration(t.Duration)
}

// lengthWidth sizes a list's length column to its longest entry, so hour-long
// uploads cannot push their row's columns out of line.
func lengthWidth(tracks []youtube.Track) int {
	w := 0
	for _, t := range tracks {
		w = max(w, len(trackLength(t)))
	}
	return w
}

// pad fills s out to w cells in style, so a cursor bar stays unbroken.
func pad(s string, w int, style lipgloss.Style) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + style.Render(strings.Repeat(" ", gap))
	}
	return s
}

// queueTracks is the queue as tracks, titled the way the queue shows them.
func (m Model) queueTracks() []youtube.Track {
	tracks := make([]youtube.Track, len(m.queue.entries))
	for i, e := range m.queue.entries {
		t := m.tracks[e.Filename]
		t.Title = displayTitle(e, t)
		tracks[i] = t
	}
	return tracks
}

func (m Model) queueLine(i int, t youtube.Track, w, lenW int, detailed, selected bool) string {
	text, dim, marker := lipgloss.NewStyle(), dimStyle, "  "
	if selected {
		text, dim = cursorStyle, cursorDim
	}
	if i == m.queue.pos {
		text, marker = text.Foreground(playing), "▶ "
	}
	prefix := text.Render(marker)
	if detailed {
		prefix += dim.Render(fmt.Sprintf("%*d ", len(strconv.Itoa(len(m.queue.entries))), i+1))
	}
	return prefix + trackRow(t, filterMatch{}, w-lipgloss.Width(prefix), lenW, text, dim)
}

// highlight cuts s to width on a grapheme boundary of s itself, so the byte
// spans stay valid, and styles each segment separately. Control bytes in
// remote titles render as nothing, so they cannot move the cursor or restyle.
func highlight(s string, spans []span, width int, base lipgloss.Style) string {
	keep, tail := cutAt(s, width), ""
	if keep < len(s) {
		tail = "…"
	}
	var b strings.Builder
	styled := func(style lipgloss.Style, text string) {
		if text = stripControl(text); text != "" {
			b.WriteString(style.Render(text))
		}
	}
	at := 0
	for _, sp := range mergeSpans(spans) {
		start, end := max(sp.start, at), min(sp.end, keep)
		if start >= end {
			continue
		}
		styled(base, s[at:start])
		styled(matchStyle.Inherit(base), s[start:end])
		at = end
	}
	styled(base, s[at:keep]+tail)
	return b.String()
}

// cutAt is the byte length of the longest prefix of s that fits width cells,
// leaving a cell for "…" when s does not fit whole.
func cutAt(s string, width int) int {
	if ansi.StringWidth(stripControl(s)) <= width {
		return len(s)
	}
	cut, used := 0, 0
	for cut < len(s) {
		c, _ := ansi.FirstGraphemeCluster(s[cut:], ansi.GraphemeWidth)
		w := ansi.StringWidth(stripControl(c))
		if used+w > width-1 {
			break
		}
		cut, used = cut+len(c), used+w
	}
	return cut
}

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func mergeSpans(spans []span) []span {
	sorted := slices.SortedFunc(slices.Values(spans), func(a, b span) int { return a.start - b.start })
	var out []span
	for _, sp := range sorted {
		if last := len(out) - 1; last >= 0 && sp.start <= out[last].end {
			out[last].end = max(out[last].end, sp.end)
			continue
		}
		out = append(out, sp)
	}
	return out
}

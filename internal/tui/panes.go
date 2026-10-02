package tui

import (
	"fmt"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

// boxInset is a box's border plus the one-cell margin inside it.
const boxInset = 2

// box frames rows in a rounded border whose top edge carries the title and
// whose bottom edge carries the note, so neither costs a content row.
func box(title, note string, rows []string, focused bool, width, height int) string {
	border, label := dimStyle, dimStyle.Bold(true)
	if focused {
		label = headStyle
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
	paneHistory
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
	case focusHistory:
		return []paneBox{{paneHistory, r}}
	case focusSearch, focusResults, focusPlaylistTracks:
	}
	if w := resultsWidth(r.Dx()); w < r.Dx() {
		if len(m.core.Search.Tracks) == 0 {
			w = max(30, r.Dx()*2/5)
		}
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
		return m.results.cur, len(m.resultRows())
	case paneQueue:
		return m.queueCur, len(m.core.Queue.Entries)
	case panePlaylists:
		return m.playlistCur, m.playlistCount()
	case panePlaylistTracks:
		return m.playlistTrackCur, len(m.selectedPlaylistTracks())
	case paneHistory:
		return m.historyCur, len(m.core.History.Entries)
	case paneDevices:
		return m.deviceCur, len(m.core.Devices)
	case paneNone:
	}
	return 0, 0
}

// list is one scrolling list box. head rows stay above the scrolled rows.
type list struct {
	title, empty, emptyTitle string
	head                     []string
	n, cursor                int
	focused                  bool
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
		if l.emptyTitle != "" && visible >= 6 {
			rows = append(rows, make([]string, max(0, (visible-2)/2))...)
			rows = append(rows,
				centerLine(headStyle.Render(l.emptyTitle), innerW),
				centerLine(dimStyle.Render(l.empty), innerW),
			)
		} else {
			rows = append(rows, " "+dimStyle.Render(l.empty))
		}
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

func centerLine(s string, width int) string {
	return strings.Repeat(" ", max(0, (width-lipgloss.Width(s))/2)) + s
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
		rows := m.resultRows()
		l.title, l.empty = resultsTitle, "press "+m.keys.global.Search.Help().Key+" to search"
		if len(m.core.Search.Tracks) == 0 {
			l.emptyTitle = "Search music"
		}
		if len(m.core.Search.Tracks) > 0 {
			more := ""
			if m.core.Search.CanLoadMore() {
				more = "+"
			}
			l.title = fmt.Sprintf("%s (%d%s)", resultsTitle, len(m.core.Search.Tracks), more)
		}
		if m.results.filterOpen() {
			l.title = fmt.Sprintf("%s (%d/%d)", resultsTitle, len(rows), len(m.core.Search.Tracks))
			l.head = []string{" " + m.results.filter.View()}
			if len(m.core.Search.Tracks) > 0 {
				l.empty = "no matches"
			}
		}
		l.focused = m.focus == focusResults && !m.dialogOpen()
		lenW := lengthWidth(m.core.Search.Tracks)
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			return trackRow(m.core.Search.Tracks[rows[i].Index], rows[i], w, lenW, text, dim)
		}
	case paneQueue:
		detailed := m.activeTab() == focusQueue
		l.title = fmt.Sprintf("Queue (%d)", len(m.core.Queue.Entries))
		l.empty = "empty — press " + m.keys.results.Enqueue.Help().Key + " on a result"
		l.focused = m.focus == focusQueue && !m.dialogOpen()
		tracks := m.queueTracks()
		lenW := lengthWidth(tracks)
		l.row = func(i, w int, selected bool) string {
			return m.queueLine(i, tracks[i], w, lenW, detailed, selected)
		}
	case panePlaylists:
		l.title = fmt.Sprintf("Playlists (%d)", m.playlistCount())
		if m.overlay == overlayPicker {
			l.title = "Save to playlist"
		}
		l.empty = "press " + m.keys.playlists.Create.Help().Key + " to create"
		l.focused = (m.overlay == overlayPicker || m.focus == focusPlaylists) && !m.dialogOpen()
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			if i >= len(m.core.Playlists.List) {
				return text.Render(ansi.Truncate(service.Sanitize(m.accountPlaylistNames()[i-len(m.core.Playlists.List)]), w, "…"))
			}
			pl := m.core.Playlists.List[i]
			count := dim.Render(" " + strconv.Itoa(len(pl.Tracks)))
			return pad(text.Render(ansi.Truncate(service.Sanitize(pl.Name), w-lipgloss.Width(count), "…")), w-lipgloss.Width(count), text) + count
		}
	case panePlaylistTracks:
		tracks := m.selectedPlaylistTracks()
		l.title, l.empty = "Tracks", "empty playlist"
		if m.playlistCur < len(m.core.Playlists.List) {
			l.title = fmt.Sprintf("%s (%d)", m.core.Playlists.List[m.playlistCur].Name, len(tracks))
		}
		if m.accountSelected() && m.core.Account.Err != nil {
			l.title, l.empty = "YouTube", service.Sanitize(m.core.Account.Err.Error())
		}
		if pl, ok := m.selectedAccountPlaylist(); ok {
			l.title = "YouTube — " + service.Sanitize(pl.Title)
			switch {
			case m.core.Account.TrackLoading[pl.ID]:
				l.empty = "loading…"
			case m.core.Account.TrackErrors[pl.ID] != nil:
				l.empty = service.Sanitize(m.core.Account.TrackErrors[pl.ID].Error())
			case m.core.Account.QueueErrors[pl.ID] != nil:
				l.empty = service.Sanitize(m.core.Account.QueueErrors[pl.ID].Error())
			}
		}
		l.focused = m.focus == focusPlaylistTracks && m.overlay == overlayNone
		lenW := lengthWidth(tracks)
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			return trackRow(tracks[i], service.Match{}, w, lenW, text, dim)
		}
	case paneHistory:
		l.title, l.empty = fmt.Sprintf("History (%d)", len(m.core.History.Entries)), "nothing played yet"
		if !m.core.History.Enabled() {
			l.empty = "history is unavailable"
		}
		l.focused = m.focus == focusHistory && !m.dialogOpen()
		tracks := make([]domain.Track, len(m.core.History.Entries))
		for i, e := range m.core.History.Entries {
			tracks[i] = e.Track
		}
		lenW := lengthWidth(tracks)
		now := time.Now()
		l.row = func(i, w int, selected bool) string {
			text, dim := styles(selected)
			ago := dim.Render(fmt.Sprintf("%-*s", agoWidth, playedAgo(m.core.History.Entries[i].PlayedAt, now)))
			return ago + trackRow(tracks[i], service.Match{}, w-agoWidth, lenW, text, dim)
		}
	case paneDevices:
		l.title, l.empty, l.focused = "Output device", "no output devices", true
		l.row = func(i, w int, selected bool) string {
			text, _ := styles(selected)
			d := m.core.Devices[i]
			mark := "  "
			if m.core.IsCurrentDevice(d) {
				text, mark = text.Foreground(playing), "● "
			}
			return text.Render(ansi.Truncate(mark+d.Label(), w, "…"))
		}
	case paneNone:
	}
	return l
}

const resultsTitle = "Results"

// agoWidth fits playedAgo's longest label, a date like "Sep 28", and a gap.
const agoWidth = 7

// trackRow sets a track out in columns: title, then channel when there is
// room for it, then the length flush right in lenW cells.
func trackRow(t domain.Track, match service.Match, w, lenW int, text, dim lipgloss.Style) string {
	right := dim.Render(fmt.Sprintf("  %*s", lenW, trackLength(t)))
	if w >= 48 {
		chanW := min(24, w/4)
		right = text.Render("  ") + pad(highlight(t.Channel, match.Channel, chanW, dim), chanW, dim) + right
	}
	titleW := max(1, w-lipgloss.Width(right))
	return pad(highlight(t.Title, match.Title, titleW, text), titleW, text) + right
}

func trackLength(t domain.Track) string {
	if t.Live {
		return "LIVE"
	}
	return service.FormatDuration(t.Duration)
}

// lengthWidth sizes a list's length column to its longest entry, so hour-long
// uploads cannot push their row's columns out of line.
func lengthWidth(tracks []domain.Track) int {
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
func (m Model) queueTracks() []domain.Track {
	tracks := make([]domain.Track, len(m.core.Queue.Entries))
	for i, e := range m.core.Queue.Entries {
		t := m.core.Tracks[e.Filename]
		t.Title = service.DisplayTitle(e, t)
		tracks[i] = t
	}
	return tracks
}

func (m Model) queueLine(i int, t domain.Track, w, lenW int, detailed, selected bool) string {
	text, dim, marker := lipgloss.NewStyle(), dimStyle, "  "
	if selected {
		text, dim = cursorStyle, cursorDim
	}
	if i == m.core.Queue.Pos {
		text, marker = text.Foreground(playing), "▶ "
	}
	prefix := text.Render(marker)
	if detailed {
		prefix += dim.Render(fmt.Sprintf("%*d ", len(strconv.Itoa(len(m.core.Queue.Entries))), i+1))
	}
	return prefix + trackRow(t, service.Match{}, w-lipgloss.Width(prefix), lenW, text, dim)
}

// highlight cuts s to width on a grapheme boundary of s itself, so the byte
// spans stay valid, and styles each segment separately, sanitized.
func highlight(s string, spans []service.Span, width int, base lipgloss.Style) string {
	keep, tail := cutAt(s, width), ""
	if keep < len(s) {
		tail = "…"
	}
	var b strings.Builder
	styled := func(style lipgloss.Style, text string) {
		if text = service.Sanitize(text); text != "" {
			b.WriteString(style.Render(text))
		}
	}
	at := 0
	for _, sp := range mergeSpans(spans) {
		start, end := max(sp.Start, at), min(sp.End, keep)
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
	if ansi.StringWidth(service.Sanitize(s)) <= width {
		return len(s)
	}
	cut, used := 0, 0
	for cut < len(s) {
		c, _ := ansi.FirstGraphemeCluster(s[cut:], ansi.GraphemeWidth)
		w := ansi.StringWidth(service.Sanitize(c))
		if used+w > width-1 {
			break
		}
		cut, used = cut+len(c), used+w
	}
	return cut
}

func mergeSpans(spans []service.Span) []service.Span {
	sorted := slices.SortedFunc(slices.Values(spans), func(a, b service.Span) int { return a.Start - b.Start })
	var out []service.Span
	for _, sp := range sorted {
		if last := len(out) - 1; last >= 0 && sp.Start <= out[last].End {
			out[last].End = max(out[last].End, sp.End)
			continue
		}
		out = append(out, sp)
	}
	return out
}

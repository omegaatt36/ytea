package tui

import (
	"fmt"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

var (
	accent  = lipgloss.Color("#b48ead")
	subtle  = lipgloss.Color("#6c7086")
	playing = lipgloss.Color("#a6e3a1")
	danger  = lipgloss.Color("#f38ba8")

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(accent).Padding(0, 1)
	paneStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle)
	paneFocus     = paneStyle.BorderForeground(accent)
	headStyle     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle      = lipgloss.NewStyle().Foreground(subtle)
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color("#313244"))
	playingStyle  = lipgloss.NewStyle().Foreground(playing)
	errorStyle    = lipgloss.NewStyle().Foreground(danger)
	nowTitleStyle = lipgloss.NewStyle().Bold(true)
	matchStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f9e2af"))

	vizGradient = []string{"#89b4fa", "#94e2d5", "#a6e3a1", "#f9e2af", "#fab387", "#f38ba8"}
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	// Cell motion reports clicks and the wheel but not bare movement, so an idle
	// pointer costs no renders.
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = m.cursor()
	v.WindowTitle = "ytea"
	if e, t, ok := m.current(); ok {
		v.WindowTitle = "ytea — " + displayTitle(e, t)
		// Ghostty draws OSC 9;4 progress in the tab/titlebar, so playback progress
		// stays visible while the terminal is in the background.
		if m.duration > 0 && !t.Live {
			state := tea.ProgressBarDefault
			if m.paused {
				state = tea.ProgressBarWarning
			}
			v.ProgressBar = tea.NewProgressBar(state, int(100*m.timePos/m.duration))
		}
	}
	return v
}

// The terminal draws and blinks the cursor itself, so an idle input costs no
// renders. Positions must follow the layout in render.
func (m Model) cursor() *tea.Cursor {
	switch {
	case m.overlay == overlayNone && m.focus == focusSearch:
		c := m.input.Cursor()
		if c != nil {
			c.X += lipgloss.Width(renderTitle())
		}
		return c
	case m.inFilter():
		c := m.filterInput.Cursor()
		if c != nil {
			c.X += paneFocus.GetBorderLeftSize() + lipgloss.Width(m.resultsHeading())
			c.Y += lipgloss.Height(m.renderHeader()) + paneFocus.GetBorderTopSize()
		}
		return c
	case m.overlay == overlayName:
		c := m.nameInput.Cursor()
		if c != nil {
			c.X += paneFocus.GetBorderLeftSize()
			c.Y += lipgloss.Height(m.renderHeader()) + paneFocus.GetBorderTopSize() + 1
		}
		return c
	default:
		return nil
	}
}

func renderTitle() string {
	return titleStyle.Render("ytea")
}

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}

	header, now, footer, bodyHeight := m.layout()

	var viz string
	if m.showViz && !m.fullHelp {
		viz = m.renderSpectrum(m.width, vizRows)
	}

	var body string
	switch {
	case m.fullHelp:
		body = m.renderFullHelp(bodyHeight)
	case m.overlay == overlayDevices:
		body = m.renderDevices(m.width, bodyHeight)
	case m.overlay == overlayInfo:
		body = m.renderInfo(bodyHeight)
	case m.overlay == overlayName:
		body = m.renderPlaylistName(bodyHeight)
	case m.overlay == overlayPicker, m.focus == focusPlaylists, m.focus == focusPlaylistTracks:
		body = m.renderPlaylistPanes(bodyHeight)
	case m.focus == focusQueue:
		body = m.renderQueue(m.width, bodyHeight, true)
	default:
		body = m.renderPanes(bodyHeight)
	}

	parts := []string{header, body, now}
	if viz != "" {
		parts = append(parts, viz)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// thumbOrigin relies on layout too, so the two cannot disagree about where
// the now-playing box sits.
func (m Model) layout() (header, now, footer string, bodyHeight int) {
	header, now, footer = m.renderHeader(), m.renderNowPlaying(), m.renderFooter()
	used := lipgloss.Height(header) + lipgloss.Height(now) + lipgloss.Height(footer)
	if m.showViz && !m.fullHelp {
		used += vizRows
	}
	return header, now, footer, max(3, m.height-used)
}

func (m Model) thumbOrigin() image.Point {
	header, _, _, bodyHeight := m.layout()
	return image.Pt(paneStyle.GetBorderLeftSize(), lipgloss.Height(header)+bodyHeight+paneStyle.GetBorderTopSize())
}

var tabs = []struct {
	name  string
	focus focus
}{
	{"Results", focusResults},
	{"Queue", focusQueue},
	{"Playlists", focusPlaylists},
}

const tabGap = "  "

func (m Model) renderHeader() string {
	title := renderTitle()
	search := m.input.View()
	if m.searching {
		search += " " + m.spinner.View()
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, title, search) + "\n" + strings.Join(m.tabLabels(), tabGap)
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
	}
	return focusResults
}

func (m Model) tabLabels() []string {
	active := m.activeTab()
	labels := make([]string, len(tabs))
	for i, t := range tabs {
		labels[i] = t.name
		if t.focus == active {
			labels[i] = headStyle.Render("[" + t.name + "]")
		}
	}
	return labels
}

func (m Model) tabsRow() int {
	return lipgloss.Height(m.renderHeader()) - 1
}

func (m Model) tabAt(x int) (focus, bool) {
	for i, label := range m.tabLabels() {
		w := lipgloss.Width(label)
		if x < w {
			return tabs[i].focus, x >= 0
		}
		x -= w + len(tabGap)
	}
	return 0, false
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

func (m Model) bodyPanes() (left, right listPane, leftW int) {
	switch {
	case m.overlay == overlayDevices:
		return paneDevices, paneNone, m.width
	case m.overlay == overlayName, m.overlay == overlayInfo:
		return paneNone, paneNone, m.width
	case m.overlay == overlayPicker, m.focus == focusPlaylists, m.focus == focusPlaylistTracks:
		return panePlaylists, panePlaylistTracks, m.width * 2 / 5
	case m.focus == focusQueue:
		return paneQueue, paneNone, m.width
	default:
		return paneResults, paneQueue, m.resultsWidth()
	}
}

func (m Model) resultsWidth() int {
	return m.width * 3 / 5
}

func (m Model) paneAt(x, y int) (listPane, int) {
	header, _, _, bodyHeight := m.layout()
	top := lipgloss.Height(header)
	if y < top || y >= top+bodyHeight {
		return paneNone, -1
	}
	left, right, leftW := m.bodyPanes()
	p := left
	if x >= leftW {
		p = right
	}
	if p == paneNone {
		return paneNone, -1
	}
	visible := listRows(bodyHeight)
	row := y - top - paneStyle.GetBorderTopSize() - 1 // title row
	if row < 0 || row >= visible {
		return p, -1
	}
	cursor, n := m.listCursor(p)
	i := scrollStart(cursor, visible) + row
	if i >= n {
		return p, -1
	}
	return p, i
}

func (m Model) listCursor(p listPane) (cursor, n int) {
	switch p {
	case paneResults:
		return m.resultCur, len(m.resultRows())
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

func (m Model) renderPlaylistPanes(height int) string {
	_, _, leftW := m.bodyPanes()
	rightW := m.width - leftW
	names := make([]string, len(m.playlists))
	for i, p := range m.playlists {
		names[i] = fmt.Sprintf("%s (%d)", p.Name, len(p.Tracks))
	}
	if len(names) == 0 {
		names = []string{dimStyle.Render("press " + m.keys.playlists.Create.Help().Key + " to create")}
	}
	tracks := m.selectedPlaylistTracks()
	lines := make([]string, len(tracks))
	for i, t := range tracks {
		lines[i] = resultLine(t, rightW-4, filterMatch{}, false)
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("empty playlist")}
	}
	leftTitle := "Playlists"
	if m.overlay == overlayPicker {
		leftTitle = "Save to playlist"
	}
	rightTitle := "Tracks"
	if m.playlistCur < len(m.playlists) {
		rightTitle = m.playlists[m.playlistCur].Name
	}
	left := pane(leftTitle, names, m.playlistCur, m.focus != focusPlaylistTracks, leftW, height)
	right := pane(rightTitle, lines, m.playlistTrackCur, m.focus == focusPlaylistTracks, rightW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) renderPlaylistName(height int) string {
	return pane("New playlist", []string{m.nameInput.View()}, -1, true, m.width, height)
}

const resultsTitle = "Results"

func (m Model) renderPanes(height int) string {
	_, _, leftW := m.bodyPanes()
	rightW := m.width - leftW

	title := m.resultsHeading()
	if m.filterOpen() {
		title += m.filterInput.View()
	}
	focused := m.focus == focusResults
	var left string
	switch rows := m.resultRows(); {
	case len(m.results) == 0:
		left = pane(title, []string{dimStyle.Render("press " + m.keys.global.Search.Help().Key + " to search")}, m.resultCur, focused, leftW, height)
	case len(rows) == 0:
		left = pane(title, []string{dimStyle.Render("no matches")}, -1, focused, leftW, height)
	default:
		left = rowsPane(title, len(rows), func(i, innerW int, selected bool) string {
			line := ansi.Truncate(resultLine(m.results[rows[i].index], leftW-4, rows[i], selected), innerW, "…")
			if pad := innerW - lipgloss.Width(line); selected && pad > 0 {
				line += cursorStyle.Render(strings.Repeat(" ", pad))
			}
			return line
		}, m.resultCur, focused, leftW, height)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, m.renderQueue(rightW, height, false))
}

// The Queue tab has the full width to itself, so its rows carry the position,
// channel and length that the narrow pane beside Results has no room for.
func (m Model) renderQueue(width, height int, detailed bool) string {
	lines := make([]string, len(m.queue.entries))
	for i, e := range m.queue.entries {
		lines[i] = m.queueLine(i, e, width-4, detailed)
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("empty — press " + m.keys.results.Enqueue.Help().Key + " on a result")}
	}
	return pane(fmt.Sprintf("Queue (%d)", len(m.queue.entries)), lines, m.queueCur, m.focus == focusQueue, width, height)
}

func (m Model) filterOpen() bool {
	return m.filterInput.Focused() || m.filterInput.Value() != ""
}

// resultsHeading is the Results title before the filter input; model.go
// reserves room for the widest count when sizing that input.
func (m Model) resultsHeading() string {
	if !m.filterOpen() {
		return resultsTitle
	}
	return fmt.Sprintf("%s %d/%d", resultsTitle, len(m.resultRows()), len(m.results))
}

func pane(title string, lines []string, cursor int, focused bool, width, height int) string {
	return rowsPane(title, len(lines), func(i, innerW int, selected bool) string {
		line := ansi.Truncate(lines[i], innerW, "…")
		if selected {
			line = cursorStyle.Width(innerW).Render(ansi.Strip(line))
		}
		return line
	}, cursor, focused, width, height)
}

// rowsPane lets a row render its own selection, so styled spans inside a row
// can survive the cursor bar.
func rowsPane(title string, n int, row func(i, innerW int, selected bool) string, cursor int, focused bool, width, height int) string {
	style := paneStyle
	if focused {
		style = paneFocus
	}
	innerW := width - style.GetHorizontalFrameSize()
	innerH := listRows(height)

	start := scrollStart(cursor, innerH)
	end := min(n, start+max(innerH, 0))

	rows := []string{headStyle.Render(title)}
	for i := start; i < end; i++ {
		rows = append(rows, row(i, innerW, focused && i == cursor))
	}
	return style.Width(width).Height(height).Render(strings.Join(rows, "\n"))
}

func listRows(height int) int {
	return height - paneStyle.GetVerticalFrameSize() - 1 // title row
}

func scrollStart(cursor, visible int) int {
	if cursor >= visible {
		return cursor - visible + 1
	}
	return 0
}

func resultLine(t youtube.Track, width int, match filterMatch, selected bool) string {
	text, dim := lipgloss.NewStyle(), dimStyle
	if selected {
		text, dim = cursorStyle, cursorStyle
	}
	return trackLine(t, width, match, text, dim)
}

func trackLine(t youtube.Track, width int, match filterMatch, text, dim lipgloss.Style) string {
	meta := formatDuration(t.Duration)
	if t.Live {
		meta = "LIVE"
	}
	right := dim.Render(" ") + highlight(t.Channel, match.channel, 20, dim) + dim.Render(" · "+meta)
	titleW := max(8, width-lipgloss.Width(right))
	return highlight(t.Title, match.title, titleW, text) + right
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

func (m Model) queueLine(i int, e mpv.PlaylistEntry, width int, detailed bool) string {
	t := m.tracks[e.Filename]
	t.Title = displayTitle(e, t)
	text, marker := lipgloss.NewStyle(), "  "
	if i == m.queue.pos {
		text, marker = playingStyle, "▶ "
	}
	if !detailed {
		return text.Render(ansi.Truncate(marker+t.Title, width, "…"))
	}
	prefix := text.Render(marker) + dimStyle.Render(fmt.Sprintf("%*d ", len(strconv.Itoa(len(m.queue.entries))), i+1))
	return prefix + trackLine(t, width-lipgloss.Width(prefix), filterMatch{}, text, dimStyle)
}

func (m Model) renderNowPlaying() string {
	e, t, ok := m.current()
	innerW := m.width - paneStyle.GetHorizontalFrameSize()

	var info []string
	if !ok {
		info = []string{dimStyle.Render("nothing playing")}
	} else {
		icon := "▶"
		if m.paused {
			icon = "⏸"
		}
		info = append(info, nowTitleStyle.Render(icon+" "+displayTitle(e, t)))
		if t.Channel != "" {
			info = append(info, dimStyle.Render(t.Channel))
		}
		info = append(info, dimStyle.Render(m.audioLine()))
	}

	textW := innerW
	var thumb string
	if ok {
		thumb = m.thumb.view()
	}
	if thumb != "" {
		thumb += " "
		textW -= thumbCols + 1
	}
	info = append(info, m.progressLine(t.Live, textW))
	for i := range info {
		info[i] = ansi.Truncate(info[i], textW, "…")
	}

	content := lipgloss.JoinHorizontal(lipgloss.Top, thumb, strings.Join(info, "\n"))
	return paneStyle.Width(m.width).Render(content)
}

func (m Model) audioLine() string {
	var parts []string
	if m.codec != "" {
		parts = append(parts, m.codec)
	}
	if m.params.SampleRate > 0 {
		parts = append(parts, fmt.Sprintf("%gkHz", float64(m.params.SampleRate)/1000))
	}
	if d, ok := m.currentDevice(); ok {
		parts = append(parts, "→ "+d.Label())
	}
	if m.normalize {
		parts = append(parts, "norm")
	}
	if r := m.repeat(); r != mpv.RepeatOff {
		parts = append(parts, "repeat "+r.String())
	}
	return strings.Join(parts, " · ")
}

func (m Model) progressLine(live bool, width int) string {
	vol := fmt.Sprintf("  vol %d%%", int(m.volume))
	if live {
		return errorStyle.Render("● LIVE") + dimStyle.Render(vol)
	}
	elapsed, total := formatDuration(m.timePos), formatDuration(m.duration)
	barW := width - len(elapsed) - len(total) - len(vol) - 2
	if barW < 4 {
		return elapsed + " / " + total + vol
	}
	filled := 0
	if m.duration > 0 {
		filled = min(barW, int(float64(barW)*float64(m.timePos)/float64(m.duration)))
	}
	bar := playingStyle.Render(strings.Repeat("━", filled)) + dimStyle.Render(strings.Repeat("─", barW-filled))
	return elapsed + " " + bar + " " + total + dimStyle.Render(vol)
}

func (m Model) renderSpectrum(width, height int) string {
	blocks := []rune(" ▁▂▃▄▅▆▇█")
	rows := make([]string, height)
	for r := range height {
		color := vizGradient[min(len(vizGradient)-1, (height-1-r)*len(vizGradient)/height)]
		var b strings.Builder
		for x := range width {
			level := 0.0
			if n := len(m.levels); n > 0 {
				level = m.levels[x*n/width]
			}
			fill := level*float64(height) - float64(height-1-r)
			idx := int(min(max(fill, 0), 1) * 8)
			b.WriteRune(blocks[idx])
		}
		rows[r] = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(b.String())
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderDevices(width, height int) string {
	lines := make([]string, len(m.devices))
	for i, d := range m.devices {
		mark := "  "
		if m.isCurrentDevice(d) {
			mark = "● "
		}
		lines[i] = mark + d.Label()
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("no output devices")}
	}
	return pane("Output device", lines, m.deviceCur, true, width, height)
}

func (m Model) renderInfo(height int) string {
	return pane("Track info", m.infoLines(), -1, true, m.width, height)
}

func (m Model) infoLines() []string {
	e, t, ok := m.current()
	if !ok {
		return []string{dimStyle.Render("nothing playing")}
	}
	length := formatDuration(m.duration)
	if t.Live {
		length = "LIVE"
	}
	rows := [][2]string{
		{"Title", displayTitle(e, t)},
		{"Channel", t.Channel},
		{"URL", e.Filename},
		{"Video ID", t.ID},
		{"Length", length},
	}

	// A stream read before a track change describes the previous file.
	stream := m.stream
	if stream.Path != e.Filename {
		stream = mpv.StreamInfo{}
	}
	codec := stream.Codec
	if codec == "" {
		codec = m.codec
	}
	rows = append(rows, [2]string{"Codec", codec})
	if yt, ok := youtube.StreamOf(stream.Opened); ok {
		format := "itag " + yt.Itag
		if yt.MIME != "" {
			format += " · " + yt.MIME
		}
		rows = append(rows, [2]string{"Format", format})
		if b := yt.Bitrate(); b > 0 {
			rows = append(rows, [2]string{"Bitrate", fmt.Sprintf("%d kbps average", b/1000)})
		}
		if yt.Size > 0 {
			rows = append(rows, [2]string{"Size", fmt.Sprintf("%.1f MiB", float64(yt.Size)/(1<<20))})
		}
	} else if stream.Bitrate > 0 {
		rows = append(rows, [2]string{"Bitrate", fmt.Sprintf("%d kbps", stream.Bitrate/1000)})
	}
	if m.params.SampleRate > 0 {
		rows = append(rows, [2]string{"Decoded", fmt.Sprintf("%gkHz · %s · %s", float64(m.params.SampleRate)/1000, m.params.Channels, m.params.Format)})
	}
	if d, ok := m.currentDevice(); ok {
		rows = append(rows, [2]string{"Output", d.Label()})
	}
	norm := "off"
	if m.normalize {
		norm = "on"
	}
	rows = append(rows, [2]string{"Normalize", norm})

	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		if r[1] != "" {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("%-10s", r[0]))+r[1])
		}
	}
	return lines
}

func (m Model) renderFooter() string {
	status := dimStyle.Render(m.status)
	if m.statusErr {
		status = errorStyle.Render(m.status)
	}
	if m.fullHelp {
		return ansi.Truncate(status, m.width, "…") + "\n" + ansi.Truncate(m.help.ShortHelpView([]key.Binding{m.keys.closeHelp}), m.width, "…")
	}
	// "? more" is rendered apart from the context entries, since truncation
	// drops trailing entries and it must stay visible.
	more := m.help.ShortHelpView([]key.Binding{m.keys.global.Help})
	sep := m.help.Styles.ShortSeparator.Render(m.help.ShortSeparator)
	line := more
	if ctx := m.contextHelp(m.help.Width() - lipgloss.Width(sep+more)); ctx != "" {
		line = ctx + sep + more
	} else {
		ellipsis := m.help.Styles.Ellipsis.Inline(true).Render(m.help.Ellipsis)
		for _, separator := range []string{sep, " ", ""} {
			candidate := ellipsis + separator + more
			if lipgloss.Width(candidate) <= m.width {
				line = candidate
				break
			}
		}
	}
	return ansi.Truncate(status, m.width, "…") + "\n" + line
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
	columns := make([]string, 0, 2*len(groups)-1)
	for i, g := range groups {
		view := headStyle.Render(g.title) + "\n" + m.help.FullHelpView([][]key.Binding{slices.Concat(g.keys.FullHelp()...)})
		if i > 0 {
			columns = append(columns, "    ")
		}
		columns = append(columns, view)
	}
	if horizontal := lipgloss.JoinHorizontal(lipgloss.Top, columns...); lipgloss.Width(horizontal) <= m.width-paneFocus.GetHorizontalFrameSize() {
		return pane("Keys", strings.Split(horizontal, "\n"), -1, true, m.width, height)
	}

	innerWidth := m.width - paneFocus.GetHorizontalFrameSize()
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
			if lipgloss.Width(line+separator+entry) <= innerWidth {
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
	return pane("Keys", lines, -1, true, m.width, height)
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

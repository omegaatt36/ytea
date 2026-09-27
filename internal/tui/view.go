package tui

import (
	"fmt"
	"image"
	"strings"
	"time"

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
	if m.showViz {
		viz = m.renderSpectrum(m.width, vizRows)
	}

	var body string
	switch {
	case m.overlay == overlayDevices:
		body = m.renderDevices(m.width, bodyHeight)
	case m.overlay == overlayInfo:
		body = m.renderInfo(bodyHeight)
	case m.overlay == overlayName:
		body = m.renderPlaylistName(bodyHeight)
	case m.overlay == overlayPicker, m.focus == focusPlaylists, m.focus == focusPlaylistTracks:
		body = m.renderPlaylistPanes(bodyHeight)
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
	if m.showViz {
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
	default:
		return paneResults, paneQueue, m.width * 3 / 5
	}
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
		return m.resultCur, len(m.results)
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
		names = []string{dimStyle.Render("press c to create")}
	}
	tracks := m.selectedPlaylistTracks()
	lines := make([]string, len(tracks))
	for i, t := range tracks {
		lines[i] = resultLine(t, rightW-4)
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("empty playlist")}
	}
	leftTitle := "Playlists"
	if m.overlay == overlayPicker {
		leftTitle = "Save to playlist — enter to select"
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
	return pane("New playlist — enter to save, esc to cancel", []string{m.nameInput.View()}, -1, true, m.width, height)
}

func (m Model) renderPanes(height int) string {
	_, _, leftW := m.bodyPanes()
	rightW := m.width - leftW

	results := make([]string, len(m.results))
	for i, t := range m.results {
		results[i] = resultLine(t, leftW-4)
	}
	if len(results) == 0 {
		results = []string{dimStyle.Render("press / to search")}
	}

	queue := make([]string, len(m.queue.entries))
	for i, e := range m.queue.entries {
		queue[i] = m.queueLine(i, e, rightW-4)
	}
	if len(queue) == 0 {
		queue = []string{dimStyle.Render("empty — press a on a result")}
	}

	left := pane("Results", results, m.resultCur, m.focus == focusResults, leftW, height)
	right := pane(fmt.Sprintf("Queue (%d)", len(m.queue.entries)), queue, m.queueCur, m.focus == focusQueue, rightW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func pane(title string, lines []string, cursor int, focused bool, width, height int) string {
	style := paneStyle
	if focused {
		style = paneFocus
	}
	innerW := width - style.GetHorizontalFrameSize()
	innerH := listRows(height)

	start := scrollStart(cursor, innerH)
	end := min(len(lines), start+max(innerH, 0))

	rows := []string{headStyle.Render(title)}
	for i := start; i < end; i++ {
		line := ansi.Truncate(lines[i], innerW, "…")
		if focused && i == cursor {
			line = cursorStyle.Width(innerW).Render(ansi.Strip(line))
		}
		rows = append(rows, line)
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

func resultLine(t youtube.Track, width int) string {
	meta := formatDuration(t.Duration)
	if t.Live {
		meta = "LIVE"
	}
	right := dimStyle.Render(fmt.Sprintf(" %s · %s", ansi.Truncate(t.Channel, 20, "…"), meta))
	titleW := max(8, width-lipgloss.Width(right))
	return ansi.Truncate(t.Title, titleW, "…") + right
}

func (m Model) queueLine(i int, e mpv.PlaylistEntry, width int) string {
	title := displayTitle(e, m.tracks[e.Filename])
	marker := "  "
	if i == m.queue.pos {
		marker = "▶ "
		return playingStyle.Render(ansi.Truncate(marker+title, width, "…"))
	}
	return ansi.Truncate(marker+title, width, "…")
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
	return pane("Output device — enter to switch, esc to cancel", lines, m.deviceCur, true, width, height)
}

func (m Model) renderInfo(height int) string {
	return pane("Track info — y copy URL, esc to close", m.infoLines(), -1, true, m.width, height)
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
	viz := ""
	if m.deps.Tap != nil {
		viz = "v viz · "
	}
	help := dimStyle.Render("/ search · enter play · a queue · s save · tab next pane · space pause · ←→ seek · n/p next/prev · +/- vol · N norm · o output · i info · " + viz + "r radio · q quit")
	switch m.overlay {
	case overlayPicker:
		help = dimStyle.Render("enter save track · c new playlist · esc cancel · q quit")
	case overlayName:
		help = dimStyle.Render("enter create playlist · esc cancel")
	case overlayInfo:
		help = dimStyle.Render("y copy URL · esc close · space pause · ←→ seek · n/p next/prev · +/- vol")
	case overlayNone:
		switch m.focus {
		case focusQueue:
			help = dimStyle.Render("enter jump · s save track · S save queue · d remove · C clear queue · J/K move · i info · tab next pane · n/p next/prev · q quit")
		case focusPlaylists:
			help = dimStyle.Render("c create · enter browse · a queue all · D delete playlist · tab next pane · / search · q quit")
		case focusPlaylistTracks:
			help = dimStyle.Render("enter play · a queue · d remove saved track · esc back · tab next pane · / search · q quit")
		}
	}
	return ansi.Truncate(status, m.width, "…") + "\n" + ansi.Truncate(help, m.width, "…")
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

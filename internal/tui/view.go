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
	"github.com/omegaatt36/ytea/internal/thumbnail"
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

// View renders the UI.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
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
	if m.focus == focusDevices {
		body = m.renderDevices(m.width, bodyHeight)
	} else {
		body = m.renderPanes(bodyHeight)
	}

	parts := []string{header, body, now}
	if viz != "" {
		parts = append(parts, viz)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// layout renders the fixed-height sections and returns the height left for the
// list panes. thumbOrigin relies on it too, so the two cannot disagree about
// where the now-playing box sits.
func (m Model) layout() (header, now, footer string, bodyHeight int) {
	header, now, footer = m.renderHeader(), m.renderNowPlaying(), m.renderFooter()
	used := lipgloss.Height(header) + lipgloss.Height(now) + lipgloss.Height(footer)
	if m.showViz {
		used += vizRows
	}
	return header, now, footer, max(3, m.height-used)
}

// thumbOrigin is the 0-based cell of the thumbnail's top-left corner: just
// inside the now-playing box, which sits below the header and the list panes.
func (m Model) thumbOrigin() image.Point {
	header, _, _, bodyHeight := m.layout()
	return image.Pt(paneStyle.GetBorderLeftSize(), lipgloss.Height(header)+bodyHeight+paneStyle.GetBorderTopSize())
}

func (m Model) renderHeader() string {
	title := titleStyle.Render("ytea")
	search := m.input.View()
	if m.searching {
		search += " " + m.spinner.View()
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, title, search)
}

func (m Model) renderPanes(height int) string {
	leftW := m.width * 3 / 5
	rightW := m.width - leftW

	results := make([]string, len(m.results))
	for i, t := range m.results {
		results[i] = resultLine(t, leftW-4)
	}
	if len(results) == 0 {
		results = []string{dimStyle.Render("press / to search")}
	}

	queue := make([]string, len(m.queue))
	for i, e := range m.queue {
		queue[i] = m.queueLine(i, e, rightW-4)
	}
	if len(queue) == 0 {
		queue = []string{dimStyle.Render("empty — press a on a result")}
	}

	left := pane("Results", results, m.resultCur, m.focus == focusResults, leftW, height)
	right := pane(fmt.Sprintf("Queue (%d)", len(m.queue)), queue, m.queueCur, m.focus == focusQueue, rightW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// pane draws a bordered, scrolling list that keeps the cursor in view.
func pane(title string, lines []string, cursor int, focused bool, width, height int) string {
	style := paneStyle
	if focused {
		style = paneFocus
	}
	innerW := width - style.GetHorizontalFrameSize()
	innerH := height - style.GetVerticalFrameSize() - 1 // title row

	start := 0
	if cursor >= innerH {
		start = cursor - innerH + 1
	}
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
	if i == m.pos {
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
	switch {
	case !ok:
	case m.thumbID != 0 && m.graphics == graphicsPlaceholder:
		thumb = thumbnail.Placeholder(m.thumbID, thumbCols, thumbRows)
	case m.thumbID != 0:
		// Direct placement draws the image on top; reserve blank cells under it.
		thumb = strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", thumbCols)+"\n", thumbRows), "\n")
	case m.thumbArt != "":
		thumb = m.thumbArt
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

// renderSpectrum maps the band levels onto width columns of eighth-block bars.
func (m Model) renderSpectrum(width, height int) string {
	blocks := []rune(" ▁▂▃▄▅▆▇█")
	rows := make([]string, height)
	for r := range height {
		// Row 0 is the top; colour climbs the gradient with height.
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

func (m Model) renderFooter() string {
	status := dimStyle.Render(m.status)
	if m.statusErr {
		status = errorStyle.Render(m.status)
	}
	help := dimStyle.Render("/ search · enter play · a queue · tab pane · space pause · ←→ seek · n/p next/prev · +/- vol · N norm · o output · v viz · r radio · q quit")
	if m.focus == focusQueue {
		help = dimStyle.Render("enter jump · d remove · J/K move · tab pane · space pause · n/p next/prev · r radio · q quit")
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

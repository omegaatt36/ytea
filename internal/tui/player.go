package tui

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
)

// playerState mirrors mpv's playback properties.
type playerState struct {
	timePos, duration time.Duration
	paused, idle      bool
	volume            float64
	codec             string
	params            mpv.AudioParams
	device            string
	normalize         bool
	loopPlaylist      bool
	loopFile          bool
}

// apply reports whether the change can switch the playing track.
func (p *playerState) apply(ev mpv.Event) bool {
	switch ev.Prop {
	case mpv.PropTimePos:
		p.timePos = seconds(mpv.Decode[float64](ev.Data))
	case mpv.PropDuration:
		p.duration = seconds(mpv.Decode[float64](ev.Data))
	case mpv.PropPause:
		p.paused = mpv.Decode[bool](ev.Data)
	case mpv.PropIdle:
		if mpv.Decode[bool](ev.Data) {
			p.stop()
		} else {
			p.idle = false
		}
		return true
	case mpv.PropVolume:
		p.volume = mpv.Decode[float64](ev.Data)
	case mpv.PropCodec:
		p.codec = mpv.Decode[string](ev.Data)
	case mpv.PropAudioParams:
		p.params = mpv.Decode[mpv.AudioParams](ev.Data)
	case mpv.PropAudioDevice:
		p.device = mpv.Decode[string](ev.Data)
	case mpv.PropAF:
		p.normalize = slices.ContainsFunc(mpv.Decode[[]mpv.Filter](ev.Data), func(f mpv.Filter) bool {
			return f.Label == mpv.NormalizeLabel
		})
	case mpv.PropLoopPlaylist:
		p.loopPlaylist = mpv.LoopOn(ev.Data)
	case mpv.PropLoopFile:
		p.loopFile = mpv.LoopOn(ev.Data)
	case mpv.PropPlaylistPos:
		p.timePos = 0
		return true
	}
	return false
}

func (p *playerState) stop() {
	p.idle = true
	p.timePos, p.duration = 0, 0
}

func (p playerState) repeat() mpv.Repeat {
	return mpv.RepeatFrom(p.loopPlaylist, p.loopFile)
}

// deviceName is mpv's audio-device, which is "auto" until one is chosen.
func (p playerState) deviceName() string {
	if p.device == "" {
		return "auto"
	}
	return p.device
}

const (
	detailRows = thumbRows - 1
	vuRows     = 9
)

type vizMode uint8

const (
	vizSpectrum vizMode = iota
	vizVU
)

func (m Model) playerContentRows() int {
	if m.showViz && m.vizMode == vizVU && m.height >= 22 && !m.fullHelp && m.overlay == overlayNone {
		_, textW := m.playerText()
		if m.vizWidth(textW) > 0 {
			return vuRows
		}
	}
	return detailRows
}

func (m Model) playerRows() int { return m.playerContentRows() + 3 }

// renderPlayer is the now-playing bar. The status rides its bottom edge, the
// row the eye already returns to after every action.
func (m Model) renderPlayer() string {
	e, t, ok := m.current()
	art := m.playerArt()
	_, textW := m.playerText()
	contentRows := m.playerContentRows()

	details := make([]string, 0, detailRows)
	if ok {
		icon := "▶"
		if m.player.paused {
			icon = "⏸"
		}
		details = append(details, nowTitleStyle.Render(icon+" "+displayTitle(e, t)))
		if t.Channel != "" {
			details = append(details, dimStyle.Render(t.Channel))
		}
		details = append(details, dimStyle.Render(m.audioLine()))
	} else {
		details = append(details, dimStyle.Render("nothing playing"))
	}
	details = append(details, m.modeLine())

	vizW := m.vizWidth(textW)
	infoW := textW - vizW
	rows := make([]string, contentRows)
	for i := range rows {
		if i < len(details) {
			rows[i] = details[i]
		}
		rows[i] = fit(rows[i], infoW)
	}
	block := strings.Join(rows, "\n")
	if vizW > 0 {
		var viz string
		if m.vizMode == vizVU {
			viz = m.renderVU(vizW-2, contentRows)
		} else {
			viz = m.renderSpectrum(vizW-2, contentRows)
		}
		block = lipgloss.JoinHorizontal(lipgloss.Top, block, "  ", viz)
	}
	block += "\n" + m.progressLine(t.Live, textW)
	if art != "" {
		block = lipgloss.JoinHorizontal(lipgloss.Top, art, "  ", block)
	}

	statusText, statusErr := m.status, m.statusErr
	if !statusErr {
		statusText = m.spectrumUnavailable
		if statusText == "" {
			statusText = m.spectrumFailure
		}
		if statusText == "" {
			statusText = m.status
		} else {
			statusErr = true
		}
	}
	status := dimStyle.Render(statusText)
	if statusErr {
		status = errorStyle.Render(statusText)
	}
	return box("Now playing", status, padRows(strings.Split(block, "\n")), false, m.width, m.playerRows())
}

func (m Model) playerArt() string {
	if _, _, ok := m.current(); !ok {
		return ""
	}
	return m.thumb.view()
}

// playerText is the column where the details and progress row start, and their width.
func (m Model) playerText() (x, w int) {
	x, w = boxInset, m.width-2*boxInset
	if m.playerArt() != "" {
		x, w = x+thumbCols+2, w-thumbCols-2
	}
	return x, w
}

// progressBar is where the progress bar's cells lie on screen; it is empty
// when none is drawn.
func (m Model) progressBar() image.Rectangle {
	_, t, ok := m.current()
	if !ok || t.Live {
		return image.Rectangle{}
	}
	x, textW := m.playerText()
	elapsed, total := formatDuration(m.player.timePos), formatDuration(m.player.duration)
	barW := progressBarWidth(textW, elapsed, total)
	if barW < minProgressBar {
		return image.Rectangle{}
	}
	x += len(elapsed) + 1
	y := m.screen().player.Min.Y + 1 + m.playerContentRows()
	return image.Rect(x, y, x+barW, y+1)
}

const minProgressBar = 4

func progressBarWidth(width int, elapsed, total string) int {
	return width - len(elapsed) - len(total) - 2
}

// vizWidth leaves the details at least a readable column; the spectrum is
// decoration and gives way first.
func (m Model) vizWidth(textW int) int {
	if !m.showViz || m.spectrumUnavailable != "" || m.spectrumFailure != "" {
		return 0
	}
	w := min(48, textW*2/5)
	if m.vizMode == vizVU && w < 20 {
		return 0
	}
	if textW-w < 32 {
		return 0
	}
	return w
}

func (m Model) audioLine() string {
	var parts []string
	if m.player.codec != "" {
		parts = append(parts, m.player.codec)
	}
	if m.player.params.SampleRate > 0 {
		parts = append(parts, fmt.Sprintf("%gkHz", float64(m.player.params.SampleRate)/1000))
	}
	if d, ok := m.currentDevice(); ok {
		parts = append(parts, "→ "+d.Label())
	}
	return strings.Join(parts, " · ")
}

// modeLine shows the settings a key toggles, so each press has a visible echo.
func (m Model) modeLine() string {
	parts := []string{fmt.Sprintf("vol %d%%", int(m.player.volume))}
	if m.player.normalize {
		parts = append(parts, "leveling")
	}
	if r := m.player.repeat(); r != mpv.RepeatOff {
		parts = append(parts, "repeat "+r.String())
	}
	return dimStyle.Render(strings.Join(parts, " · "))
}

func (m Model) progressLine(live bool, width int) string {
	if live {
		return errorStyle.Render("● LIVE")
	}
	elapsed, total := formatDuration(m.player.timePos), formatDuration(m.player.duration)
	barW := progressBarWidth(width, elapsed, total)
	if barW < minProgressBar {
		return elapsed + dimStyle.Render(" / "+total)
	}
	filled := 0
	if m.player.duration > 0 {
		filled = min(barW, int(float64(barW)*float64(m.player.timePos)/float64(m.player.duration)))
	}
	bar := playingStyle.Render(strings.Repeat("━", filled)) + dimStyle.Render(strings.Repeat("─", barW-filled))
	return elapsed + " " + bar + " " + dimStyle.Render(total)
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

func (m Model) renderVU(width, height int) string {
	if width < 18 || height < 4 {
		return strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", max(width, 0))+"\n", max(height, 0)), "\n")
	}
	leftW := (width - 2) / 2
	rightW := width - 2 - leftW
	rows := make([]string, height)
	if height >= vuRows {
		left, right := vuDialLarge(leftW, m.vu[0], 'L'), vuDialLarge(rightW, m.vu[1], 'R')
		for i := range vuRows {
			rows[i] = left[i] + "  " + right[i]
		}
	} else {
		left, right := vuDial(leftW, m.vu[0], 'L'), vuDial(rightW, m.vu[1], 'R')
		for i := range 4 {
			rows[i] = left[i] + "  " + right[i]
		}
	}
	for i := 4; i < height; i++ {
		if rows[i] != "" {
			continue
		}
		rows[i] = strings.Repeat(" ", width)
	}
	return strings.Join(rows, "\n")
}

func vuDialLarge(width int, level float64, channel rune) [vuRows]string {
	var rows [vuRows]string
	face := []rune("╭" + strings.Repeat("─", width-2) + "╮")
	copy(face[(width-4)/2:], []rune{channel, ' ', 'V', 'U'})
	rows[0] = dimStyle.Render(string(face))

	faceStyle := lipgloss.NewStyle().Background(match).Foreground(lipgloss.Color("0"))
	needleStyle := lipgloss.NewStyle().Background(match).Foreground(danger)
	rows[1] = faceStyle.Render(string(vuScale(width)))

	const pixelRows = 20
	arc := make([][]uint8, 5)
	needle := make([][]uint8, 5)
	for i := range arc {
		arc[i] = make([]uint8, width)
		needle[i] = make([]uint8, width)
	}
	center, radius := width-1, width-3
	arcY := func(x int) int {
		t := min(max(float64(x-center)/float64(radius), -1), 1)
		return 1 + int(math.Round(8*(1-math.Sqrt(1-t*t))))
	}
	for x := center - radius; x <= center+radius; x++ {
		setVUDot(arc, x, arcY(x))
	}
	for _, db := range []float64{-20, -10, -5, 0, 3} {
		x := 2 * vuPosition(width, 0.5*math.Pow(10, db/20))
		for y := arcY(x); y <= min(arcY(x)+2, pixelRows-1); y++ {
			setVUDot(arc, x, y)
		}
	}
	endX := 2 * vuPosition(width, level)
	endY := min(arcY(endX)+1, pixelRows-1)
	pivotX, pivotY := center, pixelRows-1
	steps := max(int(math.Abs(float64(endX-pivotX))), pivotY-endY)
	for step := range steps + 1 {
		x := pivotX + int(math.Round(float64(endX-pivotX)*float64(step)/float64(steps)))
		y := pivotY + int(math.Round(float64(endY-pivotY)*float64(step)/float64(steps)))
		setVUDot(needle, x, y)
	}
	for i := range 5 {
		var row strings.Builder
		var segment strings.Builder
		red := false
		flush := func() {
			if segment.Len() == 0 {
				return
			}
			style := faceStyle
			if red {
				style = needleStyle
			}
			row.WriteString(style.Render(segment.String()))
			segment.Reset()
		}
		for x := range width {
			bits := arc[i][x] | needle[i][x]
			cell := ' '
			if bits != 0 {
				cell = rune(0x2800 | int(bits))
			}
			if x == 0 || x == width-1 {
				cell = '│'
			}
			if colorNeedle := needle[i][x] != 0; colorNeedle != red {
				flush()
				red = colorNeedle
			}
			segment.WriteRune(cell)
		}
		flush()
		rows[i+2] = row.String()
	}
	pivot := []rune("│" + strings.Repeat(" ", width-2) + "│")
	pivot[(width-1)/2] = '●'
	rows[7] = faceStyle.Render(string(pivot))
	rows[8] = dimStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")
	return rows
}

func setVUDot(cells [][]uint8, x, y int) {
	if y < 0 || y >= len(cells)*4 || x < 0 || x >= len(cells[0])*2 {
		return
	}
	bits := [4][2]uint8{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	cells[y/4][x/2] |= bits[y%4][x%2]
}

func vuScale(width int) []rune {
	scale := []rune("│" + strings.Repeat(" ", width-2) + "│")
	mark := func(at int, label string) { copy(scale[at:width-1], []rune(label)) }
	mark(1, "−")
	mark(width-2, "+")
	if width >= 14 {
		mark(1, "−20")
		mark(width-3, "+3")
	}
	if width >= 18 {
		mark(vuPosition(width, math.Pow(10, -10.0/20)*0.5)-1, "−10")
		mark(vuPosition(width, 0.5)-1, "0")
	}
	return scale
}

func vuDial(width int, level float64, channel rune) [4]string {
	face := []rune("╭" + strings.Repeat("─", width-2) + "╮")
	copy(face[(width-4)/2:], []rune{channel, ' ', 'V', 'U'})
	scale := vuScale(width)
	needle := []rune("│" + strings.Repeat(" ", width-2) + "│")
	position := vuPosition(width, level)
	needle[position] = '▲'
	base := []rune("╰" + strings.Repeat("─", width-2) + "╯")
	base[width/2] = '●'
	needleColor := match
	if level > 0.5 {
		needleColor = danger
	}
	return [4]string{
		dimStyle.Render(string(face)),
		lipgloss.NewStyle().Foreground(match).Render(string(scale)),
		lipgloss.NewStyle().Foreground(needleColor).Render(string(needle)),
		dimStyle.Render(string(base)),
	}
}

func vuPosition(width int, level float64) int {
	if level <= 0 {
		return 1
	}
	db := 20 * math.Log10(level/0.5)
	return 1 + int(math.Round(min(max((db+20)/23, 0), 1)*float64(width-3)))
}

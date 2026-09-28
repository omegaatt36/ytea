package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
)

// Beside the art: details and the spectrum over one progress row.
const detailRows = thumbRows - 1

// renderPlayer is the now-playing bar. The status rides its bottom edge, the
// row the eye already returns to after every action.
func (m Model) renderPlayer() string {
	e, t, ok := m.current()
	textW := m.width - 2*boxInset

	var art string
	if ok {
		art = m.thumb.view()
	}
	if art != "" {
		textW -= thumbCols + 2
	}

	details := make([]string, 0, detailRows)
	if ok {
		icon := "▶"
		if m.paused {
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
	rows := make([]string, detailRows)
	for i := range rows {
		if i < len(details) {
			rows[i] = details[i]
		}
		rows[i] = fit(rows[i], infoW)
	}
	block := strings.Join(rows, "\n")
	if vizW > 0 {
		block = lipgloss.JoinHorizontal(lipgloss.Top, block, "  ", m.renderSpectrum(vizW-2, detailRows))
	}
	block += "\n" + m.progressLine(t.Live, textW)
	if art != "" {
		block = lipgloss.JoinHorizontal(lipgloss.Top, art, "  ", block)
	}

	status := dimStyle.Render(m.status)
	if m.statusErr {
		status = errorStyle.Render(m.status)
	}
	return box("Now playing", status, padRows(strings.Split(block, "\n")), false, m.width, playerRows)
}

// vizWidth leaves the details at least a readable column; the spectrum is
// decoration and gives way first.
func (m Model) vizWidth(textW int) int {
	if !m.showViz {
		return 0
	}
	w := min(48, textW*2/5)
	if textW-w < 32 {
		return 0
	}
	return w
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
	return strings.Join(parts, " · ")
}

// modeLine shows the settings a key toggles, so each press has a visible echo.
func (m Model) modeLine() string {
	parts := []string{fmt.Sprintf("vol %d%%", int(m.volume))}
	if m.normalize {
		parts = append(parts, "leveling")
	}
	if r := m.repeat(); r != mpv.RepeatOff {
		parts = append(parts, "repeat "+r.String())
	}
	return dimStyle.Render(strings.Join(parts, " · "))
}

func (m Model) progressLine(live bool, width int) string {
	if live {
		return errorStyle.Render("● LIVE")
	}
	elapsed, total := formatDuration(m.timePos), formatDuration(m.duration)
	barW := width - len(elapsed) - len(total) - 2
	if barW < 4 {
		return elapsed + dimStyle.Render(" / "+total)
	}
	filled := 0
	if m.duration > 0 {
		filled = min(barW, int(float64(barW)*float64(m.timePos)/float64(m.duration)))
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

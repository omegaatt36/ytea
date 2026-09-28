package tui

import (
	"fmt"
	"image"
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

// Beside the art: details and the spectrum over one progress row.
const detailRows = thumbRows - 1

// renderPlayer is the now-playing bar. The status rides its bottom edge, the
// row the eye already returns to after every action.
func (m Model) renderPlayer() string {
	e, t, ok := m.current()
	art := m.playerArt()
	_, textW := m.playerText()

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
	return box("Now playing", status, padRows(strings.Split(block, "\n")), false, m.width, playerRows)
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
	y := m.screen().player.Min.Y + 1 + detailRows
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

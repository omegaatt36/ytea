package tui

import (
	"fmt"
	"image"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/youtube"
	"github.com/omegaatt36/ytea/service"
)

const (
	deviceDialogWidth = 60
	infoDialogWidth   = 80
	nameDialogWidth   = 56
)

// dialog places the open dialog, sized to its content, a little above the
// middle of body.
func (m Model) dialog(body image.Rectangle) (paneBox, bool) {
	p, w, rows := paneNone, 0, 0
	switch m.overlay {
	case overlayDevices:
		p, w, rows = paneDevices, deviceDialogWidth, max(1, len(m.core.Devices))
	case overlayInfo:
		w, rows = infoDialogWidth, len(m.infoLines())
	case overlayName:
		w, rows = nameDialogWidth, 1
	case overlayNone, overlayPicker:
		return paneBox{}, false
	}
	w = dialogWidth(w, body.Dx())
	h := min(rows+2, body.Dy())
	at := body.Min.Add(image.Pt((body.Dx()-w)/2, (body.Dy()-h)/3))
	return paneBox{p, image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}}, true
}

func dialogWidth(want, avail int) int {
	return max(1, min(want, avail-4))
}

func (m Model) renderDialog(width, height int) string {
	switch m.overlay {
	case overlayDevices:
		return m.renderDevices(width, height)
	case overlayInfo:
		return box("Track info", "", padRows(m.infoLines()), true, width, height)
	case overlayName:
		return box(m.nameMode.title(), "", []string{" " + m.nameInput.View()}, true, width, height)
	case overlayNone, overlayPicker:
	}
	return ""
}

func (m Model) renderDevices(width, height int) string {
	return m.list(paneDevices).render(width, height)
}

func (m Model) infoLines() []string {
	e, t, ok := m.core.Current()
	if !ok {
		return []string{dimStyle.Render("nothing playing")}
	}
	length := service.FormatDuration(m.core.Playback.Duration)
	if t.Live {
		length = "LIVE"
	}
	url := e.Filename
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		url = ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()
	}
	rows := [][2]string{
		{"Title", service.DisplayTitle(e, t)},
		{"Channel", service.Sanitize(t.Channel)},
		{"URL", url},
		{"Video ID", t.ID},
		{"Length", length},
	}

	// A stream read before a track change describes the previous file.
	stream := m.core.Stream
	if stream.Path != e.Filename {
		stream = domain.StreamInfo{}
	}
	codec := stream.Codec
	if codec == "" {
		codec = m.core.Playback.Codec
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
	if m.core.Playback.Params.SampleRate > 0 {
		rows = append(rows, [2]string{"Decoded", fmt.Sprintf("%gkHz · %s · %s", float64(m.core.Playback.Params.SampleRate)/1000, m.core.Playback.Params.Channels, m.core.Playback.Params.Format)})
	}
	if d, ok := m.core.CurrentDevice(); ok {
		rows = append(rows, [2]string{"Output", d.Label()})
	}
	norm := "off"
	if m.core.Playback.Normalize {
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

package tui

import (
	"context"
	"image"
	"log/slog"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/omegaatt36/ytea/internal/thumbnail"
	"github.com/omegaatt36/ytea/internal/youtube"
)

// placeDelay lets the renderer finish the frame (including any post-resize
// screen clear) before the image is put on top of it.
const placeDelay = 100 * time.Millisecond

type thumbImage struct {
	enabled bool
	client  *http.Client

	graphics         graphicsSupport
	probeKitty       bool
	probePlaceholder bool
	id               int
	art              string
	video            string
	placedAt         image.Point
	placed           bool
}

type graphicsSupport int

const (
	graphicsUnknown graphicsSupport = iota
	// kitty Unicode placeholders, which the cell renderer treats as text (Ghostty, kitty).
	graphicsPlaceholder
	// kitty graphics without placeholders (Zellij >= 0.45).
	graphicsDirect
	// half-block art (Zellij < 0.45, tmux).
	graphicsNone
)

func (g graphicsSupport) String() string {
	switch g {
	case graphicsPlaceholder:
		return "kitty placeholders"
	case graphicsDirect:
		return "kitty direct placement"
	case graphicsNone:
		return "half-blocks"
	default:
		return "unknown"
	}
}

type (
	thumbMsg struct {
		videoID string
		img     image.Image
		err     error
	}
	placeMsg struct {
		id int
		at image.Point
	}
)

func newThumbImage(enabled bool, client *http.Client) thumbImage {
	return thumbImage{enabled: enabled, client: client}
}

func (t thumbImage) init() tea.Cmd {
	if !t.enabled {
		return nil
	}
	return tea.Raw(thumbnail.Query())
}

func (t thumbImage) update(msg tea.Msg) (thumbImage, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.placed = false

	case uv.KittyGraphicsEvent:
		if t.graphics != graphicsUnknown {
			return t, nil
		}
		ok := string(msg.Payload) == "OK"
		switch msg.Options.ID {
		case thumbnail.QueryID:
			t.probeKitty = ok
		case thumbnail.PlaceholderQueryID:
			t.probePlaceholder = ok
		}

	case uv.PrimaryDeviceAttributesEvent:
		// DA1 is answered after both probes, so every kitty reply is in by now.
		if !t.enabled || t.graphics != graphicsUnknown {
			return t, nil
		}
		switch {
		case t.probePlaceholder:
			t.graphics = graphicsPlaceholder
		case t.probeKitty:
			t.graphics = graphicsDirect
		default:
			t.graphics = graphicsNone
		}
		slog.Info("thumbnail rendering", "mode", t.graphics.String())

	case thumbMsg:
		return t, t.apply(msg)

	case placeMsg:
		if t.graphics != graphicsDirect || msg.id != t.id || msg.at != t.placedAt {
			return t, nil
		}
		return t, tea.Raw(thumbnail.Put(msg.id, msg.at.X, msg.at.Y, thumbCols, thumbRows))
	}
	return t, nil
}

// refresh waits for the graphics probe, which decides the rendering to fetch.
func (t *thumbImage) refresh(playing youtube.Track) tea.Cmd {
	if !t.enabled || t.graphics == graphicsUnknown {
		return nil
	}
	if playing.ID == "" {
		t.art, t.video, t.placed = "", "", false
		return t.erase()
	}
	if playing.ID == t.video {
		return nil
	}
	t.video = playing.ID
	client, url := t.client, playing.ThumbnailURL()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		img, err := thumbnail.Fetch(ctx, client, url)
		return thumbMsg{videoID: playing.ID, img: img, err: err}
	}
}

func (t *thumbImage) erase() tea.Cmd {
	if t.id == 0 {
		return nil
	}
	id := t.id
	t.id = 0
	return tea.Raw(thumbnail.Delete(id))
}

func (t *thumbImage) apply(msg thumbMsg) tea.Cmd {
	// A slow fetch may land after the user already skipped ahead.
	if msg.videoID != t.video {
		return nil
	}
	if msg.err != nil {
		return reportErr(msg.err)
	}

	if t.graphics == graphicsNone {
		t.art = thumbnail.HalfBlocks(msg.img, thumbCols, thumbRows)
		return nil
	}

	prev := t.id
	t.id = nextThumbID(prev)
	var (
		seq string
		err error
	)
	if t.graphics == graphicsPlaceholder {
		seq, err = thumbnail.TransmitVirtual(msg.img, t.id, thumbCols, thumbRows)
	} else {
		// Direct mode uploads only; syncPlacement puts it once the layout is known.
		seq, err = thumbnail.Transmit(msg.img, t.id)
		t.placed = false
	}
	if err != nil {
		t.id = prev
		return reportErr(err)
	}
	if prev != 0 {
		seq += thumbnail.Delete(prev)
	}
	return tea.Raw(seq)
}

// origin is only called when a placement is possible, since computing it lays out the screen.
func (t *thumbImage) syncPlacement(origin func() image.Point) tea.Cmd {
	if t.graphics != graphicsDirect || t.id == 0 {
		return nil
	}
	at := origin()
	if t.placed && at == t.placedAt {
		return nil
	}
	t.placed, t.placedAt = true, at
	id := t.id
	return tea.Tick(placeDelay, func(time.Time) tea.Msg { return placeMsg{id: id, at: at} })
}

func (t thumbImage) view() string {
	switch {
	case t.id != 0 && t.graphics == graphicsPlaceholder:
		return thumbnail.Placeholder(t.id, thumbCols, thumbRows)
	case t.id != 0:
		// Direct placement draws the image on top; reserve blank cells under it.
		return strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", thumbCols)+"\n", thumbRows), "\n")
	}
	return t.art
}

// nextThumbID alternates ids within [16, 255]: a fresh id per track means the
// old placeholders never briefly show the new image at the wrong size.
func nextThumbID(prev int) int {
	if prev < 16 || prev >= 255 {
		return 16
	}
	return prev + 1
}

func reportErr(err error) tea.Cmd {
	return func() tea.Msg { return errMsg{err} }
}

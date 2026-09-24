// Package thumbnail renders images inline via the kitty graphics protocol.
//
// Images are placed with Unicode placeholders (U+10EEEE) rather than cursor
// positioning: the placeholders are ordinary cells, so Bubble Tea's cell
// renderer can diff and redraw them like any other text.
package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // YouTube thumbnails are JPEG.
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// Probe image ids. They sit below the [16, 255] range used for real thumbnails.
const (
	// QueryID probes for kitty graphics at all.
	QueryID = 1
	// PlaceholderQueryID probes for Unicode placeholder support (U=1).
	// Zellij 0.45 implements kitty graphics but rejects U=1 with ENOTSUPPORTED,
	// so graphics support alone does not imply placeholders work.
	PlaceholderQueryID = 2
)

// Query returns two kitty graphics probes followed by a primary device
// attributes request. Every terminal answers DA1, but only kitty-graphics
// terminals answer the probes first; multiplexers without the protocol
// (Zellij < 0.45, tmux) swallow them. This is the detection method the kitty
// protocol documents, and it stays correct when TERM_PROGRAM is inherited
// from an outer terminal.
func Query() string {
	return ansi.KittyGraphics([]byte("AAAA"), "i="+strconv.Itoa(QueryID), "s=1", "v=1", "a=q", "t=d", "f=24") +
		ansi.KittyGraphics([]byte("AAAA"), "i="+strconv.Itoa(PlaceholderQueryID), "s=1", "v=1", "a=q", "t=d", "f=24", "U=1") +
		ansi.RequestPrimaryDeviceAttributes
}

// Fetch downloads and decodes an image.
func Fetch(ctx context.Context, client *http.Client, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build thumbnail request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch thumbnail %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch thumbnail %s: status %s", url, resp.Status)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("decode thumbnail %s: %w", url, err)
	}
	return img, nil
}

// TransmitVirtual returns the escape sequence that uploads img as image id and
// creates a virtual placement spanning cols x rows cells, shown wherever
// [Placeholder] cells are drawn.
func TransmitVirtual(img image.Image, id, cols, rows int) (string, error) {
	var buf bytes.Buffer
	err := kitty.EncodeGraphics(&buf, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.PNG,
		ID:               id,
		Chunk:            true,
		VirtualPlacement: true,
		Columns:          cols,
		Rows:             rows,
		// Suppress OK/error replies; Bubble Tea would read them as keystrokes.
		Quiet: 2,
	})
	if err != nil {
		return "", fmt.Errorf("encode kitty image %d: %w", id, err)
	}
	return buf.String(), nil
}

// Transmit returns the escape sequence that uploads img as image id without
// displaying it; [Put] places it.
func Transmit(img image.Image, id int) (string, error) {
	var buf bytes.Buffer
	err := kitty.EncodeGraphics(&buf, img, &kitty.Options{
		Action:       kitty.Transmit,
		Transmission: kitty.Direct,
		Format:       kitty.PNG,
		ID:           id,
		Chunk:        true,
		Quiet:        2,
	})
	if err != nil {
		return "", fmt.Errorf("encode kitty image %d: %w", id, err)
	}
	return buf.String(), nil
}

// placementID is fixed so re-placing an image replaces its previous placement
// instead of stacking a second copy.
const placementID = 1

// Put returns the escape sequence that displays image id over cols x rows
// cells whose top-left corner is the 0-based cell (x, y). The cursor is saved
// and restored around it, and C=1 keeps the image from moving it, so the Bubble
// Tea renderer's idea of the cursor position stays correct.
func Put(id, x, y, cols, rows int) string {
	return ansi.SaveCursor +
		ansi.CursorPosition(x+1, y+1) +
		ansi.KittyGraphics(nil, "a=p", "i="+strconv.Itoa(id), "p="+strconv.Itoa(placementID),
			"c="+strconv.Itoa(cols), "r="+strconv.Itoa(rows), "C=1", "q=2") +
		ansi.RestoreCursor
}

// Delete returns the escape sequence that frees image id and its data.
func Delete(id int) string {
	return ansi.KittyGraphics(nil, "a=d", "d=I", fmt.Sprintf("i=%d", id), "q=2")
}

// Placeholder returns cols x rows of placeholder cells referencing image id.
// The image id travels in the foreground colour as a 256-colour index, so id
// must be in [16, 255] to avoid the basic palette being remapped.
func Placeholder(id, cols, rows int) string {
	fg := fmt.Sprintf("\x1b[38;5;%dm", id)
	var b strings.Builder
	for r := range rows {
		if r > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(fg)
		for c := range cols {
			// Row and column diacritics on every cell keep the mapping correct
			// even when the renderer repaints a partial line.
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(kitty.Diacritic(r))
			b.WriteRune(kitty.Diacritic(c))
		}
		b.WriteString("\x1b[39m")
	}
	return b.String()
}

// HalfBlocks renders img as cols x rows cells of "▀", each cell carrying two
// vertically stacked pixels (foreground = top, background = bottom). It is the
// fallback for terminals or multiplexers without kitty graphics: plain text and
// truecolor, so Zellij and tmux pass it through untouched.
func HalfBlocks(img image.Image, cols, rows int) string {
	px := downscale(img, cols, rows*2)
	var b strings.Builder
	for r := range rows {
		if r > 0 {
			b.WriteByte('\n')
		}
		for c := range cols {
			top, bottom := px[2*r][c], px[2*r+1][c]
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.r, top.g, top.b, bottom.r, bottom.g, bottom.b)
		}
		b.WriteString("\x1b[m")
	}
	return b.String()
}

type rgb struct{ r, g, b uint8 }

// downscale area-averages img into a w x h grid. Averaging instead of
// nearest-neighbour matters at this size: each output pixel covers ~18x18
// source pixels, and sampling one of them yields noisy, unrepresentative colours.
func downscale(img image.Image, w, h int) [][]rgb {
	bounds := img.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	out := make([][]rgb, h)
	for y := range h {
		out[y] = make([]rgb, w)
		y0, y1 := bounds.Min.Y+y*sh/h, bounds.Min.Y+max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := bounds.Min.X+x*sw/w, bounds.Min.X+max((x+1)*sw/w, x*sw/w+1)
			var rs, gs, bs, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, b, _ := img.At(sx, sy).RGBA()
					rs, gs, bs, n = rs+uint64(r>>8), gs+uint64(g>>8), bs+uint64(b>>8), n+1
				}
			}
			if n > 0 {
				out[y][x] = rgb{uint8(rs / n), uint8(gs / n), uint8(bs / n)}
			}
		}
	}
	return out
}

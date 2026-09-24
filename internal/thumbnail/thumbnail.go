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
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// Supported reports whether the terminal speaks kitty graphics with Unicode placeholders.
func Supported() bool {
	term, prog := os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")
	return prog == "ghostty" || term == "xterm-ghostty" ||
		term == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
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

// Transmit returns the escape sequence that uploads img as image id and
// creates a virtual placement spanning cols x rows cells.
func Transmit(img image.Image, id, cols, rows int) (string, error) {
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

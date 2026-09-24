package thumbnail

import (
	"image"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestPlaceholderGeometry(t *testing.T) {
	tests := []struct {
		name       string
		cols, rows int
	}{
		{name: "single cell", cols: 1, rows: 1},
		{name: "thumbnail box", cols: 16, rows: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Placeholder(42, tt.cols, tt.rows)

			// The TUI lays placeholders out with lipgloss, so its width math must
			// treat each placeholder cluster as exactly one cell.
			if w := lipgloss.Width(got); w != tt.cols {
				t.Errorf("Placeholder() width = %d, want %d", w, tt.cols)
			}
			if h := lipgloss.Height(got); h != tt.rows {
				t.Errorf("Placeholder() height = %d, want %d", h, tt.rows)
			}
			if !strings.Contains(got, "\x1b[38;5;42m") {
				t.Errorf("Placeholder() = %q, want image id in 256-colour foreground", got)
			}
		})
	}
}

func TestTransmit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got, err := Transmit(img, 42, 16, 5)
	if err != nil {
		t.Fatalf("Transmit() error = %v", err)
	}
	for _, want := range []string{"\x1b_G", "i=42", "U=1", "c=16", "r=5", "f=100", "q=2", "a=T"} {
		if !strings.Contains(got, want) {
			t.Errorf("Transmit() = %q, missing %q", got, want)
		}
	}
}

func TestDelete(t *testing.T) {
	if got, want := Delete(42), "\x1b_Ga=d,d=I,i=42,q=2\x1b\\"; got != want {
		t.Errorf("Delete() = %q, want %q", got, want)
	}
}

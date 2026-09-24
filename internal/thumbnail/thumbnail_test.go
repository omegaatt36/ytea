package thumbnail

import (
	"image"
	"image/color"
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

func TestTransmitVirtual(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got, err := TransmitVirtual(img, 42, 16, 5)
	if err != nil {
		t.Fatalf("TransmitVirtual() error = %v", err)
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

func TestQueryProbesBeforeDA1(t *testing.T) {
	got := Query()
	plain := strings.Index(got, "\x1b_Gi=1,")
	placeholder := strings.Index(got, "\x1b_Gi=2,")
	da1 := strings.Index(got, "\x1b[c")
	if plain < 0 || placeholder < 0 || da1 < 0 || plain > placeholder || placeholder > da1 {
		t.Fatalf("Query() = %q, want plain probe, placeholder probe, then DA1", got)
	}
	if strings.Contains(got[plain:placeholder], "U=1") {
		t.Errorf("plain probe %q must not ask for placeholders", got[plain:placeholder])
	}
	if !strings.Contains(got[placeholder:da1], "U=1") {
		t.Errorf("placeholder probe %q missing U=1", got[placeholder:da1])
	}
}

func TestTransmitDoesNotDisplay(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got, err := Transmit(img, 42)
	if err != nil {
		t.Fatalf("Transmit() error = %v", err)
	}
	for _, unwanted := range []string{"a=T", "U=1"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Transmit() = %q, must not contain %q", got, unwanted)
		}
	}
	if !strings.Contains(got, "i=42") {
		t.Errorf("Transmit() = %q, missing image id", got)
	}
}

func TestPut(t *testing.T) {
	got := Put(42, 1, 30, 18, 5)
	want := "\x1b7\x1b[31;2H\x1b_Ga=p,i=42,p=1,c=18,r=5,C=1,q=2\x1b\\\x1b8"
	if got != want {
		t.Errorf("Put() = %q, want %q", got, want)
	}
}

func TestHalfBlocks(t *testing.T) {
	// Top half red, bottom half blue: every cell should be red over blue.
	img := image.NewRGBA(image.Rect(0, 0, 32, 20))
	for y := range 20 {
		for x := range 32 {
			c := color.RGBA{R: 255, A: 255}
			if y >= 10 {
				c = color.RGBA{B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}

	got := HalfBlocks(img, 8, 1)
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w != 8 || h != 1 {
		t.Errorf("HalfBlocks() size = %dx%d, want 8x1", w, h)
	}
	if want := "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀"; !strings.HasPrefix(got, want) {
		t.Errorf("HalfBlocks() = %q, want cells starting %q", got, want)
	}
}

func TestDownscaleAverages(t *testing.T) {
	// Alternating black/white columns average to mid grey, not to either extreme.
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := range 2 {
		for x := range 4 {
			if x%2 == 0 {
				img.Set(x, y, color.White)
			} else {
				img.Set(x, y, color.Black)
			}
		}
	}
	got := downscale(img, 1, 1)[0][0]
	if got != (rgb{127, 127, 127}) {
		t.Errorf("downscale() = %+v, want mid grey", got)
	}
}

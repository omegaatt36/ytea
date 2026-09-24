package spectrum

import (
	"math"
	"testing"
)

const rate = 48000

func sine(freq, amp float64) []float64 {
	s := make([]float64, Size)
	for i := range s {
		s[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/rate)
	}
	return s
}

// bandOf returns the band index containing freq.
func bandOf(t *testing.T, edges []int, freq float64) int {
	t.Helper()
	bin := int(math.Round(freq * Size / rate))
	for b := 0; b < len(edges)-1; b++ {
		if bin >= edges[b] && bin < edges[b+1] {
			return b
		}
	}
	t.Fatalf("frequency %v outside all bands", freq)
	return -1
}

func TestProcessPeaksAtToneBand(t *testing.T) {
	tests := []struct {
		name string
		freq float64
	}{
		{name: "bass", freq: 100},
		{name: "mid", freq: 1000},
		{name: "treble", freq: 8000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New(32, rate)
			levels := a.Process(sine(tt.freq, 1))

			want := bandOf(t, a.edges, tt.freq)
			peak := 0
			for b, l := range levels {
				if l > levels[peak] {
					peak = b
				}
			}
			if peak != want {
				t.Errorf("peak band = %d, want %d (levels %.2f)", peak, want, levels)
			}
			if levels[want] < 0.9 {
				t.Errorf("full-scale tone level = %.2f, want >= 0.9", levels[want])
			}
		})
	}
}

func TestProcessSilenceDecays(t *testing.T) {
	a := New(16, rate)
	a.Process(sine(1000, 1))
	b := bandOf(t, a.edges, 1000)
	loud := a.levels[b]

	silence := make([]float64, Size)
	quiet := a.Process(silence)[b]
	if quiet >= loud || quiet == 0 {
		t.Errorf("after silence level = %.2f, want between 0 and %.2f", quiet, loud)
	}
}

func TestBandEdgesStrictlyIncreasing(t *testing.T) {
	for _, bands := range []int{8, 64, 200} {
		edges := bandEdges(bands, rate)
		if len(edges) != bands+1 {
			t.Fatalf("bandEdges(%d) len = %d, want %d", bands, len(edges), bands+1)
		}
		for i := 1; i < len(edges); i++ {
			if edges[i] <= edges[i-1] || edges[i] > Size/2 {
				t.Fatalf("bandEdges(%d) = %v, not strictly increasing within Nyquist", bands, edges)
			}
		}
	}
}

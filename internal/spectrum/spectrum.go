// Package spectrum turns PCM samples into log-spaced frequency band levels.
package spectrum

import (
	"math"
	"math/cmplx"
)

// Size is the FFT window length: ~43ms at 48kHz, enough resolution for bass
// bands while still reacting within a frame at 30fps.
const Size = 2048

const (
	minFreq = 40.0
	maxFreq = 16000.0
	// floorDB maps to an empty bar; quieter content is not worth drawing.
	floorDB = -60.0
	decay   = 0.8
)

// Analyzer computes smoothed band levels in [0, 1].
type Analyzer struct {
	window []float64
	edges  []int
	levels []float64
	buf    []complex128
}

// New returns an Analyzer producing bands levels for audio at sampleRate.
func New(bands, sampleRate int) *Analyzer {
	window := make([]float64, Size)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(Size-1))
	}
	return &Analyzer{
		window: window,
		edges:  bandEdges(bands, sampleRate),
		levels: make([]float64, bands),
		buf:    make([]complex128, Size),
	}
}

// Process consumes exactly Size mono samples in [-1, 1] and returns the band
// levels. The returned slice is reused across calls.
func (a *Analyzer) Process(samples []float64) []float64 {
	for i := range a.buf {
		var s float64
		if i < len(samples) {
			s = samples[i]
		}
		a.buf[i] = complex(s*a.window[i], 0)
	}
	fft(a.buf)

	// A full-scale sine under a Hann window peaks at Size/4.
	const ref = Size / 4
	for b := range a.levels {
		var peak float64
		for k := a.edges[b]; k < a.edges[b+1]; k++ {
			peak = max(peak, cmplx.Abs(a.buf[k]))
		}
		level := 0.0
		if peak > 0 {
			db := 20 * math.Log10(peak/ref)
			level = min(max((db-floorDB)/-floorDB, 0), 1)
		}
		// Fast attack, slow release: keeps bars readable instead of flickering.
		if level >= a.levels[b] {
			a.levels[b] = level
		} else {
			a.levels[b] = a.levels[b]*decay + level*(1-decay)
		}
	}
	return a.levels
}

// bandEdges returns bands+1 FFT bin boundaries spaced logarithmically, each
// band at least one bin wide.
func bandEdges(bands, sampleRate int) []int {
	top := min(maxFreq, float64(sampleRate)/2)
	binHz := float64(sampleRate) / Size
	edges := make([]int, bands+1)
	for i := range edges {
		f := minFreq * math.Pow(top/minFreq, float64(i)/float64(bands))
		edges[i] = int(math.Round(f / binHz))
	}
	for i := 1; i < len(edges); i++ {
		edges[i] = max(edges[i], edges[i-1]+1)
	}
	edges[len(edges)-1] = min(edges[len(edges)-1], Size/2)
	return edges
}

// fft is an in-place iterative radix-2 Cooley-Tukey transform; len(x) must be a power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := range size / 2 {
				u := x[start+k]
				v := x[start+k+size/2] * w
				x[start+k] = u + v
				x[start+k+size/2] = u - v
				w *= step
			}
		}
	}
}

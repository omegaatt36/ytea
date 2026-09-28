package spectrum

import (
	"context"
	"sync"
	"time"
)

const frameRate = 30

// Meter buffers live mono samples and publishes band levels at a steady frame
// rate, independent of how the samples are captured.
type Meter struct {
	bands      int
	sampleRate int
	levels     chan []float64

	mu    sync.Mutex
	ring  []float64
	fresh bool
}

// NewMeter returns a Meter for bands levels of audio at sampleRate.
func NewMeter(bands, sampleRate int) *Meter {
	return &Meter{
		bands:      bands,
		sampleRate: sampleRate,
		levels:     make(chan []float64, 1),
		ring:       make([]float64, Size),
	}
}

// Levels delivers band levels in [0, 1] at up to 30fps.
func (m *Meter) Levels() <-chan []float64 {
	return m.levels
}

// Push appends mono samples in [-1, 1], keeping the latest Size.
func (m *Meter) Push(samples []float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := copy(m.ring, m.ring[min(len(samples), Size):])
	copy(m.ring[n:], samples[max(len(samples)-Size, 0):])
	m.fresh = true
}

// Run publishes levels until ctx is cancelled.
func (m *Meter) Run(ctx context.Context) {
	analyzer := New(m.bands, m.sampleRate)
	window := make([]float64, Size)
	ticker := time.NewTicker(time.Second / frameRate)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		m.mu.Lock()
		if m.fresh {
			copy(window, m.ring)
		} else {
			// No samples (paused or stream gone): feed silence so bars fall instead of freezing.
			clear(window)
		}
		m.fresh = false
		m.mu.Unlock()

		levels := append([]float64(nil), analyzer.Process(window)...)
		// Drop stale frames rather than block when the UI is behind.
		select {
		case <-m.levels:
		default:
		}
		m.levels <- levels
	}
}

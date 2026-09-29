package spectrum

import (
	"context"
	"math"
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
	vu         chan [2]float64

	mu       sync.Mutex
	ring     []float64
	fresh    bool
	vuSum    [2]float64
	vuFrames int
}

// NewMeter returns a Meter for bands levels of audio at sampleRate.
func NewMeter(bands, sampleRate int) *Meter {
	return &Meter{
		bands:      bands,
		sampleRate: sampleRate,
		levels:     make(chan []float64, 1),
		vu:         make(chan [2]float64, 1),
		ring:       make([]float64, Size),
	}
}

// Levels delivers band levels in [0, 1] at up to 30fps.
func (m *Meter) Levels() <-chan []float64 {
	return m.levels
}

// VU delivers smoothed left and right RMS levels in [0, 1] at up to 30fps.
func (m *Meter) VU() <-chan [2]float64 { return m.vu }

// Push appends mono samples in [-1, 1], keeping the latest Size.
func (m *Meter) Push(samples []float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushMono(samples)
}

func (m *Meter) pushMono(samples []float64) {
	n := copy(m.ring, m.ring[min(len(samples), Size):])
	copy(m.ring[n:], samples[max(len(samples)-Size, 0):])
	m.fresh = true
}

// PushStereo appends interleaved left/right samples and measures each channel.
// The spectrum continues to use a mono downmix.
func (m *Meter) PushStereo(interleaved []float64) {
	frames := len(interleaved) / 2
	if frames == 0 {
		return
	}
	mono := make([]float64, frames)
	var sum [2]float64
	for i := range mono {
		left, right := interleaved[2*i], interleaved[2*i+1]
		mono[i] = (left + right) / 2
		sum[0] += left * left
		sum[1] += right * right
	}
	m.mu.Lock()
	m.pushMono(mono)
	m.vuSum[0] += sum[0]
	m.vuSum[1] += sum[1]
	m.vuFrames += frames
	m.mu.Unlock()
}

// Run publishes levels until ctx is cancelled.
func (m *Meter) Run(ctx context.Context) {
	analyzer := New(m.bands, m.sampleRate)
	window := make([]float64, Size)
	var vu [2]float64
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
		var target [2]float64
		if m.vuFrames > 0 {
			for ch := range target {
				target[ch] = min(1, math.Sqrt(m.vuSum[ch]/float64(m.vuFrames)))
			}
		}
		clear(m.vuSum[:])
		m.vuFrames = 0
		m.mu.Unlock()
		for ch := range vu {
			factor := 0.18
			if target[ch] > vu[ch] {
				factor = 0.6
			}
			vu[ch] += (target[ch] - vu[ch]) * factor
		}

		levels := append([]float64(nil), analyzer.Process(window)...)
		// Drop stale frames rather than block when the UI is behind.
		select {
		case <-m.levels:
		default:
		}
		m.levels <- levels
		select {
		case <-m.vu:
		default:
		}
		m.vu <- vu
	}
}

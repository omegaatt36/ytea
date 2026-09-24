package pipewire

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/omegaatt36/ytea/internal/spectrum"
)

const (
	tapRate     = 48000
	frameRate   = 30
	rescanEvery = time.Second
)

// Tap records a playback stream with pw-cat and publishes spectrum levels.
// It follows the stream across reconnects by re-resolving its serial.
type Tap struct {
	node   string
	bands  int
	levels chan []float64

	mu    sync.Mutex
	ring  []float64
	fresh bool
}

// NewTap returns a Tap for the output stream whose node.name is node.
func NewTap(node string, bands int) *Tap {
	return &Tap{
		node:   node,
		bands:  bands,
		levels: make(chan []float64, 1),
		ring:   make([]float64, spectrum.Size),
	}
}

// Levels delivers band levels in [0, 1] at up to 30fps.
func (t *Tap) Levels() <-chan []float64 {
	return t.levels
}

// Run supervises pw-cat until ctx is cancelled.
func (t *Tap) Run(ctx context.Context) {
	go t.publish(ctx)

	var rec *recording
	defer func() { rec.stop() }()

	ticker := time.NewTicker(rescanEvery)
	defer ticker.Stop()
	for {
		serial, err := t.resolve(ctx)
		if err != nil && !errors.Is(err, ErrNodeNotFound) {
			slog.WarnContext(ctx, "resolve playback stream", "node", t.node, "error", err)
		}
		if serial != rec.target() {
			rec.stop()
			rec = nil
			if serial != 0 {
				rec = t.record(ctx, serial)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-rec.done():
			// pw-cat died (e.g. its target vanished); restart on the next scan.
			rec.stop()
			rec = nil
		case <-ticker.C:
		}
	}
}

// recording is one pw-cat process bound to a stream serial.
type recording struct {
	serial int
	cancel context.CancelFunc
	exited chan struct{}
}

// The nil-receiver methods let Run treat "no recording" as an ordinary value.

func (r *recording) target() int {
	if r == nil {
		return 0
	}
	return r.serial
}

func (r *recording) done() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.exited
}

func (r *recording) stop() {
	if r != nil {
		r.cancel()
	}
}

func (t *Tap) resolve(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	g, err := Dump(ctx)
	if err != nil {
		return 0, err
	}
	return g.StreamSerial(t.node)
}

// record starts pw-cat capturing the stream with the given serial.
func (t *Tap) record(ctx context.Context, serial int) *recording {
	ctx, cancel := context.WithCancel(ctx)
	rec := &recording{serial: serial, cancel: cancel, exited: make(chan struct{})}

	cmd := exec.CommandContext(ctx, "pw-cat", "--record", "--raw",
		"--target", strconv.Itoa(serial),
		"--format", "f32", "--rate", strconv.Itoa(tapRate), "--channels", "2",
		"--latency", "20ms",
		"-P", `{"node.name":"ytea-tap","node.dont-reconnect":true}`,
		"-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.WarnContext(ctx, "pipe pw-cat", "error", err)
		close(rec.exited)
		return rec
	}
	if err := cmd.Start(); err != nil {
		slog.WarnContext(ctx, "start pw-cat", "error", err)
		close(rec.exited)
		return rec
	}

	go func() {
		defer close(rec.exited)
		t.consume(stdout)
		_ = cmd.Wait()
	}()
	return rec
}

// consume reads interleaved stereo float32 frames into the mono ring buffer.
func (t *Tap) consume(r io.Reader) {
	br := bufio.NewReaderSize(r, 16*1024)
	frame := make([]byte, 8)
	const batch = 256
	chunk := make([]float64, 0, batch)
	for {
		if _, err := io.ReadFull(br, frame); err != nil {
			return
		}
		l := math.Float32frombits(binary.LittleEndian.Uint32(frame[0:4]))
		rr := math.Float32frombits(binary.LittleEndian.Uint32(frame[4:8]))
		chunk = append(chunk, float64(l+rr)/2)
		if len(chunk) == batch {
			t.push(chunk)
			chunk = chunk[:0]
		}
	}
}

func (t *Tap) push(samples []float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := copy(t.ring, t.ring[len(samples):])
	copy(t.ring[n:], samples)
	t.fresh = true
}

func (t *Tap) publish(ctx context.Context) {
	analyzer := spectrum.New(t.bands, tapRate)
	window := make([]float64, spectrum.Size)
	ticker := time.NewTicker(time.Second / frameRate)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		t.mu.Lock()
		if t.fresh {
			copy(window, t.ring)
		} else {
			// No samples (paused or stream gone): feed silence so bars fall instead of freezing.
			clear(window)
		}
		t.fresh = false
		t.mu.Unlock()

		levels := append([]float64(nil), analyzer.Process(window)...)
		// Drop stale frames rather than block when the UI is behind.
		select {
		case <-t.levels:
		default:
		}
		t.levels <- levels
	}
}

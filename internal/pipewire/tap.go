// Package pipewire records ytea's mpv stream with pw-cat for the spectrum
// visualizer. Capture is PipeWire-specific, so the package is unused elsewhere.
package pipewire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"time"

	"github.com/omegaatt36/ytea/internal/spectrum"
)

const (
	tapRate     = 48000
	rescanEvery = time.Second
)

// Tap records a playback stream with pw-cat and publishes spectrum levels.
// It follows the stream across reconnects by re-resolving its serial.
type Tap struct {
	node  string
	meter *spectrum.Meter
}

// NewTap returns a Tap for the output stream whose node.name is node.
func NewTap(node string, bands int) *Tap {
	return &Tap{node: node, meter: spectrum.NewMeter(bands, tapRate)}
}

// Levels delivers band levels in [0, 1] at up to 30fps.
func (t *Tap) Levels() <-chan []float64 {
	return t.meter.Levels()
}

// Run supervises pw-cat until ctx is cancelled.
func (t *Tap) Run(ctx context.Context) {
	go t.meter.Run(ctx)

	var rec *recording
	defer func() { rec.stop() }()

	ticker := time.NewTicker(rescanEvery)
	defer ticker.Stop()
	for {
		serial, err := t.streamSerial(ctx)
		if err != nil && !errors.Is(err, errNodeNotFound) {
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

// errNodeNotFound is returned when no pw-dump object matches the requested stream.
var errNodeNotFound = errors.New("pipewire stream not found")

// streamSerial resolves the object.serial of the output stream whose node.name
// is the tap's node; pw-cat --target takes serials. It re-resolves on every
// scan because the serial changes whenever mpv reopens its audio output
// (device switch, format change).
func (t *Tap) streamSerial(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "pw-dump")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("run pw-dump: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	var objects []dumpObject
	if err := json.Unmarshal(stdout.Bytes(), &objects); err != nil {
		return 0, fmt.Errorf("decode pw-dump output: %w", err)
	}
	return findStreamSerial(objects, t.node)
}

// dumpObject is one entry of pw-dump's JSON output; only nodes are of interest.
type dumpObject struct {
	ID   int         `json:"id"`
	Type string      `json:"type"`
	Info *objectInfo `json:"info"`
}

type objectInfo struct {
	Props map[string]any `json:"props"`
}

const typeNode = "PipeWire:Interface:Node"

// findStreamSerial returns the object.serial of the output stream whose
// node.name is node.
func findStreamSerial(objects []dumpObject, node string) (int, error) {
	for _, o := range objects {
		if o.Type != typeNode || o.Info == nil {
			continue
		}
		props := o.Info.Props
		if propString(props, "media.class") != "Stream/Output/Audio" || propString(props, "node.name") != node {
			continue
		}
		if serial, ok := props["object.serial"].(float64); ok {
			return int(serial), nil
		}
	}
	return 0, fmt.Errorf("find stream %q: %w", node, errNodeNotFound)
}

func propString(props map[string]any, key string) string {
	s, _ := props[key].(string)
	return s
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

// consume downmixes interleaved stereo float32 frames into the meter.
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
			t.meter.Push(chunk)
			chunk = chunk[:0]
		}
	}
}

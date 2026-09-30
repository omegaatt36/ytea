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
	tapRate    = 48000
	retryEvery = time.Second
)

// Tap records a playback stream with pw-cat and publishes spectrum levels.
// It follows the stream across reconnects by watching the PipeWire graph.
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

// VU delivers left and right RMS levels in [0, 1] at up to 30fps.
func (t *Tap) VU() <-chan [2]float64 { return t.meter.VU() }

// Run supervises pw-cat until ctx is cancelled.
func (t *Tap) Run(ctx context.Context) {
	go t.meter.Run(ctx)
	serials := make(chan int)
	go t.watch(ctx, serials)

	var rec *recording
	defer func() { rec.stop() }()
	var serial int
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case serial = <-serials:
		case <-retry:
		case <-rec.done():
			rec.stop()
			rec = nil
			retry = time.After(retryEvery)
			continue
		}
		if serial != rec.target() {
			rec.stop()
			rec = nil
			if serial != 0 {
				rec = t.record(ctx, serial)
			}
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

// watch sends the tap's stream serial each time it changes, restarting
// pw-dump whenever it exits.
func (t *Tap) watch(ctx context.Context, serials chan<- int) {
	var last int
	report := func(serial int) {
		if serial == last {
			return
		}
		last = serial
		select {
		case serials <- serial:
		case <-ctx.Done():
		}
	}
	for {
		err := t.monitor(ctx, report)
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "monitor pipewire graph", "node", t.node, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryEvery):
		}
	}
}

// monitor runs pw-dump --monitor until it exits and returns why it did.
func (t *Tap) monitor(ctx context.Context, report func(int)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "pw-dump", "--monitor")
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe pw-dump: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pw-dump: %w", err)
	}
	followErr := followStreams(stdout, t.node, report)
	cancel()
	waitErr := cmd.Wait()
	switch {
	case followErr != nil:
		return followErr
	case waitErr != nil:
		return fmt.Errorf("pw-dump exited: %w: %s", waitErr, bytes.TrimSpace(stderr.Bytes()))
	default:
		return errors.New("pw-dump exited")
	}
}

// followStreams decodes pw-dump --monitor output, a full dump followed by one
// JSON array per change, and reports the matching stream's serial (0 if none)
// after each array. pw-cat --target takes serials, and mpv gets a new one
// whenever it reopens its audio output (device switch, format change).
func followStreams(r io.Reader, node string, report func(int)) error {
	dec := json.NewDecoder(r)
	serials := map[int]int{}
	for {
		var objects []dumpObject
		if err := dec.Decode(&objects); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode pw-dump output: %w", err)
		}
		for _, o := range objects {
			if serial, ok := streamSerial(o, node); ok {
				serials[o.ID] = serial
			} else {
				delete(serials, o.ID)
			}
		}
		var newest int
		for _, serial := range serials {
			newest = max(newest, serial)
		}
		report(newest)
	}
}

// dumpObject is one entry of pw-dump's JSON output; removed objects arrive
// with a null info.
type dumpObject struct {
	ID   int         `json:"id"`
	Type string      `json:"type"`
	Info *objectInfo `json:"info"`
}

type objectInfo struct {
	Props map[string]any `json:"props"`
}

const typeNode = "PipeWire:Interface:Node"

func streamSerial(o dumpObject, node string) (int, bool) {
	if o.Type != typeNode || o.Info == nil {
		return 0, false
	}
	props := o.Info.Props
	if propString(props, "media.class") != "Stream/Output/Audio" || propString(props, "node.name") != node {
		return 0, false
	}
	serial, ok := props["object.serial"].(float64)
	return int(serial), ok
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

// consume feeds interleaved stereo float32 frames into the meter.
func (t *Tap) consume(r io.Reader) {
	br := bufio.NewReaderSize(r, 16*1024)
	frame := make([]byte, 8)
	const batch = 256
	chunk := make([]float64, 0, 2*batch)
	for {
		if _, err := io.ReadFull(br, frame); err != nil {
			return
		}
		l := math.Float32frombits(binary.LittleEndian.Uint32(frame[0:4]))
		rr := math.Float32frombits(binary.LittleEndian.Uint32(frame[4:8]))
		chunk = append(chunk, float64(l), float64(rr))
		if len(chunk) == 2*batch {
			t.meter.PushStereo(chunk)
			chunk = chunk[:0]
		}
	}
}

// Package audiotee records mpv's audio with the audiotee CLI, which wraps the
// Core Audio process taps of macOS 14.2+, for the spectrum visualizer.
package audiotee

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
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/omegaatt36/ytea/internal/spectrum"
)

const (
	// Requesting a rate makes audiotee resample, which always yields s16le.
	tapRate    = 48000
	retryEvery = time.Second
)

// Tap records one process with audiotee and publishes spectrum levels.
type Tap struct {
	pid     int
	meter   *spectrum.Meter
	mu      sync.Mutex
	device  string
	changed chan struct{}
	cancel  context.CancelFunc
	errors  chan error
	lastErr string
}

// NewTap returns a Tap for the audio output of process pid.
func NewTap(pid, bands int) *Tap {
	return &Tap{pid: pid, meter: spectrum.NewMeter(bands, tapRate), changed: make(chan struct{}), errors: make(chan error, 1)}
}

// Levels delivers band levels in [0, 1] at up to 30fps.
func (t *Tap) Levels() <-chan []float64 {
	return t.meter.Levels()
}

// VU delivers left and right RMS levels in [0, 1] at up to 30fps.
func (t *Tap) VU() <-chan [2]float64 { return t.meter.VU() }

func (t *Tap) Errors() <-chan error { return t.errors }

// SetAudioDevice pauses capture when mpv is routed away from the system default.
func (t *Tap) SetAudioDevice(device string) error {
	t.mu.Lock()
	if device != t.device {
		t.device = device
		close(t.changed)
		t.changed = make(chan struct{})
		if t.cancel != nil {
			t.cancel()
		}
	}
	t.mu.Unlock()
	if device != "" && device != "auto" {
		return errors.New("spectrum only supports the system default output")
	}
	return nil
}

func (t *Tap) report(err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	if message == t.lastErr {
		return
	}
	t.lastErr = message
	select {
	case <-t.errors:
	default:
	}
	t.errors <- err
}

// Run supervises audiotee until ctx is cancelled.
func (t *Tap) Run(ctx context.Context) {
	go t.meter.Run(ctx)
	for {
		t.mu.Lock()
		device, changed := t.device, t.changed
		recordCtx, cancel := context.WithCancel(ctx)
		if device == "" || device == "auto" {
			t.cancel = cancel
		}
		t.mu.Unlock()
		if device != "" && device != "auto" {
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-changed:
				continue
			}
		}
		err := t.record(recordCtx)
		wasCanceled := recordCtx.Err() != nil
		cancel()
		t.mu.Lock()
		t.cancel = nil
		t.mu.Unlock()
		if err != nil && !errors.Is(err, errNotPlaying) && ctx.Err() == nil && !wasCanceled {
			slog.WarnContext(ctx, "record with audiotee", "pid", t.pid, "error", err)
			t.report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
			continue
		case <-time.After(retryEvery):
		}
	}
}

// errNotPlaying is returned while the process has no audio output yet, e.g.
// mpv idling before the first track.
var errNotPlaying = errors.New("process is not playing audio")

// record runs audiotee until it exits and returns why it did.
func (t *Tap) record(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stderr := newStderrLog()
	cmd := exec.CommandContext(ctx, "audiotee",
		"--include-processes", strconv.Itoa(t.pid),
		"--stereo",
		"--sample-rate", strconv.Itoa(tapRate),
		"--chunk-duration", "0.02",
	)
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe audiotee: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start audiotee: %w", err)
	}
	consumeErr := t.consume(ctx, stdout, stderr.ready)
	if consumeErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if consumeErr != nil {
		return consumeErr
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := lastError(stderr.Bytes()); err != nil {
		return err
	}
	if waitErr != nil {
		return fmt.Errorf("audiotee exited: %w", waitErr)
	}
	return nil
}

// consume feeds interleaved stereo s16le samples into the meter.
func (t *Tap) consume(ctx context.Context, r io.Reader, format <-chan error) error {
	br := bufio.NewReaderSize(r, 16*1024)
	const batch = 256
	raw := make([]byte, 4*batch)
	chunk := make([]float64, 2*batch)
	checked := false
	reported := false
	for {
		if _, err := io.ReadFull(br, raw); err != nil {
			return nil
		}
		if !checked {
			select {
			case err := <-format:
				if err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				return errors.New("audiotee did not report its PCM format")
			}
			checked = true
		}
		for i := range chunk {
			chunk[i] = float64(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
		}
		t.meter.PushStereo(chunk)
		if !reported {
			t.report(nil)
			reported = true
		}
	}
}

// message is one JSON line of audiotee's stderr log.
type message struct {
	Type string `json:"message_type"`
	Data struct {
		Message          string `json:"message"`
		SampleRate       int    `json:"sample_rate"`
		ChannelsPerFrame int    `json:"channels_per_frame"`
		IsFloat          bool   `json:"is_float"`
		Encoding         string `json:"encoding"`
	} `json:"data"`
}

func (m message) formatError() error {
	if m.Data.SampleRate == tapRate && m.Data.ChannelsPerFrame == 2 && !m.Data.IsFloat && m.Data.Encoding == "pcm_s16le" {
		return nil
	}
	return fmt.Errorf("unsupported audiotee PCM format: %s, %d Hz, %d channels, float=%t", m.Data.Encoding, m.Data.SampleRate, m.Data.ChannelsPerFrame, m.Data.IsFloat)
}

// stderrLog keeps diagnostics while notifying the PCM reader as soon as
// audiotee declares its output format.
type stderrLog struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	pending []byte
	ready   chan error
	seen    bool
}

func newStderrLog() *stderrLog { return &stderrLog{ready: make(chan error, 1)} }

func (l *stderrLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, _ := l.buf.Write(p)
	l.pending = append(l.pending, p...)
	for {
		end := bytes.IndexByte(l.pending, '\n')
		if end < 0 {
			break
		}
		var m message
		if json.Unmarshal(l.pending[:end], &m) == nil && m.Type == "metadata" && !l.seen {
			l.seen = true
			l.ready <- m.formatError()
		}
		l.pending = l.pending[end+1:]
	}
	return n, nil
}

func (l *stderrLog) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.buf.Bytes())
}

const pidTranslationFailed = "Failed to translate process IDs to audio objects"

// lastError extracts the last error audiotee logged to stderr.
func lastError(stderr []byte) error {
	var last string
	var formatErr error
	for line := range bytes.Lines(stderr) {
		var m message
		if json.Unmarshal(line, &m) == nil {
			switch m.Type {
			case "error":
				last = m.Data.Message
			case "metadata":
				formatErr = m.formatError()
			}
		}
	}
	switch last {
	case "":
		return formatErr
	case pidTranslationFailed:
		return errNotPlaying
	default:
		return errors.New(last)
	}
}

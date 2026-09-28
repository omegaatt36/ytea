package audiotee

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Trimmed from real audiotee stderr.
const (
	startedLog = `{"message_type":"info","data":{"message":"Starting AudioTee..."}}
{"message_type":"metadata","data":{"sample_rate":48000,"channels_per_frame":1,"is_float":false,"encoding":"pcm_s16le"}}
{"message_type":"stream_start"}
`
	notPlayingLog = `{"message_type":"info","data":{"message":"Starting AudioTee..."}}
{"message_type":"error","data":{"message":"Failed to translate process IDs to audio objects","context":{"failed_pids":"27035"}}}
Error: failure
`
	tapFailedLog = `{"message_type":"error","data":{"message":"Failed to create audio tap","context":{"status":"-1"}}}
{"message_type":"error","data":{"message":"Failed to setup audio tap"}}
Error: failure
`
	wrongFormatLog = `{"message_type":"metadata","data":{"sample_rate":44100,"channels_per_frame":2,"is_float":true,"encoding":"pcm_f32le"}}
{"message_type":"stream_start"}
`
	contradictoryFormatLog = `{"message_type":"metadata","data":{"sample_rate":48000,"channels_per_frame":1,"is_float":true,"encoding":"pcm_s16le"}}
`
)

func TestLastError(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		wantErr error
		wantMsg string
	}{
		{name: "clean run", stderr: startedLog},
		{name: "process not playing", stderr: notPlayingLog, wantErr: errNotPlaying},
		{name: "last error wins", stderr: tapFailedLog, wantMsg: "Failed to setup audio tap"},
		{name: "unexpected PCM format", stderr: wrongFormatLog, wantMsg: "unsupported audiotee PCM format: pcm_f32le, 44100 Hz, 2 channels, float=true"},
		{name: "contradictory float flag", stderr: contradictoryFormatLog, wantMsg: "unsupported audiotee PCM format: pcm_s16le, 48000 Hz, 1 channels, float=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := lastError([]byte(tt.stderr))
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("lastError() = %v, want %v", err, tt.wantErr)
				}
			case tt.wantMsg != "":
				if err == nil || err.Error() != tt.wantMsg {
					t.Fatalf("lastError() = %v, want %q", err, tt.wantMsg)
				}
			case err != nil:
				t.Fatalf("lastError() = %v, want nil", err)
			}
		})
	}
}

func TestRecordRejectsUnexpectedPCM(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "audiotee")
	program := "#!/bin/sh\n" +
		"printf '%s\\n' '" + strings.TrimSpace(wrongFormatLog[:strings.IndexByte(wrongFormatLog, '\n')]) + "' >&2\n" +
		"printf '\\001\\000\\001\\000'\n"
	if err := os.WriteFile(bin, []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tap := NewTap(1234, 16)
	err := tap.record(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported audiotee PCM format") {
		t.Fatalf("record() error = %v, want unsupported PCM format", err)
	}
}

func TestRecordRejectsUnexpectedPCMBeforeProcessExits(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "audiotee")
	metadata := strings.TrimSpace(wrongFormatLog[:strings.IndexByte(wrongFormatLog, '\n')])
	program := "#!/bin/sh\n" +
		"printf '%s\\n' '" + metadata + "' >&2\n" +
		"dd if=/dev/zero bs=512 count=1 2>/dev/null\n" +
		"exec sleep 5\n"
	if err := os.WriteFile(bin, []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := NewTap(1234, 16).record(ctx)
	if err == nil || !strings.Contains(err.Error(), "unsupported audiotee PCM format") {
		t.Fatalf("record() = %v, want format rejection before helper exits", err)
	}
}

func TestTapWaitsForDefaultOutput(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	bin := filepath.Join(dir, "audiotee")
	program := "#!/bin/sh\n" +
		"touch '" + marker + "'\n" +
		"printf '%s\\n' '{\"message_type\":\"metadata\",\"data\":{\"sample_rate\":48000,\"channels_per_frame\":1,\"is_float\":false,\"encoding\":\"pcm_s16le\"}}' >&2\n" +
		"exec sleep 5\n"
	if err := os.WriteFile(bin, []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tap := NewTap(1234, 16)
	if err := tap.SetAudioDevice("coreaudio/other"); err == nil {
		t.Fatal("explicit output did not disable capture")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tap.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("audiotee started on an explicit output")
	}
	if err := tap.SetAudioDevice("auto"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("audiotee did not start after selecting default output")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

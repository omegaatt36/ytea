package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// mpv403 is what mpv printed when googlevideo refused a stream URL.
const mpv403 = `[ffmpeg] https: HTTP error 403 Forbidden
Failed to open https://rr1---sn-ipoxu-un5e6.googlevideo.com/videoplayback?expire=1&sig=abc.
EDL: Could not open source file 'https://rr1---sn-ipoxu-un5e6.googlevideo.com/videoplayback?expire=1&sig=abc'.
No video or audio streams selected.
`

type fakeRun struct {
	// outputs maps a binary and its first argument to the combined output
	// and error; a probe is keyed by "mpv --no-video".
	outputs map[string]fakeOutput
	calls   [][]string
}

type fakeOutput struct {
	out string
	err error
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.outputs[name+" "+args[0]].out, f.outputs[name+" "+args[0]].err
}

func healthyEnv() (doctorEnv, *fakeRun) {
	f := &fakeRun{outputs: map[string]fakeOutput{
		"/bin/mpv --version":    {out: "mpv v0.41.0 Copyright © 2000-2025 mpv/MPlayer/mplayer2 projects\n"},
		"/bin/yt-dlp --version": {out: "2026.08.19\n"},
	}}
	return doctorEnv{
		goos: "linux",
		lookPath: func(bin string) (string, error) {
			return "/bin/" + bin, nil
		},
		run: func(ctx context.Context, name string, args ...string) (string, error) {
			// The probe and device listing run the bare name, as mpv --version runs the resolved path.
			if name == "mpv" {
				name = "/bin/mpv"
			}
			return f.run(ctx, name, args...)
		},
	}, f
}

func testOptions() options {
	return options{mpvBin: "mpv", ytdlpBin: "yt-dlp"}
}

func runDoctor(t *testing.T, opts options, env doctorEnv) (string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	err := doctor(t.Context(), opts, env, &out)
	return out.String(), err
}

func TestDoctorHealthy(t *testing.T) {
	env, f := healthyEnv()
	out, err := runDoctor(t, testOptions(), env)
	if err != nil {
		t.Fatalf("doctor() error = %v\n%s", err, out)
	}
	for _, want := range []string{"✓ mpv         v0.41.0  /bin/mpv", "✓ yt-dlp      2026.08.19", "✓ playback", "✓ audio"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	probe := f.calls[len(f.calls)-1]
	for _, want := range []string{"--ao=null", "--ytdl-format=bestaudio/best", "--ytdl-raw-options-append=extractor-args=youtube:player_client=web_embedded,web_music", probeURL} {
		if !slices.Contains(probe, want) {
			t.Errorf("probe args %q lack %q", probe, want)
		}
	}
}

func TestDoctorPlaybackRefused(t *testing.T) {
	env, f := healthyEnv()
	f.outputs["/bin/mpv --no-video"] = fakeOutput{out: mpv403, err: &exec.ExitError{}}
	out, err := runDoctor(t, testOptions(), env)
	if err == nil || !strings.Contains(err.Error(), "1 of") {
		t.Fatalf("doctor() error = %v, want one failed check", err)
	}
	if !strings.Contains(out, "✗ playback    YouTube refused the stream URL (HTTP 403)") {
		t.Errorf("output lacks the 403 explanation:\n%s", out)
	}
	if strings.Contains(out, "googlevideo") || strings.Contains(out, "sig=") {
		t.Errorf("output leaks a stream URL:\n%s", out)
	}
}

func TestDoctorMissingTools(t *testing.T) {
	env, _ := healthyEnv()
	env.lookPath = func(bin string) (string, error) {
		if bin == "mpv" {
			return "", errors.New("not found")
		}
		return "/bin/" + bin, nil
	}
	out, err := runDoctor(t, testOptions(), env)
	if err == nil {
		t.Fatalf("doctor() succeeded without mpv:\n%s", out)
	}
	for _, want := range []string{"✗ mpv         mpv not found in PATH", "install mpv", "- playback    skipped: mpv and yt-dlp are required"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorAudioDevice(t *testing.T) {
	env, f := healthyEnv()
	f.outputs["/bin/mpv --audio-device=help"] = fakeOutput{out: "List of detected audio devices:\n  'auto' (Autoselect device)\n  'pipewire/sink' (Speakers)\n"}
	opts := testOptions()

	opts.device = "pipewire/sink"
	if out, err := runDoctor(t, opts, env); err != nil {
		t.Errorf("listed device: doctor() error = %v\n%s", err, out)
	}
	opts.device = "pipewire/gone"
	out, err := runDoctor(t, opts, env)
	if err == nil || !strings.Contains(out, `✗ audio       audio-device "pipewire/gone" is not an mpv device`) {
		t.Errorf("unlisted device: error = %v, output:\n%s", err, out)
	}
}

func TestDoctorVisualizerWarnsOnly(t *testing.T) {
	env, _ := healthyEnv()
	healthy := env.lookPath
	env.lookPath = func(bin string) (string, error) {
		if bin == "pw-cat" {
			return "", errors.New("not found")
		}
		return healthy(bin)
	}
	opts := testOptions()
	opts.visualizer = true
	out, err := runDoctor(t, opts, env)
	if err != nil {
		t.Errorf("a missing visualizer tool failed doctor: %v", err)
	}
	if !strings.Contains(out, "! visualizer") {
		t.Errorf("output lacks a visualizer warning:\n%s", out)
	}

	opts.visualizer = false
	if out, _ := runDoctor(t, opts, env); strings.Contains(out, "visualizer") {
		t.Errorf("a disabled visualizer was checked:\n%s", out)
	}
}

func TestDoctorCookiesFileMissing(t *testing.T) {
	env, _ := healthyEnv()
	opts := testOptions()
	opts.cookies = "/nonexistent/cookies.txt"
	out, err := runDoctor(t, opts, env)
	if err == nil {
		t.Fatalf("doctor() succeeded with a missing cookies file:\n%s", out)
	}
	for _, want := range []string{"✗ cookies", "- playback    skipped: cookies are unusable"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestExplainProbe(t *testing.T) {
	tests := []struct {
		name, out  string
		wantDetail string
		wantHint   string
	}{
		{name: "403", out: mpv403, wantDetail: "YouTube refused the stream URL (HTTP 403)", wantHint: "report it"},
		{name: "sign in", out: "ERROR: [youtube] abc: Sign in to confirm you're not a bot", wantDetail: "YouTube asks for a sign-in", wantHint: "cookies"},
		{name: "url dropped", out: "Failed to open https://example.com/a?sig=secret.", wantDetail: "Failed to open <url>", wantHint: "mpv.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail, hint := explainProbe(tt.out, errors.New("exit status 2"))
			if !strings.HasPrefix(detail, tt.wantDetail) {
				t.Errorf("detail = %q, want prefix %q", detail, tt.wantDetail)
			}
			if !strings.Contains(hint, tt.wantHint) {
				t.Errorf("hint = %q, want it to contain %q", hint, tt.wantHint)
			}
		})
	}
}

func TestExplainProbeCutsLongLine(t *testing.T) {
	detail, _ := explainProbe("[ytdl_hook] ERROR: "+strings.Repeat("x", 400), errors.New("exit status 2"))
	if n := len([]rune(detail)); n != detailRunes+1 {
		t.Errorf("detail has %d runes, want %d", n, detailRunes+1)
	}
}

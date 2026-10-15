package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/omegaatt36/ytea/internal/mpv"
)

const (
	// probeURL is an official-channel music video. Old uploads (Rick Astley, the
	// first YouTube video) play even when newer ones are refused with 403, so
	// they would pass while real tracks fail.
	probeURL     = "https://www.youtube.com/watch?v=MjBhRzS5qIM"
	probeTimeout = 45 * time.Second
	// detailRunes keeps one mpv error line on one terminal line.
	detailRunes = 160
)

type doctorStatus int

const (
	doctorOK doctorStatus = iota
	doctorWarn
	doctorFail
	doctorSkip
)

func (s doctorStatus) mark() string {
	switch s {
	case doctorOK:
		return "✓"
	case doctorWarn:
		return "!"
	case doctorFail:
		return "✗"
	case doctorSkip:
	}
	return "-"
}

type doctorResult struct {
	name, detail, hint string
	status             doctorStatus
}

// doctorEnv is what the checks need from the machine, so tests can fake it.
type doctorEnv struct {
	goos     string
	lookPath func(string) (string, error)
	// run returns the command's combined output.
	run func(ctx context.Context, name string, args ...string) (string, error)
}

func systemDoctorEnv() doctorEnv {
	return doctorEnv{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(out), err
		},
	}
}

func doctorCommand(opts *options, root *cli.Command) *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "check that playback, audio and the visualizer can work",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := loadOptions(root); err != nil {
				return err
			}
			return doctor(ctx, *opts, systemDoctorEnv(), cmd.Writer)
		},
	}
}

// doctor prints one line per check and fails when any check failed.
func doctor(ctx context.Context, opts options, env doctorEnv, w io.Writer) error {
	results := runChecks(ctx, opts, env)
	failed := 0
	for _, r := range results {
		fmt.Fprintf(w, "%s %-11s %s\n", r.status.mark(), r.name, r.detail)
		if r.hint != "" {
			fmt.Fprintf(w, "    %s\n", r.hint)
		}
		if r.status == doctorFail {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	}
	return nil
}

func runChecks(ctx context.Context, opts options, env doctorEnv) []doctorResult {
	mpvResult := checkTool(ctx, env, "mpv", opts.mpvBin, mpvVersion,
		"install mpv: https://mpv.io/installation/")
	ytdlpResult := checkTool(ctx, env, "yt-dlp", opts.ytdlpBin, strings.TrimSpace,
		"install yt-dlp: https://github.com/yt-dlp/yt-dlp/wiki/Installation")
	results := []doctorResult{mpvResult, ytdlpResult}

	cfg, cookiesResult := playbackConfig(opts)
	if cookiesResult != nil {
		results = append(results, *cookiesResult)
	}
	if note := ytdlpUserConfig(); note != nil {
		results = append(results, *note)
	}
	switch {
	case cookiesResult != nil && cookiesResult.status == doctorFail:
		results = append(results, doctorResult{name: "playback", status: doctorSkip, detail: "skipped: cookies are unusable"})
	case mpvResult.status == doctorFail || ytdlpResult.status == doctorFail:
		results = append(results, doctorResult{name: "playback", status: doctorSkip, detail: "skipped: mpv and yt-dlp are required"})
	default:
		results = append(results, checkPlayback(ctx, env, opts.mpvBin, cfg))
	}
	results = append(results, checkAudioDevice(ctx, env, opts))
	if opts.visualizer {
		results = append(results, checkVisualizer(env))
	}
	return results
}

func mpvVersion(out string) string {
	// "mpv v0.41.0 Copyright © 2000-2025 mpv/MPlayer/mplayer2 projects"
	if fields := strings.Fields(out); len(fields) >= 2 {
		return fields[1]
	}
	return strings.TrimSpace(out)
}

func checkTool(ctx context.Context, env doctorEnv, name, bin string, version func(string) string, install string) doctorResult {
	path, err := env.lookPath(bin)
	if err != nil {
		return doctorResult{name: name, status: doctorFail, detail: fmt.Sprintf("%s not found in PATH", bin), hint: install}
	}
	out, err := env.run(ctx, path, "--version")
	if err != nil {
		return doctorResult{name: name, status: doctorFail, detail: fmt.Sprintf("%s --version failed: %v", path, err)}
	}
	first, _, _ := strings.Cut(out, "\n")
	return doctorResult{name: name, status: doctorOK, detail: version(first) + "  " + path}
}

// playbackConfig resolves cookies the way run does, so the probe signs in
// exactly as playback will. A non-nil result reports the cookie setup.
func playbackConfig(opts options) (mpv.Config, *doctorResult) {
	cfg := mpv.Config{CookiesFromBrowser: opts.cookiesFromBrowser}
	if opts.cookiesFromBrowser != "" {
		return cfg, &doctorResult{name: "cookies", status: doctorOK, detail: "from browser " + opts.cookiesFromBrowser}
	}
	path, err := cookiesFile(opts.cookies)
	if err != nil {
		return cfg, &doctorResult{name: "cookies", status: doctorFail, detail: err.Error(), hint: "fix cookies in config.toml, or remove it to play anonymously"}
	}
	if path == "" {
		return cfg, nil
	}
	cfg.Cookies = path
	return cfg, &doctorResult{name: "cookies", status: doctorOK, detail: "file " + path}
}

// ytdlpUserConfig reports a yt-dlp config file, which yt-dlp applies to
// ytea's playback without ytea knowing, cookies included.
func ytdlpUserConfig() *doctorResult {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		base = filepath.Join(home, ".config")
	}
	for _, name := range []string{"config", "config.txt"} {
		path := filepath.Join(base, "yt-dlp", name)
		if _, err := os.Stat(path); err == nil {
			return &doctorResult{name: "yt-dlp conf", status: doctorOK, detail: path + " also applies to playback"}
		}
	}
	return nil
}

// checkPlayback resolves and opens a stream the way playback does, with the
// audio output discarded: the exact failure behind "no audio or video data played".
func checkPlayback(ctx context.Context, env doctorEnv, mpvBin string, cfg mpv.Config) doctorResult {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	args := append([]string{"--no-video", "--ao=null", "--length=1", "--msg-level=all=warn"}, cfg.YtdlArgs()...)
	start := time.Now()
	out, err := env.run(ctx, mpvBin, append(args, probeURL)...)
	if err == nil {
		return doctorResult{name: "playback", status: doctorOK, detail: fmt.Sprintf("opened a YouTube stream in %.1fs", time.Since(start).Seconds())}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return doctorResult{name: "playback", status: doctorFail, detail: fmt.Sprintf("no stream after %s", probeTimeout), hint: "check the network connection"}
	}
	detail, hint := explainProbe(out, err)
	return doctorResult{name: "playback", status: doctorFail, detail: detail, hint: hint}
}

var urlPattern = regexp.MustCompile(`https?://\S+`)

// explainProbe turns mpv's output into one line and, when the cause is a known
// one, what to do about it. Stream URLs are dropped: they are long and signed.
func explainProbe(out string, err error) (detail, hint string) {
	switch {
	case strings.Contains(out, "HTTP error 403"):
		return "YouTube refused the stream URL (HTTP 403)",
			"the yt-dlp clients ytea pins no longer work; update ytea, or report it at https://github.com/omegaatt36/ytea/issues"
	case strings.Contains(out, "Sign in to confirm"), strings.Contains(out, "Please sign in"):
		return "YouTube asks for a sign-in",
			"set cookies-from-browser or cookies in config.toml"
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	line := err.Error()
	for _, l := range lines {
		if strings.Contains(l, "ERROR") || strings.Contains(l, "Failed") {
			line = l
		}
	}
	line = strings.TrimSpace(urlPattern.ReplaceAllString(line, "<url>"))
	if runes := []rune(line); len(runes) > detailRunes {
		line = string(runes[:detailRunes]) + "…"
	}
	return line, "details: ~/.local/state/ytea/mpv.log"
}

func checkAudioDevice(ctx context.Context, env doctorEnv, opts options) doctorResult {
	if opts.device == "" {
		return doctorResult{name: "audio", status: doctorOK, detail: "system default output"}
	}
	out, err := env.run(ctx, opts.mpvBin, "--audio-device=help")
	if err != nil {
		return doctorResult{name: "audio", status: doctorFail, detail: fmt.Sprintf("list audio devices: %v", err)}
	}
	if !strings.Contains(out, "'"+opts.device+"'") {
		return doctorResult{name: "audio", status: doctorFail, detail: fmt.Sprintf("audio-device %q is not an mpv device", opts.device), hint: "list them with: mpv --audio-device=help"}
	}
	return doctorResult{name: "audio", status: doctorOK, detail: opts.device}
}

func checkVisualizer(env doctorEnv) doctorResult {
	if err := checkVisualizerTools(env.goos, env.lookPath); err != nil {
		return doctorResult{name: "visualizer", status: doctorWarn, detail: err.Error() + "; the visualizer is off", hint: "see the README for the tools it needs"}
	}
	r := doctorResult{name: "visualizer", status: doctorOK, detail: "capture tools found"}
	if env.goos == "darwin" {
		r.hint = "meters stay flat? allow your terminal under System Settings → Privacy & Security → Screen & System Audio Recording"
	}
	return r
}

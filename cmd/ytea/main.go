// Command ytea is a terminal YouTube music player built on mpv and PipeWire.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/pipewire"
	"github.com/omegaatt36/ytea/internal/tui"
	"github.com/omegaatt36/ytea/internal/youtube"
)

const appName = "ytea"

const spectrumBands = 64

type options struct {
	volume     int
	normalize  bool
	thumbnails bool
	visualizer bool
	mpris      bool
	device     string
	mpvBin     string
	ytdlpBin   string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ytea:", err)
		os.Exit(1)
	}
}

func run() error {
	opts := parseFlags()

	stateDir, err := stateDir()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(stateDir, "ytea.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()
	// The TUI owns stdout/stderr, so logs go to a file.
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))

	for _, bin := range []string{opts.mpvBin, opts.ytdlpBin} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("find %s in PATH: %w", bin, err)
		}
	}
	if opts.visualizer {
		if _, err := exec.LookPath("pw-cat"); err != nil {
			slog.Warn("visualizer disabled", "error", err)
			opts.visualizer = false
		}
	}

	// The spectrum tap finds mpv's stream by node.name; the pid keeps two
	// running instances from tapping each other.
	streamName := fmt.Sprintf("%s-%d", appName, os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	player, err := mpv.Start(ctx, mpv.Config{
		Bin:         opts.mpvBin,
		Socket:      socketPath(),
		ClientName:  streamName,
		AudioDevice: opts.device,
		Volume:      opts.volume,
		Normalize:   opts.normalize,
		LogFile:     filepath.Join(stateDir, "mpv.log"),
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := player.Quit(); err != nil {
			slog.Error("stop mpv", "error", err)
		}
	}()

	deps := tui.Deps{
		Searcher:   youtube.NewSearcher(opts.ytdlpBin),
		Player:     player,
		Thumbnails: opts.thumbnails,
		HTTP:       &http.Client{Timeout: 10 * time.Second},
		Normalize:  opts.normalize,
	}

	if opts.visualizer {
		tap := pipewire.NewTap(streamName, spectrumBands)
		go tap.Run(ctx)
		deps.Tap = tap
	}

	if opts.mpris {
		// Media keys are a nicety; a missing session bus must not block playback.
		server, err := mpris.Start(appName, player)
		if err != nil {
			slog.Warn("mpris disabled", "error", err)
		} else {
			defer server.Close()
			deps.MPRIS = server
		}
	}

	program := tea.NewProgram(tui.New(deps), tea.WithContext(ctx))
	if _, err := program.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return fmt.Errorf("run ui: %w", err)
	}
	return nil
}

func parseFlags() options {
	var opts options
	flag.IntVar(&opts.volume, "volume", 80, "initial volume in percent")
	flag.BoolVar(&opts.normalize, "normalize", true, "even out loudness between tracks (toggle with N)")
	flag.BoolVar(&opts.thumbnails, "thumbnails", true, "show cover thumbnails (kitty graphics when available, else half-block art)")
	flag.BoolVar(&opts.visualizer, "visualizer", true, "show a spectrum tapped from the PipeWire stream")
	flag.BoolVar(&opts.mpris, "mpris", true, "register as an MPRIS player for media keys")
	flag.StringVar(&opts.device, "audio-device", "", `mpv audio device, e.g. "pipewire/<sink node.name>" (default: system default)`)
	flag.StringVar(&opts.mpvBin, "mpv", "mpv", "mpv binary")
	flag.StringVar(&opts.ytdlpBin, "yt-dlp", "yt-dlp", "yt-dlp binary")
	flag.Parse()
	return opts
}

func stateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, appName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	return dir, nil
}

// socketPath is per-process so two ytea instances never share an mpv.
func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, fmt.Sprintf("ytea-%d.sock", os.Getpid()))
}

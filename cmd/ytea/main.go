// Command ytea is a terminal YouTube music player built on mpv.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/urfave/cli/v3"

	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/pipewire"
	"github.com/omegaatt36/ytea/internal/session"
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
	// cookies and cookiesFromBrowser sign yt-dlp in so Premium audio formats are offered.
	cookies            string
	cookiesFromBrowser string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	err := newCommand(run).Run(ctx, os.Args)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytea:", err)
		os.Exit(1)
	}
}

// newCommand parses the command line into options and hands them to action.
func newCommand(action func(context.Context, options) error) *cli.Command {
	var opts options
	return &cli.Command{
		Name:  appName,
		Usage: "terminal YouTube music player",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Usage: "TOML config whose keys are these flag names (default: $XDG_CONFIG_HOME/ytea/config.toml)", TakesFile: true, Sources: env("config")},
			&cli.IntFlag{Name: "volume", Value: 80, Usage: "initial volume in percent", Sources: env("volume"), Destination: &opts.volume},
			&cli.BoolWithInverseFlag{Name: "normalize", Value: true, Usage: "even out loudness between tracks (toggle with N)", Sources: env("normalize"), Destination: &opts.normalize},
			&cli.BoolWithInverseFlag{Name: "thumbnails", Value: true, Usage: "show cover thumbnails (kitty graphics when available, else half-block art)", Sources: env("thumbnails"), Destination: &opts.thumbnails},
			&cli.BoolWithInverseFlag{Name: "visualizer", Value: true, Usage: "show a spectrum of ytea's own audio (Linux with PipeWire)", Sources: env("visualizer"), Destination: &opts.visualizer},
			&cli.BoolWithInverseFlag{Name: "mpris", Value: true, Usage: "register as an MPRIS player for media keys", Sources: env("mpris"), Destination: &opts.mpris},
			&cli.StringFlag{Name: "audio-device", Usage: `mpv audio device from its list, e.g. "pipewire/<sink>" (Linux) or "coreaudio/<id>" (macOS); default: system default`, Sources: env("audio-device"), Destination: &opts.device},
			&cli.StringFlag{Name: "mpv", Value: "mpv", Usage: "mpv binary", Sources: env("mpv"), Destination: &opts.mpvBin},
			&cli.StringFlag{Name: "yt-dlp", Value: "yt-dlp", Usage: "yt-dlp binary", Sources: env("yt-dlp"), Destination: &opts.ytdlpBin},
		},
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{
			Flags: [][]cli.Flag{
				{&cli.StringFlag{Name: "cookies", Usage: "Netscape cookies.txt for yt-dlp; a Premium account unlocks 256k audio", TakesFile: true, Sources: env("cookies"), Destination: &opts.cookies}},
				{&cli.StringFlag{Name: "cookies-from-browser", Usage: `read yt-dlp cookies from a browser, e.g. "firefox" or "chrome:Profile 1"`, Sources: env("cookies-from-browser"), Destination: &opts.cookiesFromBrowser}},
			},
		}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			path, required := cmd.String("config"), true
			if path == "" {
				dir, err := configDir()
				if err != nil {
					return err
				}
				path, required = filepath.Join(dir, "config.toml"), false
			}
			if err := loadConfig(cmd, path, required); err != nil {
				return err
			}
			return action(ctx, opts)
		},
	}
}

// env is the environment variable for a flag: --audio-device reads YTEA_AUDIO_DEVICE.
func env(flag string) cli.ValueSourceChain {
	return cli.EnvVars(strings.ToUpper(appName + "_" + strings.ReplaceAll(flag, "-", "_")))
}

func run(ctx context.Context, opts options) error {
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

	if opts.cookiesFromBrowser == "" {
		if opts.cookies, err = cookiesFile(opts.cookies); err != nil {
			return err
		}
	}
	if opts.cookies != "" || opts.cookiesFromBrowser != "" {
		slog.Info("playback signed in", "cookies", opts.cookies, "cookies_from_browser", opts.cookiesFromBrowser)
	}

	for _, bin := range []string{opts.mpvBin, opts.ytdlpBin} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("find %s in PATH: %w", bin, err)
		}
	}
	if opts.visualizer {
		if err := checkVisualizerTools(exec.LookPath); err != nil {
			slog.Warn("visualizer disabled", "error", err)
			opts.visualizer = false
		}
	}

	// The spectrum tap finds mpv's stream by node.name in the PipeWire graph
	// (Linux only); the pid keeps two running instances from tapping each other.
	streamName := fmt.Sprintf("%s-%d", appName, os.Getpid())

	player, err := mpv.Start(ctx, mpv.Config{
		Bin:         opts.mpvBin,
		Socket:      socketPath(),
		ClientName:  streamName,
		AudioDevice: opts.device,
		Volume:      opts.volume,
		Normalize:   opts.normalize,
		LogFile:     filepath.Join(stateDir, "mpv.log"),
		// Search stays anonymous; only playback needs the account.
		Cookies:            opts.cookies,
		CookiesFromBrowser: opts.cookiesFromBrowser,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := player.Quit(); err != nil {
			slog.Error("stop mpv", "error", err)
		}
	}()
	sessionHealthy := true
	saved, err := session.Load(stateDir)
	if err != nil {
		slog.Warn("load previous session", "error", err)
	} else if saved.Version != 0 {
		restoreCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := player.Restore(restoreCtx, mpv.PlaybackState{
			URLs: saved.URLs, Index: saved.Index, Volume: saved.Volume,
		})
		cancel()
		if err != nil {
			slog.Warn("restore previous session", "error", err)
			sessionHealthy = false
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if stopErr := player.Stop(stopCtx); stopErr != nil {
				slog.Warn("stop partially restored session", "error", stopErr)
			}
			stopCancel()
		}
	}

	deps := tui.Deps{
		Searcher:      youtube.NewSearcher(opts.ytdlpBin),
		Player:        player,
		Thumbnails:    opts.thumbnails,
		HTTP:          &http.Client{Timeout: 10 * time.Second},
		Normalize:     opts.normalize,
		InitialTracks: tracksFromSession(saved),
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
	final, runErr := program.Run()
	if sessionHealthy {
		snapshotCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if snapshot, err := player.Snapshot(snapshotCtx); err != nil {
			slog.Warn("snapshot playback", "error", err)
		} else {
			model, ok := final.(tui.Model)
			if !ok {
				model = tui.New(deps)
			}
			if err := session.Save(stateDir, sessionFromSnapshot(snapshot, model)); err != nil {
				slog.Warn("save session", "error", err)
			}
		}
		cancel()
	}
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return fmt.Errorf("run ui: %w", runErr)
	}
	return nil
}

func tracksFromSession(saved session.State) map[string]youtube.Track {
	tracks := make(map[string]youtube.Track, len(saved.Metadata))
	for url, info := range saved.Metadata {
		tracks[url] = youtube.Track{URL: url, ID: info.ID, Title: info.Title, Channel: info.Channel, Live: info.Live}
	}
	return tracks
}

func sessionFromSnapshot(snapshot mpv.PlaybackState, model tui.Model) session.State {
	state := session.State{URLs: snapshot.URLs, Index: snapshot.Index, Volume: snapshot.Volume}
	for i, url := range snapshot.URLs {
		track := model.KnownTrack(url)
		info := session.Metadata{ID: track.ID, Title: track.Title, Channel: track.Channel, Live: track.Live}
		if info.Title == "" && i < len(snapshot.Entries) {
			info.Title = snapshot.Entries[i].Title
		}
		if info != (session.Metadata{}) {
			if state.Metadata == nil {
				state.Metadata = make(map[string]session.Metadata)
			}
			state.Metadata[url] = info
		}
	}
	return state
}

// Both tools are needed by the PipeWire tap after the TUI starts.
func checkVisualizerTools(lookPath func(string) (string, error)) error {
	for _, bin := range []string{"pw-cat", "pw-dump"} {
		if _, err := lookPath(bin); err != nil {
			return fmt.Errorf("find %s in PATH: %w", bin, err)
		}
	}
	return nil
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

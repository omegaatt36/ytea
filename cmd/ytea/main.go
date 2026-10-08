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
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/urfave/cli/v3"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/engine"
	"github.com/omegaatt36/ytea/internal/audiotee"
	"github.com/omegaatt36/ytea/internal/mpris"
	"github.com/omegaatt36/ytea/internal/pipewire"
	"github.com/omegaatt36/ytea/internal/tui"
)

const appName = "ytea"

const spectrumBands = 64

type options struct {
	volume             int
	normalize          bool
	thumbnails         bool
	visualizer         bool
	mpris              bool
	device             string
	mpvBin             string
	ytdlpBin           string
	googleClientID     string
	googleClientSecret string
	youtubeChannelID   string
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
	command := &cli.Command{
		Name:  appName,
		Usage: "terminal YouTube music player",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Usage: "TOML config whose keys are these flag names (default: $XDG_CONFIG_HOME/ytea/config.toml)", TakesFile: true, Sources: env("config")},
			&cli.IntFlag{Name: "volume", Value: 80, Usage: "initial volume in percent", Sources: env("volume"), Destination: &opts.volume},
			&cli.BoolWithInverseFlag{Name: "normalize", Value: true, Usage: "even out loudness between tracks (toggle with N)", Sources: env("normalize"), Destination: &opts.normalize},
			&cli.BoolWithInverseFlag{Name: "thumbnails", Value: true, Usage: "show cover thumbnails (kitty graphics when available, else half-block art)", Sources: env("thumbnails"), Destination: &opts.thumbnails},
			&cli.BoolWithInverseFlag{Name: "visualizer", Value: true, Usage: "show spectrum or stereo VU meters for ytea's audio (Linux with PipeWire, macOS 14.2+ with audiotee)", Sources: env("visualizer"), Destination: &opts.visualizer},
			&cli.BoolWithInverseFlag{Name: "mpris", Value: true, Usage: "register as an MPRIS player for media keys", Sources: env("mpris"), Destination: &opts.mpris},
			&cli.StringFlag{Name: "audio-device", Usage: `mpv audio device from its list, e.g. "pipewire/<sink>" (Linux) or "coreaudio/<id>" (macOS); default: system default`, Sources: env("audio-device"), Destination: &opts.device},
			&cli.StringFlag{Name: "mpv", Value: "mpv", Usage: "mpv binary", Sources: env("mpv"), Destination: &opts.mpvBin},
			&cli.StringFlag{Name: "yt-dlp", Value: "yt-dlp", Usage: "yt-dlp binary", Sources: env("yt-dlp"), Destination: &opts.ytdlpBin},
			&cli.StringFlag{Name: "google-client-id", Usage: "Google OAuth client ID", Sources: nonEmptyEnv("google-client-id"), Destination: &opts.googleClientID},
			&cli.StringFlag{Name: "google-client-secret", Usage: "Google OAuth client secret", Sources: nonEmptyEnv("google-client-secret"), Destination: &opts.googleClientSecret},
			&cli.StringFlag{Name: "youtube-channel-id", Usage: "YouTube channel ID owning account playlists when using cookies", Sources: nonEmptyEnv("youtube-channel-id"), Destination: &opts.youtubeChannelID},
		},
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{
			Flags: [][]cli.Flag{
				{&cli.StringFlag{Name: "cookies", Usage: "Netscape cookies.txt for yt-dlp; a Premium account unlocks 256k audio", TakesFile: true, Sources: env("cookies"), Destination: &opts.cookies}},
				{&cli.StringFlag{Name: "cookies-from-browser", Usage: `read yt-dlp cookies from a browser, e.g. "firefox" or "chrome:Profile 1"`, Sources: env("cookies-from-browser"), Destination: &opts.cookiesFromBrowser}},
			},
		}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := loadOptions(cmd); err != nil {
				return err
			}
			if opts.googleClientID != "" && opts.googleClientSecret == "" {
				return fmt.Errorf("google-client-secret is required when google-client-id is set")
			}
			if opts.googleClientSecret != "" && opts.googleClientID == "" {
				return fmt.Errorf("google-client-id is required when google-client-secret is set")
			}
			return action(ctx, opts)
		},
	}
	command.Commands = []*cli.Command{authCommand(&opts, command)}
	return command
}

func loadOptions(cmd *cli.Command) error {
	path, required := cmd.String("config"), true
	if path == "" {
		dir, err := configDir()
		if err != nil {
			return err
		}
		path, required = filepath.Join(dir, "config.toml"), false
	}
	return loadConfig(cmd, path, required)
}

// env is the environment variable for a flag: --audio-device reads YTEA_AUDIO_DEVICE.
func env(flag string) cli.ValueSourceChain {
	return cli.EnvVars(strings.ToUpper(appName + "_" + strings.ReplaceAll(flag, "-", "_")))
}

type nonEmptyEnvSource struct {
	cli.ValueSource
	key string
}

func (s nonEmptyEnvSource) Lookup() (string, bool) {
	value, found := s.ValueSource.Lookup()
	return value, found && value != ""
}

func (s nonEmptyEnvSource) IsFromEnv() bool { return true }
func (s nonEmptyEnvSource) Key() string     { return s.key }

func nonEmptyEnv(flag string) cli.ValueSourceChain {
	key := strings.ToUpper(appName + "_" + strings.ReplaceAll(flag, "-", "_"))
	return cli.NewValueSourceChain(nonEmptyEnvSource{ValueSource: cli.EnvVar(key), key: key})
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
	accountSource, err := accountPlaylistSource(opts)
	if err != nil {
		return err
	}

	for _, bin := range []string{opts.mpvBin, opts.ytdlpBin} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("find %s in PATH: %w", bin, err)
		}
	}
	if opts.visualizer {
		if err := checkVisualizerTools(runtime.GOOS, exec.LookPath); err != nil {
			slog.Warn("visualizer disabled", "error", err)
			opts.visualizer = false
		}
	}

	// The Linux spectrum tap finds mpv's stream by node.name in the PipeWire
	// graph; the pid keeps two running instances from tapping each other.
	streamName := fmt.Sprintf("%s-%d", appName, os.Getpid())

	configPath, err := configDir()
	if err != nil {
		return err
	}
	eng, err := engine.New(ctx, engine.Config{
		MPVBin:      opts.mpvBin,
		YtDlpBin:    opts.ytdlpBin,
		StateDir:    stateDir,
		ConfigDir:   configPath,
		ClientName:  streamName,
		AudioDevice: opts.device,
		Volume:      opts.volume,
		Normalize:   opts.normalize,
		// Search stays anonymous; only playback needs the account.
		Cookies:            opts.cookies,
		CookiesFromBrowser: opts.cookiesFromBrowser,
		Account:            accountSource,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := eng.Close(); err != nil {
			slog.Error("stop mpv", "error", err)
		}
	}()

	core := eng.Deps()
	deps := tui.Deps{
		AccountPlaylists: core.Account,
		AccountIgnores:   core.AccountIgnores,
		Searcher:         core.Searcher,
		Player:           core.Player,
		Thumbnails:       opts.thumbnails,
		HTTP:             &http.Client{Timeout: 10 * time.Second},
		Normalize:        core.Normalize,
		InitialTracks:    core.InitialTracks,
		Library:          core.Library,
		History:          core.History,
	}

	if opts.visualizer {
		tap := newSpectrumTap(streamName, eng.PlayerPID())
		if deviceTap, ok := tap.(interface{ SetAudioDevice(string) error }); ok {
			_ = deviceTap.SetAudioDevice(opts.device)
		}
		go tap.Run(ctx)
		deps.Tap = tap
	}

	if opts.mpris {
		// Media keys are a nicety; a missing session bus must not block playback.
		server, err := mpris.Start(appName, core.Player)
		if err != nil {
			slog.Warn("mpris disabled", "error", err)
		} else {
			defer server.Close()
			deps.MPRIS = server
		}
	}

	program := tea.NewProgram(tui.New(deps), tea.WithContext(ctx))
	final, runErr := program.Run()
	known := func(url string) domain.Track { return core.InitialTracks[url] }
	if model, ok := final.(tui.Model); ok {
		known = model.KnownTrack
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := eng.SaveSession(saveCtx, known); err != nil {
		slog.Warn("save session", "error", err)
	}
	cancel()
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return fmt.Errorf("run ui: %w", runErr)
	}
	return nil
}

type spectrumTap interface {
	tui.Spectrum
	Run(context.Context)
}

// newSpectrumTap picks the capture backend for this OS; checkVisualizerTools
// has already rejected the rest.
func newSpectrumTap(streamName string, mpvPID int) spectrumTap {
	if runtime.GOOS == "darwin" {
		return audiotee.NewTap(mpvPID, spectrumBands)
	}
	return pipewire.NewTap(streamName, spectrumBands)
}

// checkVisualizerTools finds the binaries the spectrum tap on goos runs after the TUI starts.
func checkVisualizerTools(goos string, lookPath func(string) (string, error)) error {
	var bins []string
	switch goos {
	case "linux":
		bins = []string{"pw-cat", "pw-dump"}
	case "darwin":
		bins = []string{"audiotee"}
	default:
		return fmt.Errorf("no spectrum capture on %s", goos)
	}
	for _, bin := range bins {
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

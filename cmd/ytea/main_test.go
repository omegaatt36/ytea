package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/session"
	"github.com/omegaatt36/ytea/internal/tui"
	"github.com/omegaatt36/ytea/internal/youtube"
)

// parse runs the command line with an empty config dir, so the developer's
// own ~/.config/ytea never leaks into a test.
func parse(t *testing.T, args ...string) (options, error) {
	t.Helper()
	return parseIn(t, t.TempDir(), args...)
}

// parseIn runs the command line with XDG_CONFIG_HOME set to configHome.
func parseIn(t *testing.T, configHome string, args ...string) (options, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	var got options
	cmd := newCommand(func(_ context.Context, opts options) error {
		got = opts
		return nil
	})
	cmd.Writer, cmd.ErrWriter = io.Discard, io.Discard
	err := cmd.Run(t.Context(), append([]string{appName}, args...))
	return got, err
}

func TestFlagsDefaults(t *testing.T) {
	opts, err := parse(t)
	if err != nil {
		t.Fatal(err)
	}
	want := options{
		volume: 80, normalize: true, thumbnails: true, visualizer: true, mpris: true,
		mpvBin: "mpv", ytdlpBin: "yt-dlp",
	}
	if opts != want {
		t.Errorf("options = %+v, want %+v", opts, want)
	}
}

func TestFlagsInverseBool(t *testing.T) {
	opts, err := parse(t, "--no-normalize", "--no-mpris")
	if err != nil {
		t.Fatal(err)
	}
	if opts.normalize || opts.mpris {
		t.Errorf("normalize=%v mpris=%v, want both false", opts.normalize, opts.mpris)
	}
}

func TestFlagsCookies(t *testing.T) {
	opts, err := parse(t, "--cookies", "c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if opts.cookies != "c.txt" {
		t.Errorf("cookies = %q, want %q", opts.cookies, "c.txt")
	}

	opts, err = parse(t, "--cookies-from-browser", "chrome:Profile 1")
	if err != nil {
		t.Fatal(err)
	}
	if opts.cookiesFromBrowser != "chrome:Profile 1" {
		t.Errorf("cookiesFromBrowser = %q, want %q", opts.cookiesFromBrowser, "chrome:Profile 1")
	}
}

func TestFlagsCookieSourcesExclusive(t *testing.T) {
	if _, err := parse(t, "--cookies", "c.txt", "--cookies-from-browser", "firefox"); err == nil {
		t.Error("want an error when both cookie sources are set")
	}
}

func TestCheckVisualizerTools(t *testing.T) {
	missing := errors.New("missing binary")
	for _, tt := range []struct {
		name       string
		missingBin string
		wantCalls  string
	}{
		{name: "both present", wantCalls: "pw-cat,pw-dump"},
		{name: "pw-cat missing", missingBin: "pw-cat", wantCalls: "pw-cat"},
		{name: "pw-dump missing", missingBin: "pw-dump", wantCalls: "pw-cat,pw-dump"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			err := checkVisualizerTools(func(bin string) (string, error) {
				calls = append(calls, bin)
				if bin == tt.missingBin {
					return "", missing
				}
				return "/usr/bin/" + bin, nil
			})
			if got := strings.Join(calls, ","); got != tt.wantCalls {
				t.Errorf("looked up %q, want %q", got, tt.wantCalls)
			}
			if tt.missingBin == "" {
				if err != nil {
					t.Fatalf("checkVisualizerTools() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, missing) || !strings.Contains(err.Error(), tt.missingBin) {
				t.Errorf("checkVisualizerTools() = %v, want error for %s", err, tt.missingBin)
			}
		})
	}
}

func TestCLIHelpProcess(t *testing.T) {
	bin := filepath.Join(t.TempDir(), appName)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.CommandContext(t.Context(), bin, "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run CLI help: %v\n%s", err, output)
	}
	for _, flag := range []string{"--[no-]visualizer", "--mpv", "--yt-dlp"} {
		if !strings.Contains(string(output), flag) {
			t.Errorf("CLI help missing %q:\n%s", flag, output)
		}
	}
}

func TestSessionRoundTripKeepsUnplayedTrackMetadata(t *testing.T) {
	const first, second = "https://www.youtube.com/watch?v=first", "https://www.youtube.com/watch?v=second"
	model := tui.New(tui.Deps{InitialTracks: map[string]youtube.Track{
		second: {URL: second, ID: "second", Title: "Unplayed song", Channel: "Artist"},
	}})
	snapshot := mpv.PlaybackState{
		URLs:    []string{first, second},
		Entries: []mpv.PlaylistEntry{{Filename: first, Title: "Already playing"}, {Filename: second}},
		Index:   0,
		Volume:  65,
	}
	dir := t.TempDir()
	if err := session.Save(dir, sessionFromSnapshot(snapshot, model)); err != nil {
		t.Fatal(err)
	}
	saved, err := session.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored := tui.New(tui.Deps{InitialTracks: tracksFromSession(saved)})
	if got := restored.KnownTrack(second); got.Title != "Unplayed song" || got.Channel != "Artist" || got.ID != "second" {
		t.Errorf("unplayed track after restart = %+v", got)
	}
	if got := restored.KnownTrack(first).Title; got != "Already playing" {
		t.Errorf("mpv title after restart = %q, want Already playing", got)
	}
}

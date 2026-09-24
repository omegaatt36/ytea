package main

import (
	"context"
	"io"
	"testing"
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

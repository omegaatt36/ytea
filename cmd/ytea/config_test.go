package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, home, content string) string {
	t.Helper()
	dir := filepath.Join(home, appName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigFillsUnsetFlags(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `
volume = 60
normalize = false
audio-device = "pipewire/usb"
`)
	opts, err := parseIn(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if opts.volume != 60 || opts.normalize || opts.device != "pipewire/usb" {
		t.Errorf("volume=%d normalize=%v device=%q, want 60 false pipewire/usb", opts.volume, opts.normalize, opts.device)
	}
	if !opts.thumbnails {
		t.Error("thumbnails = false, want the default true for a key the config omits")
	}
}

func TestConfigPrecedence(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "volume = 60\naudio-device = \"pipewire/usb\"\n")
	t.Setenv("YTEA_AUDIO_DEVICE", "pipewire/env")

	opts, err := parseIn(t, home, "--volume", "90")
	if err != nil {
		t.Fatal(err)
	}
	if opts.volume != 90 {
		t.Errorf("volume = %d, want the flag's 90 over the config's 60", opts.volume)
	}
	if opts.device != "pipewire/env" {
		t.Errorf("device = %q, want the environment's value over the config's", opts.device)
	}
}

func TestConfigExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.toml")
	if err := os.WriteFile(path, []byte("volume = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := parse(t, "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	if opts.volume != 30 {
		t.Errorf("volume = %d, want 30", opts.volume)
	}

	if _, err := parse(t, "--config", filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Error("want an error for a named config file that does not exist")
	}
}

func TestConfigErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "syntax", content: "volume = \n", wantErr: "parse config"},
		{name: "unknown key", content: "volum = 60\n", wantErr: `unknown key "volum"`},
		{name: "config key", content: "config = \"x.toml\"\n", wantErr: `unknown key "config"`},
		{name: "wrong type", content: "volume = \"loud\"\n", wantErr: `key "volume"`},
		{name: "table", content: "[player]\nvolume = 60\n", wantErr: `unknown key "player"`},
		{name: "table for a flag", content: "[mpv]\nbin = \"mpv\"\n", wantErr: "want a string, number or boolean"},
		{name: "both cookie sources", content: "cookies = \"c.txt\"\ncookies-from-browser = \"firefox\"\n", wantErr: "only one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, tt.content)
			_, err := parseIn(t, home)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigCookiesPath(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	tests := []struct {
		name    string
		cookies string
		want    string // relative to the config dir when not absolute
	}{
		{name: "relative to config file", cookies: "cookies.txt", want: "cookies.txt"},
		{name: "home", cookies: "~/yt/cookies.txt", want: "/home/u/yt/cookies.txt"},
		{name: "absolute", cookies: "/etc/c.txt", want: "/etc/c.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := writeConfig(t, home, "cookies = \""+tt.cookies+"\"\n")
			want := tt.want
			if !filepath.IsAbs(want) {
				want = filepath.Join(filepath.Dir(path), want)
			}
			opts, err := parseIn(t, home)
			if err != nil {
				t.Fatal(err)
			}
			if opts.cookies != want {
				t.Errorf("cookies = %q, want %q", opts.cookies, want)
			}
		})
	}
}

func TestConfigCookiesOverriddenBySource(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "cookies = \"c.txt\"\n")

	opts, err := parseIn(t, home, "--cookies-from-browser", "firefox")
	if err != nil {
		t.Fatal(err)
	}
	if opts.cookies != "" || opts.cookiesFromBrowser != "firefox" {
		t.Errorf("cookies=%q cookiesFromBrowser=%q, want only the flag's browser", opts.cookies, opts.cookiesFromBrowser)
	}
}

func TestCookiesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	got, err := cookiesFile("")
	if err != nil || got != "" {
		t.Errorf("cookiesFile(\"\") = %q, %v; want signed out without a default file", got, err)
	}

	dflt := filepath.Join(home, appName, "cookies.txt")
	if err := os.MkdirAll(filepath.Dir(dflt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dflt, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := cookiesFile(""); err != nil || got != dflt {
		t.Errorf("cookiesFile(\"\") = %q, %v; want the default %q", got, err, dflt)
	}

	if _, err := cookiesFile(filepath.Join(home, "missing.txt")); err == nil {
		t.Error("want an error for a named cookies file that does not exist")
	}
}

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

func TestYoutubeChannelIDSourcesAndPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  string
		args []string
		want string
	}{
		{name: "config", want: "UCconfig"},
		{name: "environment overrides config", env: "UCenv", want: "UCenv"},
		{name: "flag overrides environment and config", env: "UCenv", args: []string{"--youtube-channel-id", "UCflag"}, want: "UCflag"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("YTEA_YOUTUBE_CHANNEL_ID", tt.env)
			home := t.TempDir()
			writeConfig(t, home, "youtube-channel-id = \"UCconfig\"\n")
			opts, err := parseIn(t, home, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if opts.youtubeChannelID != tt.want {
				t.Errorf("youtubeChannelID = %q, want %q", opts.youtubeChannelID, tt.want)
			}
		})
	}
}

func TestGoogleClientCredentialsSourcesAndPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		envID      string
		envSecret  string
		args       []string
		wantID     string
		wantSecret string
	}{
		{
			name:       "config",
			config:     "google-client-id = \"config-id\"\ngoogle-client-secret = \"config-secret\"\n",
			wantID:     "config-id",
			wantSecret: "config-secret",
		},
		{
			name:       "environment overrides config",
			config:     "google-client-id = \"config-id\"\ngoogle-client-secret = \"config-secret\"\n",
			envID:      "env-id",
			envSecret:  "env-secret",
			wantID:     "env-id",
			wantSecret: "env-secret",
		},
		{
			name:       "flags override environment and config",
			config:     "google-client-id = \"config-id\"\ngoogle-client-secret = \"config-secret\"\n",
			envID:      "env-id",
			envSecret:  "env-secret",
			args:       []string{"--google-client-id", "flag-id", "--google-client-secret", "flag-secret"},
			wantID:     "flag-id",
			wantSecret: "flag-secret",
		},
		{
			name:       "credentials may come from different sources",
			config:     "google-client-secret = \"config-secret\"\n",
			args:       []string{"--google-client-id", "flag-id"},
			wantID:     "flag-id",
			wantSecret: "config-secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("YTEA_GOOGLE_CLIENT_ID", tt.envID)
			t.Setenv("YTEA_GOOGLE_CLIENT_SECRET", tt.envSecret)
			home := t.TempDir()
			if tt.config != "" {
				writeConfig(t, home, tt.config)
			}
			opts, err := parseIn(t, home, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if opts.googleClientID != tt.wantID || opts.googleClientSecret != tt.wantSecret {
				t.Errorf("credentials = (%q, %q), want (%q, %q)", opts.googleClientID, opts.googleClientSecret, tt.wantID, tt.wantSecret)
			}
		})
	}
}

func TestGoogleClientCredentialsRequireBothAtStartup(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		envID   string
		envKey  string
		args    []string
		missing string
	}{
		{name: "config ID only", config: "google-client-id = \"config-id\"\n", missing: "google-client-secret"},
		{name: "config secret only", config: "google-client-secret = \"config-secret\"\n", missing: "google-client-id"},
		{name: "environment ID only", envID: "env-id", missing: "google-client-secret"},
		{name: "environment secret only", envKey: "env-secret", missing: "google-client-id"},
		{name: "flag ID only", args: []string{"--google-client-id", "flag-id"}, missing: "google-client-secret"},
		{name: "flag secret only", args: []string{"--google-client-secret", "flag-secret"}, missing: "google-client-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("YTEA_GOOGLE_CLIENT_ID", tt.envID)
			t.Setenv("YTEA_GOOGLE_CLIENT_SECRET", tt.envKey)
			home := t.TempDir()
			if tt.config != "" {
				writeConfig(t, home, tt.config)
			}
			_, err := parseIn(t, home, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.missing) {
				t.Errorf("startup error = %v, want it to name missing %q", err, tt.missing)
			}
		})
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

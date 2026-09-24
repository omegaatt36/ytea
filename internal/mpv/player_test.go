package mpv

import (
	"slices"
	"strings"
	"testing"
)

func TestConfigArgsCookies(t *testing.T) {
	const extractorArgs = "--ytdl-raw-options-append=extractor-args=youtube:player_client=default,web_music"

	tests := []struct {
		name string
		cfg  Config
		want []string
	}{
		{
			name: "no cookies",
			cfg:  Config{},
			want: nil,
		},
		{
			name: "cookies file",
			cfg:  Config{Cookies: "/home/u/cookies.txt"},
			want: []string{"--ytdl-raw-options-append=cookies=/home/u/cookies.txt", extractorArgs},
		},
		{
			name: "cookies from browser",
			cfg:  Config{CookiesFromBrowser: "chrome:Profile 1"},
			want: []string{"--ytdl-raw-options-append=cookies-from-browser=chrome:Profile 1", extractorArgs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, arg := range tt.cfg.args() {
				if strings.HasPrefix(arg, "--ytdl-raw-options") {
					got = append(got, arg)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ytdl raw options = %q, want %q", got, tt.want)
			}
		})
	}
}

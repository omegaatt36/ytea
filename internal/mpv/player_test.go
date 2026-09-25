package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAppendAllAppendsWithoutJumping(t *testing.T) {
	var ops [][]string
	c := newFakePair(t, func(req request) string {
		cmd := make([]string, len(req.Command))
		for i, a := range req.Command {
			cmd[i] = fmt.Sprint(a)
		}
		ops = append(ops, cmd)
		b, _ := json.Marshal(req.RequestID)
		return `{"request_id":` + string(b) + `,"error":"success"}`
	})
	p := &Player{client: c}
	ctx := context.Background()

	if err := p.AppendAll(ctx, nil); err != nil {
		t.Fatalf("AppendAll(nil) error = %v", err)
	}
	if err := p.AppendAll(ctx, []string{"u1", "u2", "u3"}); err != nil {
		t.Fatalf("AppendAll() error = %v", err)
	}

	want := [][]string{
		{"loadfile", "u1", "append-play"}, // starts an idle player
		{"loadfile", "u2", "append"},      // the rest must not trigger playback
		{"loadfile", "u3", "append"},
	}
	eq := func(a, b []string) bool { return slices.Equal(a, b) }
	if !slices.EqualFunc(ops, want, eq) {
		t.Errorf("AppendAll() commands = %v, want %v", ops, want)
	}
}

func TestAudioDevices(t *testing.T) {
	c := newFakePair(t, func(req request) string {
		b, _ := json.Marshal(req.RequestID)
		if len(req.Command) == 2 && req.Command[0] == "get_property" && req.Command[1] == PropAudioDeviceList {
			return `{"request_id":` + string(b) + `,"error":"success","data":` +
				`[{"name":"auto","description":"Autoselect device"},` +
				`{"name":"coreaudio/0x44d9","description":"External Headphones"}]}`
		}
		return `{"request_id":` + string(b) + `,"error":"property unavailable"}`
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := (&Player{client: c}).AudioDevices(ctx)
	if err != nil {
		t.Fatalf("AudioDevices() error = %v", err)
	}
	want := []AudioDevice{
		{Name: "auto", Description: "Autoselect device"},
		{Name: "coreaudio/0x44d9", Description: "External Headphones"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("AudioDevices() = %+v, want %+v", got, want)
	}
	if got[1].Label() != "External Headphones" {
		t.Errorf("Label() = %q, want the description", got[1].Label())
	}
}

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

package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/internal/googleauth"
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

// TestMain lets TestMPVDiesWithParent re-run this binary as a stand-in for
// ytea: it starts mpv, reports mpv's pid and waits to be SIGKILLed.
func TestMain(m *testing.M) {
	if os.Getenv("YTEA_MPV_PARENT") == "1" {
		p, err := Start(context.Background(), Config{Bin: "mpv"})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(p.cmd.Process.Pid)
		select {}
	}
	os.Exit(m.Run())
}

func TestMPVDiesWithParent(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not in PATH")
	}
	parent := exec.Command(os.Args[0])
	parent.Env = append(os.Environ(), "YTEA_MPV_PARENT=1")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Fscan(out, &pid); err != nil {
		_ = parent.Process.Kill()
		t.Fatalf("read mpv pid: %v", err)
	}

	// SIGKILL skips every deferred Quit, like the benchmark that leaked mpv.
	_ = parent.Process.Kill()
	_ = parent.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("mpv %d outlived its SIGKILLed parent", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestConfigArgsUseCookiesWhenOAuthTokenExists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := googleauth.SaveToken(&googleauth.Token{
		AccessToken: "oauth-access-secret", RefreshToken: "oauth-refresh-secret",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	args := (Config{Cookies: "/tmp/cookies.txt"}).args()
	const cookieArg = "--ytdl-raw-options-append=cookies=/tmp/cookies.txt"
	if !slices.Contains(args, cookieArg) {
		t.Errorf("mpv args = %q, want cookie option %q", args, cookieArg)
	}
	for _, arg := range args {
		if strings.Contains(arg, "oauth-access-secret") || strings.Contains(arg, "oauth-refresh-secret") {
			t.Errorf("mpv arg %q contains OAuth credentials", arg)
		}
	}
}

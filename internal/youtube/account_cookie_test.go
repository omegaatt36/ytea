package youtube

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// stubCookieYtDlp returns canned yt-dlp JSON and records every invocation.
func stubCookieYtDlp(t *testing.T, feed, detail, tracks string) (string, string) {
	return stubCookieYtDlpWithLikes(t, feed, detail, tracks, `{}`)
}

func stubCookieYtDlpWithLikes(t *testing.T, feed, detail, tracks, likes string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	for name, body := range map[string]string{"feed": feed, "detail": detail, "tracks": tracks, "likes": likes} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "yt-dlp")
	script := fmt.Sprintf(`#!/bin/sh
printf 'CALL\n' >> %q
printf '%%s\n' "$@" >> %q
case " $* " in
  *"/feed/playlists"*) cat %q ;;
  *":ytfav"*|*"list=LL"*) cat %q ;;
  *"PLneedsdetail"*) cat %q ;;
  *) cat %q ;;
esac
`, args, args, filepath.Join(dir, "feed"), filepath.Join(dir, "likes"), filepath.Join(dir, "detail"), filepath.Join(dir, "tracks"))
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, args
}

func TestCookieAccountListPlaylistsIncludesLikedVideosWhenFeedOmitsIt(t *testing.T) {
	bin, args := stubCookieYtDlpWithLikes(t,
		`{"_type":"playlist","entries":[{"id":"PLmine","title":"My mix","channel_id":"UCmine"}]}`,
		`{}`, `{}`,
		`{"_type":"playlist","id":"LL","title":"Liked videos","entries":[]}`,
	)
	client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "cookies.txt", ChannelID: "UCmine"})
	got, err := client.ListPlaylists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "PLmine" || got[1].ID != "LL" || got[1].Title != "Liked videos" {
		t.Errorf("ListPlaylists() = %+v, want My mix and Liked videos from liked listing", got)
	}
	calls := readCookieCalls(t, args)
	if !strings.Contains(strings.Join(calls, ""), ":ytfav") && !strings.Contains(strings.Join(calls, ""), "list=LL") {
		t.Errorf("yt-dlp calls = %q, want liked-videos lookup", calls)
	}
}

func TestCookieAccountListPlaylistsIncludesLikedVideosWithCanonicalID(t *testing.T) {
	bin, _ := stubCookieYtDlpWithLikes(t,
		`{"_type":"playlist","entries":[{"id":"PLmine","title":"My mix","channel_id":"UCmine"}]}`,
		`{}`, `{}`,
		`{"_type":"playlist","id":"LLmine","title":"Liked videos","entries":[]}`,
	)
	client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "cookies.txt", ChannelID: "UCmine"})
	got, err := client.ListPlaylists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, playlist := range got {
		if playlist.Title == "Liked videos" && strings.Contains(playlist.URL, "list=LL") {
			return
		}
	}
	t.Errorf("ListPlaylists() = %+v, want Liked videos from successful direct liked-playlist lookup", got)
}

func readCookieCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(strings.TrimPrefix(string(data), "CALL\n")), "CALL\n")
}

func TestCookieAccountListPlaylistsVerifiesOwnerAndKeepsLikedVideos(t *testing.T) {
	const feed = `{"_type":"playlist","entries":[
{"id":"PLmine","title":"My mix","channel_id":"UCmine"},
{"id":"PLsaved","title":"Saved mix","channel_id":"UCother"},
{"id":"PLneedsdetail","title":"Needs detail"},
{"id":"PLunknown","title":"Unknown owner"},
{"id":"PLsame-title","title":"Liked Music","channel_id":"UCmine"},
{"id":"LL","title":"Liked videos","channel_id":"UCmine"},
{"id":"LM","title":"Musique aimée","channel_id":"UCmine"}]}`
	bin, args := stubCookieYtDlpWithLikes(t, feed, `{"id":"PLneedsdetail","title":"Needs detail","channel_id":"UCmine"}`, `{"id":"PLunknown","title":"Unknown owner"}`, `{"_type":"playlist","id":"LL","title":"Liked videos","entries":[]}`)
	client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "/tmp/account-cookies.txt", ChannelID: "UCmine"})
	got, err := client.ListPlaylists(t.Context())
	if err != nil {
		t.Fatalf("ListPlaylists() error = %v", err)
	}
	want := []AccountPlaylist{
		{ID: "PLmine", Title: "My mix", URL: "https://www.youtube.com/playlist?list=PLmine"},
		{ID: "PLneedsdetail", Title: "Needs detail", URL: "https://www.youtube.com/playlist?list=PLneedsdetail"},
		{ID: "PLsame-title", Title: "Liked Music", URL: "https://www.youtube.com/playlist?list=PLsame-title"},
		{ID: "LL", Title: "Liked videos", URL: "https://www.youtube.com/playlist?list=LL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListPlaylists() = %+v, want %+v", got, want)
	}
	calls := readCookieCalls(t, args)
	if len(calls) < 3 || !strings.Contains(calls[0], "/feed/playlists") || !strings.Contains(strings.Join(calls, ""), "PLneedsdetail") || !strings.Contains(strings.Join(calls, ""), "PLunknown") {
		t.Errorf("yt-dlp calls = %q, want feed and both missing-owner details", calls)
	}
	if strings.Contains(calls[0], "--playlist-items") {
		t.Errorf("feed call = %q, want all playlist entries for discovery", calls[0])
	}
	for _, call := range calls {
		if !strings.Contains(call, "--cookies\n/tmp/account-cookies.txt") {
			t.Errorf("yt-dlp call = %q, want cookie file argument", call)
		}
		if !strings.Contains(call, "/feed/playlists") && !strings.Contains(call, "--playlist-items\n0") {
			t.Errorf("metadata call = %q, want --playlist-items 0 to avoid loading tracks", call)
		}
	}
}

func TestCookieAccountUsesBrowserCookies(t *testing.T) {
	bin, args := stubCookieYtDlp(t, `{"entries":[]}`, `{}`, `{}`)
	client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, CookiesFromBrowser: "firefox:default", ChannelID: "UCmine"})
	_, _ = client.ListPlaylists(t.Context())
	calls := readCookieCalls(t, args)
	if len(calls) == 0 || !strings.Contains(calls[0], "--cookies-from-browser\nfirefox:default") || strings.Contains(calls[0], "--cookies\n") {
		t.Errorf("yt-dlp calls = %q, want browser cookie source only", calls)
	}
}

func TestCookieAccountListPlaylistsReportsYtDlpFailureAndEmptyFeed(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "yt-dlp")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'cookies expired' >&2\nexit 17\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "cookies.txt", ChannelID: "UCmine"})
		if _, err := client.ListPlaylists(t.Context()); err == nil || !strings.Contains(err.Error(), "cookies expired") {
			t.Errorf("ListPlaylists() error = %v, want yt-dlp diagnostic", err)
		}
	})
	t.Run("empty account data", func(t *testing.T) {
		bin, _ := stubCookieYtDlp(t, `{"entries":[]}`, `{}`, `{}`)
		client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "cookies.txt", ChannelID: "UCmine"})
		if _, err := client.ListPlaylists(t.Context()); err == nil {
			t.Error("ListPlaylists() error = nil, want missing account data error")
		}
	})
}

func TestCookieAccountListTracksFillsFieldsAndSkipsUnavailable(t *testing.T) {
	const tracks = `{"_type":"playlist","entries":[
{"id":"song-a","title":"First song","channel":"Artist A","duration":192.5},
{"id":"gone","title":"Deleted video","availability":"unavailable"},
{"id":"hidden","title":"Private video","availability":"private"},
{"id":"song-b","title":"Second song","uploader":"Artist B","duration":75}]}`
	bin, args := stubCookieYtDlp(t, `{}`, `{}`, tracks)
	client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "cookies.txt", ChannelID: "UCmine"})
	got, err := client.ListTracks(t.Context(), "PLmine")
	if err != nil {
		t.Fatal(err)
	}
	want := []Track{
		{ID: "song-a", Title: "First song", Channel: "Artist A", URL: "https://www.youtube.com/watch?v=song-a", Duration: 192500 * time.Millisecond},
		{ID: "song-b", Title: "Second song", Channel: "Artist B", URL: "https://www.youtube.com/watch?v=song-b", Duration: 75 * time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTracks() = %+v, want %+v", got, want)
	}
	calls := readCookieCalls(t, args)
	if len(calls) != 1 || !strings.Contains(calls[0], "PLmine") || !strings.Contains(calls[0], "--cookies\ncookies.txt") {
		t.Errorf("yt-dlp calls = %q, want cookie-backed playlist lookup", calls)
	}
}

func TestCookieAccountListTracksCapsAt200(t *testing.T) {
	entries := make([]map[string]any, MaxPlaylistItems+5)
	for i := range entries {
		entries[i] = map[string]any{"id": fmt.Sprintf("video-%03d", i), "title": fmt.Sprintf("Title %03d", i), "channel": "Artist", "duration": 60}
	}
	dump, err := json.Marshal(map[string]any{"_type": "playlist", "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	bin, args := stubCookieYtDlp(t, `{}`, `{}`, string(dump))
	client := NewCookieAccountClient(CookieAccountConfig{Bin: bin, Cookies: "cookies.txt", ChannelID: "UCmine"})
	got, err := client.ListTracks(t.Context(), "PLlarge")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxPlaylistItems {
		t.Fatalf("ListTracks() count = %d, want 200", len(got))
	}
	if got[0].ID != "video-000" || got[MaxPlaylistItems-1].ID != "video-199" {
		t.Errorf("ListTracks() edges = %s/%s, want video-000/video-199", got[0].ID, got[len(got)-1].ID)
	}
	calls := readCookieCalls(t, args)
	if len(calls) != 1 || (!strings.Contains(calls[0], "--playlist-items\n1:200") && !strings.Contains(calls[0], "--playlist-end\n200") && !strings.Contains(calls[0], "-I\n1:200")) {
		t.Errorf("yt-dlp calls = %q, want request capped at 200", calls)
	}
}

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/internal/googleauth"
	"github.com/omegaatt36/ytea/internal/youtube"
)

type accountRoundTripFunc func(*http.Request) (*http.Response, error)

func (f accountRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSelectedOAuthSourceSendsBearerTokenToDataAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := googleauth.SaveToken(&googleauth.Token{
		AccessToken: "selected-access", RefreshToken: "selected-refresh",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// This package has no parallel tests. Restore the global transport before the next test.
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	var requests int
	const wantAuthorization = "Bearer selected-access"
	http.DefaultTransport = accountRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if got := req.Header.Get("Authorization"); got != wantAuthorization {
			t.Errorf("selected source Authorization = %q, want %q", got, wantAuthorization)
		}
		if req.URL.Scheme != "https" || !strings.HasSuffix(req.URL.Hostname(), "googleapis.com") {
			t.Errorf("selected source API URL = %q, want HTTPS Google API", req.URL)
		}
		response := httptest.NewRecorder()
		switch req.URL.Path {
		case "/youtube/v3/channels":
			fmt.Fprint(response, `{"items":[{"id":"UCmine","contentDetails":{"relatedPlaylists":{"likes":"LLmine"}}}]}`)
		case "/youtube/v3/playlists":
			fmt.Fprint(response, `{"items":[{"id":"PLmine","snippet":{"channelId":"UCmine","title":"Mine"}}]}`)
		default:
			t.Errorf("unexpected Data API path %q", req.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
		return response.Result(), nil
	})

	source, err := accountPlaylistSource(options{googleClientID: "client", googleClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if source == nil {
		t.Fatal("stored OAuth token produced no account source")
	}
	playlists, err := source.ListPlaylists(t.Context())
	if err != nil {
		t.Fatalf("selected source ListPlaylists() error = %v", err)
	}
	if requests != 2 || len(playlists) != 2 || playlists[0].ID != "PLmine" || playlists[1].ID != "LLmine" {
		t.Errorf("selected source requests = %d, playlists = %+v; want two API calls and owned/Liked playlists", requests, playlists)
	}
}

func TestAccountPlaylistSourcePrefersOAuthOverCookies(t *testing.T) {
	wantType := reflect.TypeFor[*youtube.AccountClient]()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := googleauth.SaveToken(&googleauth.Token{
		AccessToken: "oauth-access-secret", RefreshToken: "oauth-refresh-secret",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	cookies := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(cookies, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := accountPlaylistSource(options{
		cookies: cookies, googleClientID: "oauth-client", googleClientSecret: "oauth-client-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(source) != wantType {
		t.Errorf("source with OAuth token and cookies = %T, want YouTube Data API client", source)
	}
}

func TestAccountPlaylistSourceFallsBackToCookies(t *testing.T) {
	wantType := reflect.TypeOf(youtube.NewCookieAccountClient(youtube.CookieAccountConfig{}))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cookies := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(cookies, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := accountPlaylistSource(options{cookies: cookies, youtubeChannelID: "UCmine"})
	if err != nil {
		t.Fatal(err)
	}
	if source == nil {
		t.Fatal("source with cookies = nil, want cookie playlist source")
	}
	if reflect.TypeOf(source) != wantType {
		t.Errorf("source without OAuth token = %T, want %v", source, wantType)
	}
}

func TestAccountPlaylistSourceShowsMissingChannelIDErrorForCookies(t *testing.T) {
	for _, tt := range []struct {
		name               string
		cookieFile         bool
		cookiesFromBrowser string
	}{
		{name: "cookie file", cookieFile: true},
		{name: "browser cookies", cookiesFromBrowser: "firefox:default"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			var cookies string
			if tt.cookieFile {
				cookies = filepath.Join(t.TempDir(), "cookies.txt")
				if err := os.WriteFile(cookies, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source, err := accountPlaylistSource(options{
				cookies: cookies, cookiesFromBrowser: tt.cookiesFromBrowser,
			})
			if err != nil {
				t.Fatalf("selector aborted startup: %v", err)
			}
			if source == nil {
				t.Fatal("cookies without a channel ID produced no source; want a YouTube-section error")
			}
			if _, ok := source.(*youtube.AccountClient); ok {
				t.Fatalf("cookies without an OAuth token selected OAuth source %T", source)
			}
			_, err = source.ListPlaylists(t.Context())
			if err == nil || !strings.Contains(err.Error(), "youtube-channel-id") {
				t.Errorf("ListPlaylists error = %v, want guidance naming youtube-channel-id", err)
			}
		})
	}
}

func TestAccountPlaylistSourceAbsentWithoutCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	source, err := accountPlaylistSource(options{})
	if err != nil {
		t.Fatal(err)
	}
	if source != nil {
		t.Errorf("source without cookies or OAuth token = %T, want nil so YouTube section is hidden", source)
	}
}

func TestAccountPlaylistSourceKeepsUnusableOAuthVisibleWithoutCookieFallback(t *testing.T) {
	for _, tt := range []struct {
		name      string
		tokenJSON string
		clientID  string
		clientKey string
		wantHint  string
	}{
		{name: "malformed token", tokenJSON: "{", clientID: "client", clientKey: "secret", wantHint: "ytea auth login"},
		{name: "empty token", tokenJSON: "{}", clientID: "client", clientKey: "secret", wantHint: "ytea auth login"},
		{name: "missing client configuration", tokenJSON: `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z"}`, wantHint: "client id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			tokenPath, err := googleauth.TokenPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tokenPath, []byte(tt.tokenJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			cookies := filepath.Join(t.TempDir(), "cookies.txt")
			if err := os.WriteFile(cookies, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "yt-dlp-called")
			stub := filepath.Join(t.TempDir(), "yt-dlp")
			if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf called > '"+marker+"'\nexit 42\n"), 0o700); err != nil {
				t.Fatal(err)
			}

			source, err := accountPlaylistSource(options{
				cookies: cookies, ytdlpBin: stub, googleClientID: tt.clientID, googleClientSecret: tt.clientKey,
			})
			if err != nil {
				t.Fatalf("selector aborted startup for stored OAuth token: %v", err)
			}
			if source == nil {
				t.Fatal("stored OAuth token produced no source; want an error shown in the YouTube section")
			}
			_, err = source.ListPlaylists(t.Context())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.wantHint) {
				t.Errorf("ListPlaylists error = %v, want actionable guidance containing %q", err, tt.wantHint)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("cookie yt-dlp was invoked after unusable OAuth token; marker stat error = %v", err)
			}
		})
	}
}

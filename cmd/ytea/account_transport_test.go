package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/internal/googleauth"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestAccountDataAPIUsesStoredOrRefreshedBearerToken(t *testing.T) {
	for _, tt := range []struct {
		name          string
		expiry        time.Time
		wantBearer    string
		wantRefreshes int
	}{
		{name: "valid token", expiry: time.Now().Add(time.Hour), wantBearer: "Bearer stored-access"},
		{name: "expired token", expiry: time.Now().Add(-time.Hour), wantBearer: "Bearer fresh-access", wantRefreshes: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			var refreshes int
			config := authServerConfig(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/token" {
					t.Errorf("OAuth path = %q, want /token", r.URL.Path)
				}
				refreshes++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if got := r.Form.Get("refresh_token"); got != "stored-refresh" {
					t.Errorf("refresh token = %q, want stored-refresh", got)
				}
				fmt.Fprint(w, `{"access_token":"fresh-access","token_type":"Bearer","expires_in":3600}`)
			})
			config.ClientID = "test-client"
			config.ClientSecret = "test-secret"
			authClient, err := googleauth.NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != tt.wantBearer {
					t.Errorf("Data API Authorization = %q, want %q", got, tt.wantBearer)
				}
				switch r.URL.Path {
				case "/youtube/v3/channels":
					fmt.Fprint(w, `{"items":[{"id":"UCmine","contentDetails":{"relatedPlaylists":{"likes":"LLmine"}}}]}`)
				case "/youtube/v3/playlists":
					fmt.Fprint(w, `{"items":[{"id":"PLmine","snippet":{"channelId":"UCmine","title":"Mine"}}]}`)
				default:
					t.Errorf("unexpected Data API path %q", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(api.Close)
			stored := &googleauth.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh", Expiry: tt.expiry}
			if err := googleauth.SaveToken(stored); err != nil {
				t.Fatal(err)
			}
			httpClient := &http.Client{Transport: &oauthTransport{base: api.Client().Transport, token: authClient.TokenSource(stored)}}
			client := youtube.NewAccountClient(httpClient, api.URL+"/youtube/v3")
			playlists, err := client.ListPlaylists(t.Context())
			if err != nil {
				t.Fatalf("ListPlaylists() error = %v", err)
			}
			if len(playlists) != 2 || playlists[0].ID != "PLmine" || playlists[1].ID != "LLmine" {
				t.Errorf("playlists = %+v, want owned playlist and Liked videos", playlists)
			}
			if refreshes != tt.wantRefreshes {
				t.Errorf("refresh requests = %d, want %d", refreshes, tt.wantRefreshes)
			}
			loaded, err := googleauth.LoadToken()
			if err != nil {
				t.Fatal(err)
			}
			wantAccess := "stored-access"
			if tt.wantRefreshes != 0 {
				wantAccess = "fresh-access"
			}
			if loaded.AccessToken != wantAccess || loaded.RefreshToken != "stored-refresh" {
				t.Errorf("persisted token = %+v, want access %q and original refresh token", loaded, wantAccess)
			}
		})
	}
}

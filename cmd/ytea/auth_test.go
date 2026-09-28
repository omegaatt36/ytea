package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/omegaatt36/ytea/internal/googleauth"
)

func runAuthCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	cmd := newCommand(func(context.Context, options) error {
		t.Error("auth subcommand started the player")
		return nil
	})
	cmd.Writer = &output
	cmd.ErrWriter = &output
	err := cmd.Run(t.Context(), append([]string{appName}, args...))
	return output.String(), err
}

func authServerConfig(t *testing.T, handler http.HandlerFunc) googleauth.Config {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return googleauth.Config{
		HTTPClient:    server.Client(),
		DeviceAuthURL: server.URL + "/device",
		TokenURL:      server.URL + "/token",
	}
}

func TestAuthLoginMissingClientCredentials(t *testing.T) {
	for _, tt := range []struct {
		name    string
		config  string
		missing []string
	}{
		{name: "both missing", missing: []string{"google-client-id"}},
		{name: "ID missing", config: "google-client-secret = \"secret\"\n", missing: []string{"google-client-id"}},
		{name: "secret missing", config: "google-client-id = \"client\"\n", missing: []string{"google-client-secret"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv("YTEA_GOOGLE_CLIENT_ID", "")
			t.Setenv("YTEA_GOOGLE_CLIENT_SECRET", "")
			if tt.config != "" {
				writeConfig(t, home, tt.config)
			}
			output, err := runAuthCommand(t, "auth", "login")
			if err == nil {
				t.Fatal("auth login succeeded without both client credentials")
			}
			message := output + err.Error()
			for _, missing := range tt.missing {
				if !strings.Contains(message, missing) {
					t.Errorf("error %q does not name missing setting %q", message, missing)
				}
			}
			if !strings.Contains(message, "TVs and Limited Input devices") {
				t.Errorf("error %q does not explain the required client type", message)
			}
			path, err := googleauth.TokenPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("token after missing credentials: stat error = %v, want not exist", err)
			}
		})
	}
}

func TestAuthLoginPrintsCodeAndStoresApprovedToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var tokenRequests int
	config := authServerConfig(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse OAuth request: %v", err)
		}
		switch r.URL.Path {
		case "/device":
			if got := r.Form.Get("client_id"); got != "cli-client" {
				t.Errorf("device client_id = %q, want cli-client", got)
			}
			fmt.Fprint(w, `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_url":"https://www.google.com/device","expires_in":60,"interval":1}`)
		case "/token":
			tokenRequests++
			if got := r.Form.Get("client_secret"); got != "cli-secret" {
				t.Errorf("token client_secret = %q, want cli-secret", got)
			}
			if tokenRequests == 1 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"approved-access","refresh_token":"approved-refresh","token_type":"Bearer","expires_in":3600}`)
		default:
			t.Errorf("unexpected OAuth path %q", r.URL.Path)
		}
	})

	var output bytes.Buffer
	err := authLogin(t.Context(), options{googleClientID: "cli-client", googleClientSecret: "cli-secret"}, &output, config)
	if err != nil {
		t.Fatalf("auth login: %v; output: %s", err, output.String())
	}
	for _, want := range []string{"https://www.google.com/device", "ABCD-EFGH"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("login output %q does not contain %q", output.String(), want)
		}
	}
	if tokenRequests < 2 {
		t.Errorf("token requests = %d, want polling before approval", tokenRequests)
	}
	token, err := googleauth.LoadToken()
	if err != nil {
		t.Fatalf("load approved token: %v", err)
	}
	if token.AccessToken != "approved-access" || token.RefreshToken != "approved-refresh" {
		t.Errorf("stored token = %+v, want approved credentials", token)
	}
}

func TestAuthLoginDenialAndExpiryDoNotWriteToken(t *testing.T) {
	for _, rejection := range []string{"access_denied", "expired_token"} {
		t.Run(rejection, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			config := authServerConfig(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/device":
					fmt.Fprint(w, `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_url":"https://www.google.com/device","expires_in":60,"interval":1}`)
				case "/token":
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"error":%q}`, rejection)
				default:
					t.Errorf("unexpected OAuth path %q", r.URL.Path)
				}
			})
			var output bytes.Buffer
			if err := authLogin(t.Context(), options{googleClientID: "cli-client", googleClientSecret: "cli-secret"}, &output, config); err == nil {
				t.Fatalf("auth login succeeded for %s; output: %s", rejection, output.String())
			}
			for _, want := range []string{"https://www.google.com/device", "ABCD-EFGH"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("login output %q does not contain %q before %s", output.String(), want, rejection)
				}
			}
			path, err := googleauth.TokenPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("token after %s: stat error = %v, want not exist", rejection, err)
			}
		})
	}
}

func TestAuthLogoutDeletesTokenAndShowsRevokeHint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := googleauth.SaveToken(&googleauth.Token{AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	path, err := googleauth.TokenPath()
	if err != nil {
		t.Fatal(err)
	}
	output, err := runAuthCommand(t, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v; output: %s", err, output)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("token after logout: stat error = %v, want not exist", err)
	}
	if !strings.Contains(output, "myaccount.google.com") || !strings.Contains(strings.ToLower(output), "revoke") {
		t.Errorf("logout output %q does not point to Google account third-party access", output)
	}
}

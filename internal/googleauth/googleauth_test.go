package googleauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/internal/googleauth"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*googleauth.Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := googleauth.NewClient(googleauth.Config{
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		HTTPClient:    server.Client(),
		DeviceAuthURL: server.URL + "/device",
		TokenURL:      server.URL + "/token",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client, server
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestDeviceLoginDisplaysCodeAndStoresApprovedToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var tokenRequests atomic.Int32
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		switch r.URL.Path {
		case "/device":
			if got := r.Form.Get("client_id"); got != "test-client" {
				t.Errorf("device client_id = %q", got)
			}
			if got := r.Form.Get("scope"); got != "https://www.googleapis.com/auth/youtube.readonly" {
				t.Errorf("device scope = %q", got)
			}
			fmt.Fprint(w, `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_url":"https://www.google.com/device","expires_in":60,"interval":1}`)
		case "/token":
			requestNumber := tokenRequests.Add(1)
			if got := r.Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("token grant_type = %q", got)
			}
			if got := r.Form.Get("device_code"); got != "device-secret" {
				t.Errorf("device_code = %q", got)
			}
			if requestNumber == 1 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"approved-access","refresh_token":"approved-refresh","token_type":"Bearer","expires_in":3600}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, err := client.Login(ctx, &output)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if token.AccessToken != "approved-access" || token.RefreshToken != "approved-refresh" {
		t.Errorf("Login() token = %+v", token)
	}
	if got := tokenRequests.Load(); got < 2 {
		t.Errorf("token polls = %d, want pending followed by approval", got)
	}
	for _, want := range []string{"https://www.google.com/device", "ABCD-EFGH"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("login instructions %q do not contain %q", output.String(), want)
		}
	}
	stored, err := googleauth.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if stored.AccessToken != "approved-access" || stored.RefreshToken != "approved-refresh" {
		t.Errorf("stored token = %+v", stored)
	}
}

func TestDeviceLoginRejectionDoesNotWriteToken(t *testing.T) {
	for _, rejection := range []string{"access_denied", "expired_token"} {
		t.Run(rejection, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/device":
					fmt.Fprint(w, `{"device_code":"code","user_code":"ABCD","verification_url":"https://www.google.com/device","expires_in":60,"interval":1}`)
				case "/token":
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"error":%q}`, rejection)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Login(ctx, &bytes.Buffer{}); err == nil {
				t.Fatal("Login() error = nil, want rejection")
			}
			path, err := googleauth.TokenPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("token path after rejection: stat error = %v, want not exist", err)
			}
		})
	}
}

func TestDeviceLoginExpiryStopsStalledTokenRequest(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tokenRequested := make(chan struct{}, 1)
	releaseResponse := make(chan struct{})
	defer close(releaseResponse)
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device":
			fmt.Fprint(w, `{"device_code":"code","user_code":"ABCD","verification_url":"https://www.google.com/device","expires_in":2,"interval":1}`)
		case "/token":
			tokenRequested <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-releaseResponse:
			}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	token, err := client.Login(ctx, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("Login() = (%+v, %v), want device code expiry", token, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Login() took %v, want device expiry to bound stalled token response", elapsed)
	}
	select {
	case <-tokenRequested:
	default:
		t.Error("token request was not made; stalled response was not exercised")
	}
	path, err := googleauth.TokenPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("token path after expiry: stat error = %v, want not exist", err)
	}
}

func TestDeviceLoginBoundsInitialAuthorizationRequest(t *testing.T) {
	client, err := googleauth.NewClient(googleauth.Config{
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		DeviceAuthURL: "https://example.test/device",
		TokenURL:      "https://example.test/token",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			deadline, ok := request.Context().Deadline()
			if !ok || time.Until(deadline) > time.Minute {
				t.Errorf("device request deadline = %v, present = %t; want a finite request bound of at most one minute", deadline, ok)
			}
			return nil, errors.New("stop after checking request deadline")
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(context.Background(), &bytes.Buffer{}); err == nil {
		t.Fatal("Login() error = nil, want request error")
	}
}

func TestDeviceLoginRejectsIncompleteApprovedToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing refresh token", `{"access_token":"access","expires_in":3600}`},
		{"missing expiry", `{"access_token":"access","refresh_token":"refresh"}`},
		{"zero expiry", `{"access_token":"access","refresh_token":"refresh","expires_in":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/device":
					fmt.Fprint(w, `{"device_code":"code","user_code":"ABCD","verification_url":"https://www.google.com/device","expires_in":60,"interval":1}`)
				case "/token":
					fmt.Fprint(w, tc.body)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if token, err := client.Login(ctx, &bytes.Buffer{}); err == nil {
				t.Errorf("Login() = (%+v, nil), want invalid token error", token)
			}
			path, err := googleauth.TokenPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("token path after invalid response: stat error = %v, want not exist", err)
			}
		})
	}
}

func TestTokenPathModeAndRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	path, err := googleauth.TokenPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(configDir, "ytea", "google-token.json"); path != want {
		t.Fatalf("TokenPath() = %q, want %q", path, want)
	}
	want := &googleauth.Token{AccessToken: "access-secret", RefreshToken: "refresh-secret", Expiry: time.Now().Add(time.Hour).Truncate(time.Second)}
	if err := googleauth.SaveToken(want); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token mode = %#o, want 0600", got)
	}
	got, err := googleauth.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || !got.Expiry.Equal(want.Expiry) {
		t.Errorf("LoadToken() = %+v, want %+v", got, want)
	}
}

func TestTokenReplacementIsAtomicForReaders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := googleauth.TokenPath()
	if err != nil {
		t.Fatal(err)
	}
	old := &googleauth.Token{AccessToken: strings.Repeat("a", 256*1024), RefreshToken: "old"}
	newToken := &googleauth.Token{AccessToken: strings.Repeat("b", 256*1024), RefreshToken: "new"}
	if err := googleauth.SaveToken(old); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	readerErrors := make(chan error, 1)
	wg.Go(func() {
		for range 100 {
			data, err := os.ReadFile(path)
			if err != nil {
				readerErrors <- err
				return
			}
			var token googleauth.Token
			if err := json.Unmarshal(data, &token); err != nil {
				readerErrors <- fmt.Errorf("reader saw incomplete JSON: %w", err)
				return
			}
			if token.AccessToken != old.AccessToken && token.AccessToken != newToken.AccessToken {
				readerErrors <- fmt.Errorf("reader saw partial token of length %d", len(token.AccessToken))
				return
			}
		}
	})
	for range 10 {
		if err := googleauth.SaveToken(newToken); err != nil {
			t.Fatal(err)
		}
		if err := googleauth.SaveToken(old); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	select {
	case err := <-readerErrors:
		t.Fatal(err)
	default:
	}
}

func TestExpiredTokenRefreshesAndPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var refreshCalls atomic.Int32
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		if r.URL.Path != "/token" {
			t.Errorf("refresh path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("refresh grant_type = %q", got)
		}
		if got := r.Form.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh_token = %q", got)
		}
		fmt.Fprint(w, `{"access_token":"fresh-access","token_type":"Bearer","expires_in":3600}`)
	})
	initial := &googleauth.Token{AccessToken: "expired-access", RefreshToken: "old-refresh", Expiry: time.Now().Add(-time.Hour)}
	if err := googleauth.SaveToken(initial); err != nil {
		t.Fatal(err)
	}
	got, err := client.TokenSource(initial).Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if got.AccessToken != "fresh-access" || got.RefreshToken != "old-refresh" || !got.Expiry.After(time.Now()) {
		t.Errorf("refreshed token = %+v", got)
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Errorf("refresh calls = %d, want 1", got)
	}
	stored, err := googleauth.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "fresh-access" || stored.RefreshToken != "old-refresh" {
		t.Errorf("stored refreshed token = %+v", stored)
	}
}

func TestInvalidGrantIsClassifiedAndKeepsStoredToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	})
	initial := &googleauth.Token{AccessToken: "expired-access", RefreshToken: "revoked-refresh", Expiry: time.Now().Add(-time.Hour)}
	if err := googleauth.SaveToken(initial); err != nil {
		t.Fatal(err)
	}
	_, err := client.TokenSource(initial).Token()
	if !errors.Is(err, googleauth.ErrInvalidGrant) {
		t.Fatalf("Token() error = %v, want ErrInvalidGrant", err)
	}
	stored, err := googleauth.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != initial.AccessToken || stored.RefreshToken != initial.RefreshToken {
		t.Errorf("failed refresh changed stored token: %+v", stored)
	}
}

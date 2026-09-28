// Package googleauth manages YouTube OAuth device authorization and stored tokens.
package googleauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultDeviceAuthURL = "https://oauth2.googleapis.com/device/code"
	defaultTokenURL      = "https://oauth2.googleapis.com/token"
	youtubeReadonlyScope = "https://www.googleapis.com/auth/youtube.readonly"
	refreshTimeout       = 30 * time.Second
	deviceRequestTimeout = 30 * time.Second
)

var ErrInvalidGrant = errors.New("google authorization expired or revoked; run `ytea auth login` again")

type Config struct {
	ClientID      string
	ClientSecret  string
	HTTPClient    *http.Client
	DeviceAuthURL string
	TokenURL      string
}

type Client struct {
	clientID      string
	clientSecret  string
	httpClient    *http.Client
	deviceAuthURL string
	tokenURL      string
}

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.ClientID) == "" {
		return nil, errors.New("missing Google client ID; create a TVs and Limited Input devices OAuth client")
	}
	if strings.TrimSpace(config.ClientSecret) == "" {
		return nil, errors.New("missing Google client secret; create a TVs and Limited Input devices OAuth client")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.DeviceAuthURL == "" {
		config.DeviceAuthURL = defaultDeviceAuthURL
	}
	if config.TokenURL == "" {
		config.TokenURL = defaultTokenURL
	}
	for _, endpoint := range []string{config.DeviceAuthURL, config.TokenURL} {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errors.New("invalid Google OAuth endpoint URL")
		}
	}
	return &Client{config.ClientID, config.ClientSecret, config.HTTPClient, config.DeviceAuthURL, config.TokenURL}, nil
}

func TokenPath() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "ytea", "google-token.json"), nil
}

func SaveToken(token *Token) error {
	if token == nil {
		return errors.New("save Google token: token is nil")
	}
	path, err := TokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Google token directory: %w", err)
	}
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode Google token: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".google-token-*")
	if err != nil {
		return fmt.Errorf("create temporary Google token file: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("secure Google token file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write Google token: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync Google token: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Google token: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace Google token: %w", err)
	}
	return nil
}

func LoadToken() (*Token, error) {
	path, err := TokenPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Google token: %w", err)
	}
	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("decode Google token: %w", err)
	}
	return &token, nil
}

type deviceAuthorization struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

func (c *Client) postForm(ctx context.Context, endpoint string, values url.Values, result any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return 0, fmt.Errorf("create Google OAuth request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("send Google OAuth request: %w", err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result); err != nil {
		return 0, fmt.Errorf("decode Google OAuth response (HTTP %d): %w", response.StatusCode, err)
	}
	return response.StatusCode, nil
}

func (c *Client) Login(ctx context.Context, output io.Writer) (*Token, error) {
	var device deviceAuthorization
	requestCtx, cancelRequest := context.WithTimeout(ctx, deviceRequestTimeout)
	status, err := c.postForm(requestCtx, c.deviceAuthURL, url.Values{
		"client_id": {c.clientID},
		"scope":     {youtubeReadonlyScope},
	}, &device)
	cancelRequest()
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || device.DeviceCode == "" || device.UserCode == "" || device.VerificationURL == "" || device.ExpiresIn <= 0 {
		return nil, fmt.Errorf("request Google device authorization: invalid response (HTTP %d)", status)
	}
	if _, err := fmt.Fprintf(output, "Visit %s and enter code %s\n", device.VerificationURL, device.UserCode); err != nil {
		return nil, fmt.Errorf("display Google device instructions: %w", err)
	}
	interval := time.Duration(max(device.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	pollCtx, cancelPoll := context.WithDeadline(ctx, deadline)
	defer cancelPoll()
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, errors.New("device code expired")
		}
		timer := time.NewTimer(min(interval, remaining))
		select {
		case <-pollCtx.Done():
			timer.Stop()
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("wait for Google authorization: %w", err)
			}
			return nil, errors.New("device code expired")
		case <-timer.C:
		}
		var response tokenResponse
		status, err := c.postForm(pollCtx, c.tokenURL, url.Values{
			"client_id":     {c.clientID},
			"client_secret": {c.clientSecret},
			"device_code":   {device.DeviceCode},
			"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &response)
		if err != nil {
			if pollCtx.Err() != nil && ctx.Err() == nil {
				return nil, errors.New("device code expired")
			}
			return nil, err
		}
		if status == http.StatusOK && response.AccessToken != "" {
			if response.RefreshToken == "" || response.ExpiresIn <= 0 {
				return nil, errors.New("poll Google device authorization: incomplete approved token")
			}
			token := &Token{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, Expiry: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)}
			if err := SaveToken(token); err != nil {
				return nil, err
			}
			return token, nil
		}
		switch response.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			return nil, errors.New("device authorization denied")
		case "expired_token":
			return nil, errors.New("device code expired")
		default:
			return nil, fmt.Errorf("poll Google device authorization: %s (HTTP %d)", response.Error, status)
		}
	}
}

type Source struct {
	client *Client
	mu     sync.Mutex
	token  Token
}

func (c *Client) TokenSource(token *Token) *Source {
	source := &Source{client: c}
	if token != nil {
		source.token = *token
	}
	return source
}

func (source *Source) Token() (*Token, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.token.AccessToken != "" && time.Until(source.token.Expiry) > 30*time.Second {
		return new(source.token), nil
	}
	if source.token.RefreshToken == "" {
		return nil, errors.New("missing Google refresh token; run `ytea auth login` again")
	}
	// Token has no context parameter, so bound the network call while the source is locked.
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	var response tokenResponse
	status, err := source.client.postForm(ctx, source.client.tokenURL, url.Values{
		"client_id":     {source.client.clientID},
		"client_secret": {source.client.clientSecret},
		"refresh_token": {source.token.RefreshToken},
		"grant_type":    {"refresh_token"},
	}, &response)
	if err != nil {
		return nil, err
	}
	if response.Error == "invalid_grant" {
		return nil, ErrInvalidGrant
	}
	if status != http.StatusOK || response.AccessToken == "" || response.ExpiresIn <= 0 {
		return nil, fmt.Errorf("refresh Google token: %s (HTTP %d)", response.Error, status)
	}
	refreshed := Token{AccessToken: response.AccessToken, RefreshToken: source.token.RefreshToken, Expiry: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)}
	if response.RefreshToken != "" {
		refreshed.RefreshToken = response.RefreshToken
	}
	if err := SaveToken(&refreshed); err != nil {
		return nil, err
	}
	source.token = refreshed
	return new(refreshed), nil
}

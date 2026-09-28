package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/omegaatt36/ytea/internal/googleauth"
	"github.com/omegaatt36/ytea/internal/tui"
	"github.com/omegaatt36/ytea/internal/youtube"
)

// accountPlaylistSource selects one read-only source for the account section.
func accountPlaylistSource(opts options) (tui.AccountPlaylistSource, error) {
	token, err := googleauth.LoadToken()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return &accountPlaylistErrorSource{fmt.Errorf("load Google authorization; check the token file or run `ytea auth login` again: %w", err)}, nil
	}
	if err == nil {
		if token.AccessToken == "" && token.RefreshToken == "" {
			return &accountPlaylistErrorSource{errors.New("saved Google token is empty; run `ytea auth login` again")}, nil
		}
		client, err := googleauth.NewClient(googleauth.Config{
			ClientID: opts.googleClientID, ClientSecret: opts.googleClientSecret,
		})
		if err != nil {
			return &accountPlaylistErrorSource{fmt.Errorf("configure Google authorization for saved token: %w", err)}, nil
		}
		httpClient := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &oauthTransport{
				base: http.DefaultTransport, token: client.TokenSource(token),
			},
		}
		return youtube.NewAccountClient(httpClient, "https://www.googleapis.com/youtube/v3"), nil
	}
	if opts.cookies != "" || opts.cookiesFromBrowser != "" {
		if opts.youtubeChannelID == "" {
			return &accountPlaylistErrorSource{errors.New("set youtube-channel-id to list account playlists with cookies")}, nil
		}
		return youtube.NewCookieAccountClient(youtube.CookieAccountConfig{
			Bin:                opts.ytdlpBin,
			Cookies:            opts.cookies,
			CookiesFromBrowser: opts.cookiesFromBrowser,
			ChannelID:          opts.youtubeChannelID,
		}), nil
	}
	return nil, nil
}

// accountPlaylistErrorSource keeps the account section visible while local use continues.
type accountPlaylistErrorSource struct{ err error }

func (source *accountPlaylistErrorSource) ListPlaylists(context.Context) ([]youtube.AccountPlaylist, error) {
	return nil, source.err
}

func (source *accountPlaylistErrorSource) ListTracks(context.Context, string) ([]youtube.Track, error) {
	return nil, source.err
}

type oauthTransport struct {
	base  http.RoundTripper
	token *googleauth.Source
}

func (transport *oauthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	token, err := transport.token.Token()
	if err != nil {
		return nil, fmt.Errorf("authorize YouTube request: %w", err)
	}
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return transport.base.RoundTrip(copy)
}

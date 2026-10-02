package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

// CookieAccountConfig identifies the signed-in yt-dlp session and its channel.
type CookieAccountConfig struct {
	Bin                string
	Cookies            string
	CookiesFromBrowser string
	ChannelID          string
}

// CookieAccountClient lists account playlists through yt-dlp.
type CookieAccountClient struct {
	config CookieAccountConfig
}

func NewCookieAccountClient(config CookieAccountConfig) *CookieAccountClient {
	if config.Bin == "" {
		config.Bin = "yt-dlp"
	}
	return &CookieAccountClient{config: config}
}

type cookiePlaylistDump struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	ChannelID string                `json:"channel_id"`
	Entries   []cookiePlaylistEntry `json:"entries"`
}

type cookiePlaylistEntry struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	ChannelID    string  `json:"channel_id"`
	Channel      string  `json:"channel"`
	Uploader     string  `json:"uploader"`
	Duration     float64 `json:"duration"`
	Availability string  `json:"availability"`
	IEKey        string  `json:"ie_key"`
}

// ListPlaylists returns playlists owned by ChannelID and the account's Liked videos.
func (c *CookieAccountClient) ListPlaylists(ctx context.Context) ([]domain.AccountPlaylist, error) {
	if c.config.ChannelID == "" {
		return nil, fmt.Errorf("list account playlists: youtube channel ID is required")
	}
	var feed cookiePlaylistDump
	if err := c.dump(ctx, "https://www.youtube.com/feed/playlists", []string{"--flat-playlist"}, &feed); err != nil {
		return nil, fmt.Errorf("list account playlists: %w", err)
	}
	playlists := make([]domain.AccountPlaylist, 0, len(feed.Entries)+1)
	seen := make(map[string]bool, len(feed.Entries))
	feedHasLikes := false
	for _, entry := range feed.Entries {
		if entry.ID == "LL" {
			feedHasLikes = true
			continue
		}
		if entry.ID == "" || entry.ID == "LM" || seen[entry.ID] {
			continue
		}
		owner := entry.ChannelID
		if owner == "" {
			var detail cookiePlaylistDump
			if err := c.dump(ctx, playlistURL(entry.ID), []string{"--flat-playlist", "--playlist-items", "0"}, &detail); err != nil {
				return nil, fmt.Errorf("resolve playlist %q owner: %w", entry.ID, err)
			}
			owner = detail.ChannelID
		}
		if owner != c.config.ChannelID {
			continue
		}
		seen[entry.ID] = true
		playlists = append(playlists, domain.AccountPlaylist{ID: entry.ID, Title: entry.Title, URL: playlistURL(entry.ID)})
	}
	var likes cookiePlaylistDump
	if err := c.dump(ctx, "https://www.youtube.com/playlist?list=LL", []string{"--flat-playlist", "--playlist-items", "0"}, &likes); err != nil {
		return nil, fmt.Errorf("list liked videos: %w", err)
	}
	hasLikes := feedHasLikes || strings.HasPrefix(likes.ID, "LL")
	if hasLikes {
		playlists = append(playlists, domain.AccountPlaylist{ID: "LL", Title: "Liked videos", URL: playlistURL("LL")})
	}
	if len(feed.Entries) == 0 && !hasLikes {
		return nil, fmt.Errorf("list account playlists: yt-dlp returned no account data; check cookies")
	}
	return playlists, nil
}

// ListTracks returns at most MaxPlaylistItems available videos in playlist order.
func (c *CookieAccountClient) ListTracks(ctx context.Context, playlistID string) ([]domain.Track, error) {
	var dump cookiePlaylistDump
	if err := c.dump(ctx, playlistURL(playlistID), []string{"--flat-playlist", "--playlist-items", fmt.Sprintf("1:%d", domain.MaxPlaylistItems)}, &dump); err != nil {
		return nil, fmt.Errorf("list playlist %q tracks: %w", playlistID, err)
	}
	tracks := make([]domain.Track, 0, min(len(dump.Entries), domain.MaxPlaylistItems))
	for i, entry := range dump.Entries {
		if i == domain.MaxPlaylistItems {
			break
		}
		if entry.ID == "" || entry.Title == "Deleted video" || entry.Title == "Private video" || entry.Availability == "unavailable" || entry.Availability == "private" || entry.Availability == "needs_auth" || (entry.IEKey != "" && entry.IEKey != "Youtube") {
			continue
		}
		channel := entry.Channel
		if channel == "" {
			channel = entry.Uploader
		}
		tracks = append(tracks, domain.Track{
			ID: entry.ID, Title: entry.Title, Channel: channel,
			URL:      "https://www.youtube.com/watch?v=" + url.QueryEscape(entry.ID),
			Duration: time.Duration(entry.Duration * float64(time.Second)),
		})
	}
	return tracks, nil
}

func (c *CookieAccountClient) dump(ctx context.Context, target string, extra []string, destination any) error {
	args := []string{target, "--dump-single-json", "--no-warnings"}
	if c.config.CookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", c.config.CookiesFromBrowser)
	} else if c.config.Cookies != "" {
		args = append(args, "--cookies", c.config.Cookies)
	}
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, c.config.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if message := stderrSummary(stderr.Bytes()); message != "" {
			return fmt.Errorf("run yt-dlp %q: %w: %s", target, err, message)
		}
		return fmt.Errorf("run yt-dlp %q: %w", target, err)
	}
	if err := json.Unmarshal(stdout.Bytes(), destination); err != nil {
		return fmt.Errorf("decode yt-dlp %q: %w", target, err)
	}
	return nil
}

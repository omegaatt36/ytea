package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

// AccountClient reads account playlists through the YouTube Data API.
// Its HTTP client supplies the OAuth transport.
type AccountClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewAccountClient creates a Data API client using the caller's HTTP transport.
func NewAccountClient(httpClient *http.Client, baseURL string) *AccountClient {
	return &AccountClient{httpClient: httpClient, baseURL: strings.TrimRight(baseURL, "/")}
}

type accountChannelPage struct {
	Items []struct {
		ID             string `json:"id"`
		ContentDetails struct {
			RelatedPlaylists struct {
				Likes string `json:"likes"`
			} `json:"relatedPlaylists"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type accountPlaylistPage struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []struct {
		ID      string `json:"id"`
		Snippet struct {
			ChannelID string `json:"channelId"`
			Title     string `json:"title"`
		} `json:"snippet"`
	} `json:"items"`
}

// ListPlaylists returns owned playlists and the channel's Liked videos playlist.
func (c *AccountClient) ListPlaylists(ctx context.Context) ([]domain.AccountPlaylist, error) {
	var channels accountChannelPage
	if err := c.get(ctx, "channels", url.Values{"part": {"contentDetails"}, "mine": {"true"}}, &channels); err != nil {
		return nil, fmt.Errorf("list account channels: %w", err)
	}
	if len(channels.Items) == 0 {
		return []domain.AccountPlaylist{}, nil
	}
	owner := channels.Items[0].ID
	likes := channels.Items[0].ContentDetails.RelatedPlaylists.Likes
	playlists := make([]domain.AccountPlaylist, 0)
	seen := make(map[string]bool)
	pageToken := ""
	for {
		query := url.Values{"part": {"snippet"}, "mine": {"true"}, "maxResults": {"50"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var page accountPlaylistPage
		if err := c.get(ctx, "playlists", query, &page); err != nil {
			return nil, fmt.Errorf("list owned playlists: %w", err)
		}
		for _, item := range page.Items {
			if item.ID == "" || item.ID == "LM" || item.Snippet.ChannelID != owner || item.ID == likes || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			playlists = append(playlists, domain.AccountPlaylist{ID: item.ID, Title: item.Snippet.Title, URL: playlistURL(item.ID)})
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	if likes != "" {
		playlists = append(playlists, domain.AccountPlaylist{ID: likes, Title: "Liked videos", URL: playlistURL(likes)})
	}
	return playlists, nil
}

func playlistURL(id string) string {
	return "https://www.youtube.com/playlist?list=" + url.QueryEscape(id)
}

type accountPlaylistItemsPage struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []struct {
		Snippet struct {
			Title                  string `json:"title"`
			VideoOwnerChannelTitle string `json:"videoOwnerChannelTitle"`
		} `json:"snippet"`
		ContentDetails struct {
			VideoID string `json:"videoId"`
		} `json:"contentDetails"`
		Status struct {
			PrivacyStatus string `json:"privacyStatus"`
		} `json:"status"`
	} `json:"items"`
}

type accountVideosPage struct {
	Items []struct {
		ID             string `json:"id"`
		ContentDetails struct {
			Duration string `json:"duration"`
		} `json:"contentDetails"`
	} `json:"items"`
}

// ListTracks returns up to MaxPlaylistItems available videos in playlist order.
func (c *AccountClient) ListTracks(ctx context.Context, playlistID string) ([]domain.Track, error) {
	tracks := make([]domain.Track, 0)
	pageToken := ""
	seenItems := 0
	for seenItems < domain.MaxPlaylistItems {
		query := url.Values{"part": {"snippet,contentDetails,status"}, "playlistId": {playlistID}, "maxResults": {"50"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var page accountPlaylistItemsPage
		if err := c.get(ctx, "playlistItems", query, &page); err != nil {
			return nil, fmt.Errorf("list playlist %q items: %w", playlistID, err)
		}
		for _, item := range page.Items {
			if seenItems == domain.MaxPlaylistItems {
				break
			}
			seenItems++
			id := item.ContentDetails.VideoID
			title := item.Snippet.Title
			if id == "" || item.Status.PrivacyStatus == "private" || title == "Deleted video" || title == "Private video" {
				continue
			}
			tracks = append(tracks, domain.Track{ID: id, Title: title, Channel: item.Snippet.VideoOwnerChannelTitle, URL: "https://www.youtube.com/watch?v=" + url.QueryEscape(id)})
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}

	// videos.list accepts at most 50 IDs. An omitted video is no longer available.
	available := make(map[string]time.Duration, len(tracks))
	for start := 0; start < len(tracks); start += 50 {
		end := min(start+50, len(tracks))
		ids := make([]string, 0, end-start)
		for _, track := range tracks[start:end] {
			ids = append(ids, track.ID)
		}
		var page accountVideosPage
		if err := c.get(ctx, "videos", url.Values{"part": {"contentDetails"}, "id": {strings.Join(ids, ",")}}, &page); err != nil {
			return nil, fmt.Errorf("lookup playlist video durations: %w", err)
		}
		for _, video := range page.Items {
			duration, err := parseAPIDuration(video.ContentDetails.Duration)
			if err != nil {
				return nil, fmt.Errorf("parse video %q duration: %w", video.ID, err)
			}
			available[video.ID] = duration
		}
	}
	result := make([]domain.Track, 0, len(tracks))
	for _, track := range tracks {
		duration, ok := available[track.ID]
		if !ok {
			continue
		}
		track.Duration = duration
		result = append(result, track)
	}
	return result, nil
}

var apiDurationPattern = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?)?$`)

func parseAPIDuration(value string) (time.Duration, error) {
	parts := apiDurationPattern.FindStringSubmatch(value)
	if parts == nil || value == "P" || value == "PT" {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", value)
	}
	units := []float64{7 * 24 * 60 * 60, 24 * 60 * 60, 60 * 60, 60, 1}
	var seconds float64
	for i, part := range parts[1:] {
		if part == "" {
			continue
		}
		amount, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid ISO 8601 duration %q: %w", value, err)
		}
		seconds += amount * units[i]
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (c *AccountClient) get(ctx context.Context, resource string, query url.Values, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+resource+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("create %s request: %w", resource, err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request %s: %w", resource, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("request %s: HTTP %d: %s", resource, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode %s response: %w", resource, err)
	}
	return nil
}

package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The server returns documented YouTube Data API v3 resource shapes. The client
// receives an HTTP client so OAuth transport can be supplied by its caller.
func accountClientForTest(t *testing.T, handler http.HandlerFunc) *AccountClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewAccountClient(server.Client(), server.URL+"/youtube/v3")
}

func writeAPIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode API response: %v", err)
	}
}

func TestAccountClientListPlaylistsOwnsAndLikes(t *testing.T) {
	var requests []string
	client := accountClientForTest(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Query().Get("mine") != "true" {
			t.Errorf("%s mine = %q, want true", r.URL.Path, r.URL.Query().Get("mine"))
		}
		switch r.URL.Path {
		case "/youtube/v3/channels":
			if !strings.Contains(r.URL.Query().Get("part"), "contentDetails") {
				t.Errorf("channels part = %q, want contentDetails", r.URL.Query().Get("part"))
			}
			writeAPIJSON(t, w, map[string]any{"items": []any{
				map[string]any{"id": "UCowner", "contentDetails": map[string]any{
					"relatedPlaylists": map[string]any{"likes": "LLowner", "uploads": "UUowner"},
				}},
			}})
		case "/youtube/v3/playlists":
			if !strings.Contains(r.URL.Query().Get("part"), "snippet") {
				t.Errorf("playlists part = %q, want snippet", r.URL.Query().Get("part"))
			}
			switch r.URL.Query().Get("pageToken") {
			case "":
				writeAPIJSON(t, w, map[string]any{"nextPageToken": "page2", "items": []any{
					map[string]any{"id": "PLown1", "snippet": map[string]any{"channelId": "UCowner", "title": "Owned one"}},
					map[string]any{"id": "PLsaved", "snippet": map[string]any{"channelId": "UCother", "title": "Saved from another channel"}},
				}})
			case "page2":
				writeAPIJSON(t, w, map[string]any{"items": []any{
					map[string]any{"id": "PLown2", "snippet": map[string]any{"channelId": "UCowner", "title": "Owned two"}},
					map[string]any{"id": "LM", "snippet": map[string]any{"channelId": "UCowner", "title": "Musique aimée"}},
					map[string]any{"id": "LMmusic", "snippet": map[string]any{"channelId": "UCmusic", "title": "Liked Music"}},
				}})
			default:
				t.Errorf("unexpected playlist page token %q", r.URL.Query().Get("pageToken"))
				http.Error(w, "unexpected page", http.StatusBadRequest)
			}
		default:
			t.Errorf("unexpected API path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	got, err := client.ListPlaylists(context.Background())
	if err != nil {
		t.Fatalf("ListPlaylists() error = %v", err)
	}
	want := []AccountPlaylist{
		{ID: "PLown1", Title: "Owned one", URL: "https://www.youtube.com/playlist?list=PLown1"},
		{ID: "PLown2", Title: "Owned two", URL: "https://www.youtube.com/playlist?list=PLown2"},
		{ID: "LLowner", Title: "Liked videos", URL: "https://www.youtube.com/playlist?list=LLowner"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListPlaylists() = %+v, want %+v", got, want)
	}
	if len(requests) != 3 {
		t.Errorf("API requests = %v, want channel lookup and two playlist pages", requests)
	}
}

func TestAccountClientListTracksSkipsUnavailableAndFillsFields(t *testing.T) {
	var videoCalls int
	client := accountClientForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/youtube/v3/playlistItems":
			if r.URL.Query().Get("playlistId") != "PLowned" {
				t.Errorf("playlistId = %q, want PLowned", r.URL.Query().Get("playlistId"))
			}
			if r.URL.Query().Get("maxResults") != "50" {
				t.Errorf("maxResults = %q, want 50", r.URL.Query().Get("maxResults"))
			}
			writeAPIJSON(t, w, map[string]any{"items": []any{
				playlistItem("video-a", "First song", "Artist A", "public"),
				playlistItem("deleted", "Deleted video", "", "public"),
				playlistItem("private", "Private video", "", "private"),
				playlistItem("video-b", "Second song", "Artist B", "public"),
			}})
		case "/youtube/v3/videos":
			videoCalls++
			if !strings.Contains(r.URL.Query().Get("part"), "contentDetails") {
				t.Errorf("videos part = %q, want contentDetails", r.URL.Query().Get("part"))
			}
			ids := strings.Split(r.URL.Query().Get("id"), ",")
			if !slices.Equal(ids, []string{"video-a", "video-b"}) {
				t.Errorf("videos IDs = %v, want [video-a video-b]", ids)
			}
			writeAPIJSON(t, w, map[string]any{"items": []any{
				map[string]any{"id": "video-a", "contentDetails": map[string]any{"duration": "PT3M12S"}},
				map[string]any{"id": "video-b", "contentDetails": map[string]any{"duration": "PT1H2M3S"}},
			}})
		default:
			t.Errorf("unexpected API path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	got, err := client.ListTracks(context.Background(), "PLowned")
	if err != nil {
		t.Fatalf("ListTracks() error = %v", err)
	}
	want := []Track{
		{ID: "video-a", Title: "First song", Channel: "Artist A", URL: "https://www.youtube.com/watch?v=video-a", Duration: 3*time.Minute + 12*time.Second},
		{ID: "video-b", Title: "Second song", Channel: "Artist B", URL: "https://www.youtube.com/watch?v=video-b", Duration: time.Hour + 2*time.Minute + 3*time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTracks() = %+v, want %+v", got, want)
	}
	if videoCalls != 1 {
		t.Errorf("videos.list calls = %d, want one batched lookup", videoCalls)
	}
}

func TestAccountClientListTracksPaginatesAndCapsAt200(t *testing.T) {
	var itemPages, videoCalls int
	var requestedVideos []string
	client := accountClientForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/youtube/v3/playlistItems":
			itemPages++
			page := 0
			if token := r.URL.Query().Get("pageToken"); token != "" {
				var err error
				page, err = strconv.Atoi(token)
				if err != nil {
					t.Errorf("invalid pageToken %q", token)
				}
			}
			if page >= 4 {
				t.Errorf("requested page %d beyond the 200-item cap", page)
				http.Error(w, "past cap", http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("playlistId") != "PLlarge" {
				t.Errorf("playlistId = %q, want PLlarge", r.URL.Query().Get("playlistId"))
			}
			if r.URL.Query().Get("maxResults") != "50" {
				t.Errorf("maxResults = %q, want 50", r.URL.Query().Get("maxResults"))
			}
			items := make([]any, 50)
			for i := range items {
				id := fmt.Sprintf("video-%03d", page*50+i)
				items[i] = playlistItem(id, "Title "+id, "Artist", "public")
			}
			writeAPIJSON(t, w, map[string]any{"nextPageToken": strconv.Itoa(page + 1), "items": items})
		case "/youtube/v3/videos":
			videoCalls++
			ids := strings.Split(r.URL.Query().Get("id"), ",")
			if len(ids) > 50 {
				t.Errorf("videos.list requested %d IDs, maximum is 50", len(ids))
			}
			requestedVideos = append(requestedVideos, ids...)
			items := make([]any, len(ids))
			for i, id := range ids {
				items[i] = map[string]any{"id": id, "contentDetails": map[string]any{"duration": "PT1M"}}
			}
			writeAPIJSON(t, w, map[string]any{"items": items})
		default:
			t.Errorf("unexpected API path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	got, err := client.ListTracks(context.Background(), "PLlarge")
	if err != nil {
		t.Fatalf("ListTracks() error = %v", err)
	}
	if len(got) != MaxPlaylistItems {
		t.Fatalf("ListTracks() returned %d tracks, want %d", len(got), MaxPlaylistItems)
	}
	if itemPages != 4 {
		t.Errorf("playlistItems.list pages = %d, want 4", itemPages)
	}
	if videoCalls != 4 {
		t.Errorf("videos.list calls = %d, want 4 batches of 50", videoCalls)
	}
	if len(requestedVideos) != MaxPlaylistItems {
		t.Errorf("videos.list requested %d IDs, want %d", len(requestedVideos), MaxPlaylistItems)
	}
	if got[0].ID != "video-000" || got[199].ID != "video-199" || got[199].Duration != time.Minute {
		t.Errorf("first/last tracks = %+v / %+v, want IDs video-000 / video-199 and one-minute duration", got[0], got[199])
	}
}

func playlistItem(id, title, owner, privacy string) map[string]any {
	return map[string]any{
		"snippet": map[string]any{
			"title":                  title,
			"videoOwnerChannelTitle": owner,
			"resourceId":             map[string]any{"kind": "youtube#video", "videoId": id},
		},
		"contentDetails": map[string]any{"videoId": id},
		"status":         map[string]any{"privacyStatus": privacy},
	}
}

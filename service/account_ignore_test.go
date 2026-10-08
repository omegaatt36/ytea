package service

import (
	"testing"

	"github.com/omegaatt36/ytea/domain"
)

type stubAccountIgnoreStore struct{}

func (stubAccountIgnoreStore) IgnoredYouTubePlaylists() []string  { return []string{"hidden"} }
func (stubAccountIgnoreStore) IgnoreYouTubePlaylist(string) error { return nil }

func TestAccountIgnoreStoreDoesNotRequireLocalLibrary(t *testing.T) {
	c := New(Deps{AccountIgnores: stubAccountIgnoreStore{}})
	c.Account.Update(AccountPlaylistsDone{playlists: []domain.AccountPlaylist{
		{ID: "one", Title: "hidden"}, {ID: "two", Title: "visible"},
	}})
	if !c.Account.CanIgnore() || len(c.Account.Playlists) != 1 || c.Account.Playlists[0].Title != "visible" {
		t.Fatalf("separate ignore dependency: canIgnore=%v playlists=%+v", c.Account.CanIgnore(), c.Account.Playlists)
	}
}

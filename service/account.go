package service

import (
	"context"
	"errors"
	"slices"

	"github.com/omegaatt36/ytea/domain"
)

type AccountSource interface {
	ListPlaylists(context.Context) ([]domain.AccountPlaylist, error)
	ListTracks(context.Context, string) ([]domain.Track, error)
}

// AccountIgnoreStore persists account playlist preferences independently of local playlists.
type AccountIgnoreStore interface {
	IgnoredYouTubePlaylists() []string
	IgnoreYouTubePlaylist(string) error
}

type Account struct {
	source           AccountSource
	ignoreStore      AccountIgnoreStore
	Started, Loading bool
	Playlists        []domain.AccountPlaylist
	Err              error
	Tracks           map[string][]domain.Track
	TrackLoading     map[string]bool
	TrackErrors      map[string]error
	QueueErrors      map[string]error
	// Reload advances the generation so delayed requests cannot repopulate the new view.
	generation uint64
}

type (
	AccountPlaylistsDone struct {
		generation uint64
		playlists  []domain.AccountPlaylist
		err        error
	}
	AccountTracksDone struct {
		generation uint64
		id         string
		tracks     []domain.Track
		err        error
	}
	// AccountQueueFailed reports a queue lookup that failed. It must not
	// complete or clear an independent browse request.
	AccountQueueFailed struct {
		generation uint64
		id         string
		err        error
	}
)

func (AccountPlaylistsDone) serviceMsg() {}
func (AccountTracksDone) serviceMsg()    {}
func (AccountQueueFailed) serviceMsg()   {}

func (a Account) Enabled() bool { return a.source != nil }

func (a Account) CanIgnore() bool { return a.ignoreStore != nil }

// Ignore hides every account playlist with this exact title only after saving.
func (a *Account) Ignore(title string) error {
	if !a.CanIgnore() {
		return errors.New("playlist ignore storage is unavailable")
	}
	if err := a.ignoreStore.IgnoreYouTubePlaylist(title); err != nil {
		return err
	}
	a.Playlists = a.visiblePlaylists(a.Playlists)
	return nil
}

func (a Account) visiblePlaylists(playlists []domain.AccountPlaylist) []domain.AccountPlaylist {
	if !a.CanIgnore() {
		return playlists
	}
	ignored := a.ignoreStore.IgnoredYouTubePlaylists()
	visible := make([]domain.AccountPlaylist, 0, len(playlists))
	for _, p := range playlists {
		if !slices.Contains(ignored, p.Title) {
			visible = append(visible, p)
		}
	}
	return visible
}

func (a *Account) Reload() Cmd {
	if a.Loading {
		return nil
	}
	a.Playlists = nil
	a.Started, a.Loading = true, true
	a.Err = nil
	a.generation++
	generation := a.generation
	a.TrackLoading = make(map[string]bool)
	a.TrackErrors = make(map[string]error)
	a.QueueErrors = make(map[string]error)
	a.Tracks = make(map[string][]domain.Track)
	source := a.source
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()
		playlists, err := source.ListPlaylists(ctx)
		return AccountPlaylistsDone{generation: generation, playlists: playlists, err: err}
	}
}

func (a *Account) LoadTracks(id string) Cmd {
	if a.Loading || a.Err != nil || a.TrackLoading[id] {
		return nil
	}
	if _, loaded := a.Tracks[id]; loaded || a.TrackErrors[id] != nil {
		return nil
	}
	a.TrackLoading[id] = true
	source, generation := a.source, a.generation
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()
		tracks, err := source.ListTracks(ctx, id)
		return AccountTracksDone{generation: generation, id: id, tracks: tracks, err: err}
	}
}

func (a *Account) Retry(id string) { delete(a.TrackErrors, id) }

func (a *Account) Update(msg Msg) {
	switch msg := msg.(type) {
	case AccountPlaylistsDone:
		if msg.generation != a.generation {
			return
		}
		a.Loading = false
		a.Err = msg.err
		if msg.err == nil && len(msg.playlists) == 0 {
			a.Err = errors.New("no account data")
		}
		a.Playlists = a.visiblePlaylists(msg.playlists)
	case AccountQueueFailed:
		// A successful browse supersedes an earlier queue lookup for the same playlist.
		_, browsed := a.Tracks[msg.id]
		if msg.generation == a.generation && !browsed {
			a.QueueErrors[msg.id] = msg.err
		}
	case AccountTracksDone:
		if msg.generation != a.generation {
			return
		}
		a.TrackLoading[msg.id] = false
		a.TrackErrors[msg.id] = msg.err
		if msg.err == nil {
			a.Tracks[msg.id] = msg.tracks
			delete(a.QueueErrors, msg.id)
		}
	}
}

func (c *Core) QueueAccountPlaylist(id string) Cmd {
	if c.deps.Player == nil {
		return nil
	}
	requestID, task := c.NextRequest(), c.Queue.Reserve()
	a := &c.Account
	source, player := a.source, c.deps.Player
	tracks, loaded := a.Tracks[id]
	generation := a.generation
	delete(a.QueueErrors, id)
	return func() Msg {
		if !loaded {
			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()
			var err error
			tracks, err = source.ListTracks(ctx, id)
			if err != nil {
				_ = task.Run(func() error { return err })
				return AccountQueueFailed{generation: generation, id: id, err: err}
			}
		}
		return appendPlaylist(player.AppendAll, tracks, requestID, task)()
	}
}

package service

import (
	"context"
	"maps"

	"github.com/omegaatt36/ytea/domain"
)

type Searcher interface {
	// Search skips the first offset results, so later pages extend earlier ones.
	Search(ctx context.Context, query string, offset, limit int) ([]domain.Track, error)
	// Lookup resolves the tracks behind a YouTube URL; a positive limit caps the listing.
	Lookup(ctx context.Context, url string, limit int) ([]domain.Track, error)
}

type Deps struct {
	Searcher Searcher
	Player   Player
	// Library, History and Account are optional.
	Library        PlaylistStore
	History        HistoryStore
	Account        AccountSource
	AccountIgnores AccountIgnoreStore
	InitialTracks  map[string]domain.Track
	Normalize      bool
}

// Core holds no cursors, focus or text: those belong to the front end.
type Core struct {
	deps Deps

	Playback  Playback
	Queue     Queue
	Search    Search
	History   History
	Playlists Playlists
	Account   Account
	Devices   []domain.AudioDevice
	Stream    domain.StreamInfo
	// Tracks remembers metadata by URL, since mpv only knows filenames until
	// yt-dlp resolves each entry.
	Tracks map[string]domain.Track

	requestID uint64
	// Active is the newest user action; only its completion is worth reporting.
	Active uint64

	busy        bool
	busyRequest uint64
}

func New(deps Deps) Core {
	tracks := make(map[string]domain.Track, len(deps.InitialTracks))
	maps.Copy(tracks, deps.InitialTracks)
	c := Core{
		deps:      deps,
		Playback:  Playback{Idle: true, Volume: 100, Normalize: deps.Normalize},
		Queue:     NewQueue(deps.Player),
		Tracks:    tracks,
		History:   History{store: deps.History},
		Playlists: Playlists{store: deps.Library},
		Account: Account{
			source:       deps.Account,
			ignoreStore:  deps.AccountIgnores,
			Tracks:       make(map[string][]domain.Track),
			TrackLoading: make(map[string]bool),
			TrackErrors:  make(map[string]error),
			QueueErrors:  make(map[string]error),
		},
	}
	c.History.Reload()
	if c.Playlists.Enabled() {
		c.Playlists.List = deps.Library.Playlists()
	}
	return c
}

// NextRequest names a new user action. Completions carry the name back, so
// stale ones can be told from the current one.
func (c *Core) NextRequest() uint64 {
	c.requestID++
	c.Active = c.requestID
	return c.requestID
}

func (c Core) Busy() bool { return c.busy }

func (c *Core) beginBusy(requestID uint64) {
	c.busy, c.busyRequest = true, requestID
}

func (c *Core) endBusy(requestID uint64) {
	if requestID == c.busyRequest {
		c.busy = false
	}
}

func (c Core) Current() (domain.PlaylistEntry, domain.Track, bool) {
	pos := c.Queue.Pos
	if c.Playback.Idle || pos < 0 || pos >= len(c.Queue.Entries) {
		return domain.PlaylistEntry{}, domain.Track{}, false
	}
	e := c.Queue.Entries[pos]
	return e, c.Tracks[e.Filename], true
}

func (c Core) EntryTrack(e domain.PlaylistEntry) domain.Track {
	t := c.Tracks[e.Filename]
	t.URL = e.Filename
	if t.Title == "" {
		t.Title = e.Title
	}
	return t
}

func (c *Core) remember(tracks ...domain.Track) {
	for _, t := range tracks {
		c.Tracks[t.URL] = t
	}
}

func (c *Core) PlayNow(t domain.Track) Cmd {
	c.remember(t)
	return c.Queue.PlayNow(c.NextRequest(), t)
}

func (c *Core) Enqueue(t domain.Track) Cmd {
	c.remember(t)
	return c.Queue.Enqueue(c.NextRequest(), t)
}

func (c *Core) PlayAll(tracks []domain.Track, start int) Cmd {
	c.remember(tracks...)
	return c.Queue.Replace(c.NextRequest(), tracks, start)
}

func (c *Core) ApplyEvent(ev PlayerEvent) (cmd Cmd, switchesTrack bool) {
	cmd = c.Queue.ApplyEvent(ev)
	return cmd, c.Playback.Apply(ev)
}

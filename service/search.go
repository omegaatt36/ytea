package service

import (
	"context"
	"slices"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

const (
	SearchLimit   = 30
	RadioLimit    = 25
	searchTimeout = 30 * time.Second
	importTimeout = 2 * time.Minute
)

type Search struct {
	Tracks []domain.Track
	// Fetched counts the results its pages have covered, including ones dropped as
	// duplicates or non-videos.
	Query       string
	Fetched     int
	More        bool
	LoadingMore bool

	// request is the newest search or page; older completions are stale.
	request uint64
	imports importQueue
}

func (s Search) CanLoadMore() bool {
	return s.Query != "" && s.More
}

type (
	// Offset is how many results earlier pages covered; 0 is a new search.
	SearchDone struct {
		RequestID uint64
		Query     string
		Offset    int
		Tracks    []domain.Track
		Err       error
	}
	LookupDone struct {
		RequestID uint64
		Tracks    []domain.Track
		LimitHit  bool
		Err       error
	}
	AppendDone struct {
		RequestID uint64
		Tracks    []domain.Track
		LimitHit  bool
		Err       error
	}
)

func (SearchDone) serviceMsg() {}
func (LookupDone) serviceMsg() {}
func (AppendDone) serviceMsg() {}

// A non-nil link says the input was imported rather than searched.
func (c *Core) Submit(input string) (cmd Cmd, link *domain.Link) {
	requestID := c.NextRequest()
	c.beginBusy(requestID)
	if ref, ok := linkOf(input); ok {
		c.Search.imports = c.Search.imports.add(requestID)
		return fetchQueue(c.deps.Searcher, ref, requestID), &ref
	}
	c.Search.request = requestID
	return search(c.deps.Searcher, input, 0, requestID), nil
}

func (c *Core) LoadMore() Cmd {
	s := &c.Search
	if !s.CanLoadMore() || s.LoadingMore {
		return nil
	}
	requestID := c.NextRequest()
	s.request = requestID
	c.beginBusy(requestID)
	s.LoadingMore = true
	return search(c.deps.Searcher, s.Query, s.Fetched, requestID)
}

func (c *Core) StartRadio() Cmd {
	_, t, ok := c.Current()
	if !ok || t.ID == "" {
		return nil
	}
	requestID := c.NextRequest()
	c.Search.imports = c.Search.imports.add(requestID)
	c.beginBusy(requestID)
	return fetchRadio(c.deps.Searcher, t.ID, requestID)
}

// ok is false when a newer search made the page stale.
func (c *Core) SearchDone(msg SearchDone) (added int, ok bool) {
	s := &c.Search
	if msg.RequestID != s.request {
		return 0, false
	}
	c.endBusy(msg.RequestID)
	s.LoadingMore = false
	if msg.Err != nil {
		return 0, true
	}
	c.remember(msg.Tracks...)
	if msg.Offset > 0 {
		added = s.extend(msg.Tracks, msg.Offset+SearchLimit)
		return added, true
	}
	s.Tracks, s.Query, s.Fetched, s.More = msg.Tracks, msg.Query, SearchLimit, len(msg.Tracks) > 0
	return len(msg.Tracks), true
}

func (s *Search) extend(tracks []domain.Track, fetched int) int {
	seen := make(map[string]bool, len(s.Tracks))
	for _, t := range s.Tracks {
		seen[t.URL] = true
	}
	added := 0
	for _, t := range tracks {
		if !seen[t.URL] {
			seen[t.URL] = true
			s.Tracks = append(s.Tracks, t)
			added++
		}
	}
	s.Fetched = fetched
	// An empty page is the end; a page of only duplicates is not.
	s.More = len(tracks) > 0
	return added
}

// LookupDone queues a resolved lookup. Slots are reserved only once a lookup
// resolves, so queue edits made meanwhile neither wait on the network nor act
// on its tracks; lookups still land in the order they were started.
func (c *Core) LookupDone(msg LookupDone) []Cmd {
	var ready []LookupDone
	c.Search.imports, ready = c.Search.imports.resolve(msg)
	p := c.deps.Player
	cmds := make([]Cmd, len(ready))
	for i, lookup := range ready {
		cmds[i] = appendLookup(func(tracks []domain.Track) error { return appendTracks(p, tracks) }, lookup, c.Queue.Reserve())
	}
	return cmds
}

func (c *Core) AppendDone(msg AppendDone) Cmd {
	c.endBusy(msg.RequestID)
	// mpv may have accepted some entries before AppendAll returned an error.
	c.remember(msg.Tracks...)
	return c.Queue.Reconcile()
}

func (c *Core) AppendTracks(tracks []domain.Track) Cmd {
	requestID := c.NextRequest()
	c.beginBusy(requestID)
	return appendPlaylist(c.deps.Player.AppendAll, tracks, requestID, c.Queue.Reserve())
}

// importQueue lists link and radio lookups in trigger order, so a slow lookup
// cannot reorder the imports queued after it.
type importQueue []pendingImport

type pendingImport struct {
	requestID uint64
	lookup    *LookupDone // nil while the lookup runs
}

func (q importQueue) add(requestID uint64) importQueue {
	return append(q, pendingImport{requestID: requestID})
}

// resolve records a finished lookup and returns, in trigger order, the
// lookups no longer waiting behind an earlier one.
func (q importQueue) resolve(msg LookupDone) (importQueue, []LookupDone) {
	i := slices.IndexFunc(q, func(p pendingImport) bool { return p.requestID == msg.RequestID })
	if i < 0 {
		return q, nil
	}
	q = slices.Clone(q)
	q[i].lookup = &msg
	var ready []LookupDone
	for len(q) > 0 && q[0].lookup != nil {
		ready = append(ready, *q[0].lookup)
		q = q[1:]
	}
	return q, ready
}

func search(s Searcher, query string, offset int, requestID uint64) Cmd {
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		tracks, err := s.Search(ctx, query, offset, SearchLimit)
		return SearchDone{RequestID: requestID, Query: query, Offset: offset, Tracks: tracks, Err: err}
	}
}

func fetchQueue(s Searcher, link domain.Link, requestID uint64) Cmd {
	return func() Msg {
		limit := 0
		if link.Mix {
			limit = RadioLimit
		}
		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()
		tracks, err := s.Lookup(ctx, link.URL, limit)
		if link.Mix && len(tracks) > RadioLimit {
			tracks = tracks[:RadioLimit]
		}
		if !link.Mix && len(tracks) > domain.MaxPlaylistItems {
			tracks = tracks[:domain.MaxPlaylistItems]
		}
		limitHit := !link.Mix && len(tracks) == domain.MaxPlaylistItems
		return LookupDone{RequestID: requestID, Tracks: tracks, LimitHit: limitHit, Err: err}
	}
}

func fetchRadio(s Searcher, seedID string, requestID uint64) Cmd {
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		ref := "https://www.youtube.com/watch?v=" + seedID + "&list=RD" + seedID
		tracks, err := s.Lookup(ctx, ref, RadioLimit+1)
		// The mix lists its seed first, and the seed is what is playing.
		tracks = slices.DeleteFunc(tracks, func(t domain.Track) bool { return t.ID == seedID })
		if len(tracks) > RadioLimit {
			tracks = tracks[:RadioLimit]
		}
		return LookupDone{RequestID: requestID, Tracks: tracks, Err: err}
	}
}

func appendLookup(appendQueue func([]domain.Track) error, lookup LookupDone, task Task) Cmd {
	return func() Msg {
		err := task.Run(func() error {
			if lookup.Err != nil || len(lookup.Tracks) == 0 {
				return lookup.Err
			}
			return appendQueue(lookup.Tracks)
		})
		return AppendDone{RequestID: lookup.RequestID, Tracks: lookup.Tracks, LimitHit: lookup.LimitHit, Err: err}
	}
}

func appendPlaylist(appendAll func(context.Context, []string) error, tracks []domain.Track, requestID uint64, task Task) Cmd {
	return func() Msg {
		urls := make([]string, len(tracks))
		for i, t := range tracks {
			urls[i] = t.URL
		}
		err := task.Run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()
			return appendAll(ctx, urls)
		})
		return AppendDone{RequestID: requestID, Tracks: tracks, Err: err}
	}
}

func appendTracks(p Player, tracks []domain.Track) error {
	urls := make([]string, len(tracks))
	for i, track := range tracks {
		urls[i] = track.URL
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return p.AppendAll(ctx, urls)
}

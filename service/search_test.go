package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

type searchStub struct{ tracks []domain.Track }

func (s searchStub) Search(context.Context, string, int, int) ([]domain.Track, error) {
	return s.tracks, nil
}

func (s searchStub) Lookup(context.Context, string, int) ([]domain.Track, error) {
	return s.tracks, nil
}

// appendRecorder reports AppendAll calls on a channel so tests can observe
// the order of concurrent writes.
type appendRecorder struct {
	Player
	writes chan string
}

func (p appendRecorder) AppendAll(_ context.Context, urls []string) error {
	p.writes <- urls[0]
	return nil
}

func watch(id string) domain.Track {
	return domain.Track{ID: id, URL: "https://www.youtube.com/watch?v=" + id}
}

func numbered(seed string, n int) []domain.Track {
	tracks := []domain.Track{watch(seed)}
	for i := range n {
		tracks = append(tracks, watch(fmt.Sprintf("t%d", i)))
	}
	return tracks
}

func TestFetchQueueCapsMixes(t *testing.T) {
	tracks := numbered("seed", RadioLimit+3)

	link := domain.Link{URL: "https://www.youtube.com/watch?v=seed&list=RDseed", Mix: true}
	lookup := fetchQueue(searchStub{tracks: tracks}, link, 1)().(LookupDone)
	if len(lookup.Tracks) != RadioLimit {
		t.Errorf("tracks = %d, want %d", len(lookup.Tracks), RadioLimit)
	}
	if lookup.Tracks[0].ID != "seed" {
		t.Errorf("first track = %q, want the seed, which is what the link plays", lookup.Tracks[0].ID)
	}

	link = domain.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	lookup = fetchQueue(searchStub{tracks: tracks}, link, 2)().(LookupDone)
	if len(lookup.Tracks) != RadioLimit+4 {
		t.Errorf("tracks = %d, want the whole playlist", len(lookup.Tracks))
	}
}

func TestFetchQueueCapsLargePlaylistAndFlagsLimit(t *testing.T) {
	tracks := make([]domain.Track, domain.MaxPlaylistItems+5)
	for i := range tracks {
		tracks[i] = watch(fmt.Sprint(i))
	}
	link := domain.Link{URL: "https://www.youtube.com/playlist?list=PL123"}
	lookup := fetchQueue(searchStub{tracks: tracks}, link, 1)().(LookupDone)

	appended := 0
	done := appendLookup(func(got []domain.Track) error {
		appended = len(got)
		return nil
	}, lookup, (&Queue{}).Reserve())().(AppendDone)
	if len(done.Tracks) != domain.MaxPlaylistItems || appended != domain.MaxPlaylistItems || !done.LimitHit {
		t.Fatalf("queued %d tracks, appended %d, limitHit %v", len(done.Tracks), appended, done.LimitHit)
	}
}

func TestFetchRadioDropsSeedAndCaps(t *testing.T) {
	lookup := fetchRadio(searchStub{tracks: numbered("seed", RadioLimit+1)}, "seed", 1)().(LookupDone)
	if lookup.Err != nil {
		t.Fatalf("err = %v", lookup.Err)
	}
	if len(lookup.Tracks) != RadioLimit {
		t.Errorf("tracks = %d, want %d", len(lookup.Tracks), RadioLimit)
	}
	for _, tr := range lookup.Tracks {
		if tr.ID == "seed" {
			t.Error("seed still queued, want it dropped from the mix")
		}
	}
}

func TestPartialAppendKeepsResolvedTracks(t *testing.T) {
	c := New(Deps{})
	want := errors.New("mpv rejected append")
	tracks := []domain.Track{{ID: "x", Title: "resolved first", URL: "x"}, {ID: "y", Title: "resolved second", URL: "y"}}
	accepted := ""
	done := appendLookup(func(tracks []domain.Track) error {
		accepted = tracks[0].URL // mpv accepted this entry before rejecting the next one.
		return want
	}, LookupDone{RequestID: c.NextRequest(), Tracks: tracks}, c.Queue.Reserve())().(AppendDone)
	if !errors.Is(done.Err, want) {
		t.Fatalf("err = %v, want %v", done.Err, want)
	}
	c.AppendDone(done)
	if accepted != "x" || c.Tracks["x"].Title != "resolved first" || c.Tracks["y"].Title != "resolved second" {
		t.Errorf("accepted = %q, metadata = %+v, want resolved tracks retained after partial write", accepted, c.Tracks)
	}
}

func TestSearchCompletionKeepsNewestRequest(t *testing.T) {
	c := New(Deps{})
	c.Submit("older")
	first := c.Active
	c.Submit("newest")
	second := c.Active

	if _, ok := c.SearchDone(SearchDone{RequestID: second, Query: "newest", Tracks: []domain.Track{{ID: "new", URL: "new"}}}); !ok {
		t.Fatal("newest completion was dropped")
	}
	if _, ok := c.SearchDone(SearchDone{RequestID: first, Query: "older", Tracks: []domain.Track{{ID: "old", URL: "old"}}}); ok {
		t.Error("older completion was applied")
	}
	if len(c.Search.Tracks) != 1 || c.Search.Tracks[0].ID != "new" || c.Busy() {
		t.Errorf("results = %+v, busy = %v, want only newest and the wait over", c.Search.Tracks, c.Busy())
	}
	if _, ok := c.Tracks["old"]; ok {
		t.Error("older result was added to track metadata")
	}
}

func TestStaleCompletionKeepsWaiting(t *testing.T) {
	c := New(Deps{})
	c.Submit("old")
	old := c.Active
	c.Submit("newest")

	if _, ok := c.SearchDone(SearchDone{RequestID: old, Err: errors.New("old search failed")}); ok || !c.Busy() {
		t.Errorf("stale search: applied = %v, busy = %v", ok, c.Busy())
	}
	c.AppendDone(AppendDone{RequestID: old})
	if !c.Busy() {
		t.Error("a stale append ended the wait for the newest search")
	}
}

func TestLaterActionDoesNotCancelPendingSearch(t *testing.T) {
	c := New(Deps{})
	c.Submit("song")
	searchID := c.Active
	queueID := c.NextRequest()

	c.SearchDone(SearchDone{RequestID: searchID, Query: "song", Tracks: []domain.Track{{ID: "song", URL: "song"}}})
	if len(c.Search.Tracks) != 1 || c.Busy() || c.Active != queueID {
		t.Errorf("results = %+v, busy = %v, active = %d", c.Search.Tracks, c.Busy(), c.Active)
	}
}

func TestSubmitImportsLinksAndSearchesText(t *testing.T) {
	c := New(Deps{Searcher: searchStub{tracks: []domain.Track{watch("a")}}})

	cmd, link := c.Submit("https://youtu.be/a")
	if link == nil || cmd == nil || !c.Busy() {
		t.Fatalf("link: cmd=%v link=%v busy=%v, want an import in flight", cmd != nil, link, c.Busy())
	}
	if _, ok := cmd().(LookupDone); !ok {
		t.Error("a link did not start a lookup")
	}

	cmd, link = c.Submit("lofi")
	if link != nil {
		t.Errorf("link = %v, want text searched", link)
	}
	if _, ok := cmd().(SearchDone); !ok {
		t.Error("text did not start a search")
	}
}

func TestLoadMoreAppendsNextPageOnce(t *testing.T) {
	c := New(Deps{Searcher: searchStub{}})
	if c.LoadMore() != nil {
		t.Fatal("LoadMore without results asked for a page")
	}
	c.Submit("q")
	page := make([]domain.Track, SearchLimit)
	for i := range page {
		page[i] = watch(fmt.Sprint(i))
	}
	c.SearchDone(SearchDone{RequestID: c.Active, Query: "q", Tracks: page})
	if !c.Search.CanLoadMore() {
		t.Fatal("a full page left no more on offer")
	}
	cmd := c.LoadMore()
	if cmd == nil || !c.Search.LoadingMore {
		t.Fatal("LoadMore did not start")
	}
	if c.LoadMore() != nil {
		t.Error("a second LoadMore while loading asked for the page twice")
	}
	added, ok := c.SearchDone(SearchDone{RequestID: c.Active, Query: "q", Offset: SearchLimit, Tracks: []domain.Track{page[0], watch("new")}})
	if !ok || added != 1 || len(c.Search.Tracks) != SearchLimit+1 || c.Search.Fetched != 2*SearchLimit {
		t.Errorf("added = %d, results = %d, fetched = %d", added, len(c.Search.Tracks), c.Search.Fetched)
	}
}

func TestStartRadioNeedsPlayingTrack(t *testing.T) {
	c := New(Deps{})
	if c.StartRadio() != nil {
		t.Error("radio started with nothing playing")
	}
	c.Queue.Entries, c.Queue.Pos, c.Playback.Idle = []domain.PlaylistEntry{{Filename: "x"}}, 0, false
	c.Tracks["x"] = domain.Track{ID: "x", URL: "x"}
	if c.StartRadio() == nil || !c.Busy() {
		t.Error("radio did not start from the playing track")
	}
}

func TestOverlappingImportsWriteInTriggerOrder(t *testing.T) {
	player := appendRecorder{writes: make(chan string, 2)}
	c := New(Deps{Player: player, Searcher: searchStub{}})
	c.Submit("https://youtu.be/a")
	first := c.Active
	c.Submit("https://youtu.be/b")
	second := c.Active

	if cmds := c.LookupDone(LookupDone{RequestID: second, Tracks: []domain.Track{{URL: "second"}}}); len(cmds) != 0 {
		t.Fatal("second import was queued before first resolved")
	}
	cmds := c.LookupDone(LookupDone{RequestID: first, Tracks: []domain.Track{{URL: "first"}}})
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want both imports queued", len(cmds))
	}
	// Start the writes in reverse: their reserved slots still keep trigger order.
	done := make(chan Msg, 2)
	for _, write := range slices.Backward(cmds) {
		go func() { done <- write() }()
	}
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-player.writes:
			if got != want {
				t.Fatalf("write = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q write", want)
		}
	}
	for range 2 {
		if err := (<-done).(AppendDone).Err; err != nil {
			t.Fatalf("import error = %v", err)
		}
	}
}

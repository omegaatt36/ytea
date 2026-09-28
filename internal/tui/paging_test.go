package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
)

// pageSearcher serves numbered results and records the offsets asked for.
type pageSearcher struct {
	total   int
	offsets []int
}

func (s *pageSearcher) Search(_ context.Context, _ string, offset, limit int) ([]youtube.Track, error) {
	s.offsets = append(s.offsets, offset)
	var tracks []youtube.Track
	for i := offset; i < min(offset+limit, s.total); i++ {
		tracks = append(tracks, youtube.Track{Title: fmt.Sprintf("song %d", i), URL: fmt.Sprint(i)})
	}
	return tracks, nil
}

func (s *pageSearcher) Lookup(context.Context, string, int) ([]youtube.Track, error) {
	return nil, nil
}

// runCmd runs cmd and any batch it returns, and collects the messages.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func deliverSearch(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range runCmd(cmd) {
		if done, ok := msg.(searchDoneMsg); ok {
			got, _ := m.update(done)
			return got.(Model)
		}
	}
	t.Fatal("no search ran")
	return m
}

func searchedModel(t *testing.T, total int) (Model, *pageSearcher) {
	t.Helper()
	s := &pageSearcher{total: total}
	m := New(Deps{Searcher: s})
	m.width, m.height = 100, 40
	m = typeText(t, m, "lofi")
	got, cmd := m.update(keyPress("enter"))
	return deliverSearch(t, got.(Model), cmd), s
}

func titles(tracks []youtube.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Title
	}
	return out
}

func TestMoreResultsAppendsTheNextPage(t *testing.T) {
	m, s := searchedModel(t, 45)
	if len(m.results.tracks) != searchLimit || !m.results.canLoadMore() {
		t.Fatalf("first page: %d results, more=%v", len(m.results.tracks), m.results.canLoadMore())
	}
	if got := resultsTitleLine(t, m); !strings.Contains(got, fmt.Sprintf("Results (%d+)", searchLimit)) {
		t.Errorf("title %q does not hint at more results", got)
	}
	m.results.cur = 3

	got, cmd := m.update(keyPress("m"))
	m = deliverSearch(t, got.(Model), cmd)
	if want := []int{0, searchLimit}; !slices.Equal(s.offsets, want) {
		t.Errorf("search offsets = %v, want %v", s.offsets, want)
	}
	if len(m.results.tracks) != 45 || m.results.tracks[44].Title != "song 44" || m.results.cur != 3 {
		t.Fatalf("after more: %d results, last %q, cursor %d", len(m.results.tracks), m.results.tracks[len(m.results.tracks)-1].Title, m.results.cur)
	}

	got, cmd = m.update(keyPress("m"))
	m = deliverSearch(t, got.(Model), cmd)
	if m.results.canLoadMore() {
		t.Error("an empty page left more results on offer")
	}
	if _, cmd := m.update(keyPress("m")); cmd != nil {
		t.Error("m searched again past the last page")
	}
}

func TestDownPastTheLastResultLoadsMore(t *testing.T) {
	m, s := searchedModel(t, 100)
	m.results.cur = len(m.results.tracks) - 1
	got, cmd := m.update(keyPress("down"))
	m = got.(Model)
	if !m.results.loadingMore {
		t.Fatal("down on the last result did not load more")
	}
	// A second press while the page loads must not ask for it twice.
	if _, again := m.update(keyPress("down")); again != nil {
		t.Error("down while loading started another search")
	}
	m = deliverSearch(t, m, cmd)
	if len(s.offsets) != 2 || m.results.cur != searchLimit-1 {
		t.Errorf("offsets %v, cursor %d; want two searches and the cursor left in place", s.offsets, m.results.cur)
	}
}

func TestNewSearchDiscardsAPendingPage(t *testing.T) {
	m, _ := searchedModel(t, 100)
	got, moreCmd := m.update(keyPress("m"))
	m = got.(Model)
	m = press(t, m, keyPress("/"))
	m.input.SetValue("jazz")
	got, searchCmd := m.update(keyPress("enter"))
	m = got.(Model)

	m = deliverSearch(t, m, searchCmd)
	m = deliverSearch(t, m, moreCmd)
	if len(m.results.tracks) != searchLimit || m.results.query != "jazz" || m.results.loadingMore {
		t.Errorf("stale page applied: %d results for %q, loading=%v", len(m.results.tracks), m.results.query, m.results.loadingMore)
	}
}

func TestMoreResultsSkipsDuplicates(t *testing.T) {
	var r resultsPane
	r.set([]youtube.Track{{Title: "a", URL: "a"}, {Title: "b", URL: "b"}})
	r.query = "q"
	if added := r.extend([]youtube.Track{{Title: "b", URL: "b"}, {Title: "c", URL: "c"}}, 60); added != 1 {
		t.Errorf("extend() added %d, want 1", added)
	}
	if got, want := titles(r.tracks), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("results = %q, want %q", got, want)
	}
	if !r.canLoadMore() || r.fetched != 60 {
		t.Errorf("more=%v fetched=%d, want more past offset 60", r.canLoadMore(), r.fetched)
	}
}

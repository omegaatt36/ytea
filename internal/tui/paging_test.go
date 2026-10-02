package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

// pageSearcher serves numbered results and records the offsets asked for.
type pageSearcher struct {
	total   int
	offsets []int
}

func (s *pageSearcher) Search(_ context.Context, _ string, offset, limit int) ([]domain.Track, error) {
	s.offsets = append(s.offsets, offset)
	var tracks []domain.Track
	for i := offset; i < min(offset+limit, s.total); i++ {
		tracks = append(tracks, domain.Track{Title: fmt.Sprintf("song %d", i), URL: fmt.Sprint(i)})
	}
	return tracks, nil
}

func (s *pageSearcher) Lookup(context.Context, string, int) ([]domain.Track, error) {
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
		if done, ok := msg.(service.SearchDone); ok {
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

func titles(tracks []domain.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Title
	}
	return out
}

func TestMoreResultsAppendsTheNextPage(t *testing.T) {
	m, s := searchedModel(t, 45)
	if len(m.core.Search.Tracks) != service.SearchLimit || !m.core.Search.CanLoadMore() {
		t.Fatalf("first page: %d results, more=%v", len(m.core.Search.Tracks), m.core.Search.CanLoadMore())
	}
	if got := resultsTitleLine(t, m); !strings.Contains(got, fmt.Sprintf("Results (%d+)", service.SearchLimit)) {
		t.Errorf("title %q does not hint at more results", got)
	}
	m.results.cur = 3

	got, cmd := m.update(keyPress("m"))
	m = deliverSearch(t, got.(Model), cmd)
	if want := []int{0, service.SearchLimit}; !slices.Equal(s.offsets, want) {
		t.Errorf("search offsets = %v, want %v", s.offsets, want)
	}
	if len(m.core.Search.Tracks) != 45 || m.core.Search.Tracks[44].Title != "song 44" || m.results.cur != 3 {
		t.Fatalf("after more: %d results, last %q, cursor %d", len(m.core.Search.Tracks), m.core.Search.Tracks[len(m.core.Search.Tracks)-1].Title, m.results.cur)
	}

	got, cmd = m.update(keyPress("m"))
	m = deliverSearch(t, got.(Model), cmd)
	if m.core.Search.CanLoadMore() {
		t.Error("an empty page left more results on offer")
	}
	if _, cmd := m.update(keyPress("m")); cmd != nil {
		t.Error("m searched again past the last page")
	}
}

func TestDownPastTheLastResultLoadsMore(t *testing.T) {
	m, s := searchedModel(t, 100)
	m.results.cur = len(m.core.Search.Tracks) - 1
	got, cmd := m.update(keyPress("down"))
	m = got.(Model)
	if !m.core.Search.LoadingMore {
		t.Fatal("down on the last result did not load more")
	}
	// A second press while the page loads must not ask for it twice.
	if _, again := m.update(keyPress("down")); again != nil {
		t.Error("down while loading started another search")
	}
	m = deliverSearch(t, m, cmd)
	if len(s.offsets) != 2 || m.results.cur != service.SearchLimit-1 {
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
	if len(m.core.Search.Tracks) != service.SearchLimit || m.core.Search.Query != "jazz" || m.core.Search.LoadingMore {
		t.Errorf("stale page applied: %d results for %q, loading=%v", len(m.core.Search.Tracks), m.core.Search.Query, m.core.Search.LoadingMore)
	}
}

package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/omegaatt36/ytea/domain"
)

// newFakePlaylist serves playlist reads and applies playlist-move the way mpv
// does: the entry at from takes the place of the entry at to, and the playing
// position follows its entry. Only moves are recorded in ops.
func newFakePlaylist(t *testing.T, list *[]string, pos *int, ops *[][]any) *Player {
	t.Helper()
	return &Player{client: newFakePair(t, func(req request) string {
		switch {
		case req.Command[0] == "get_property" && req.Command[1] == PropPlaylist:
			entries := make([]domain.PlaylistEntry, len(*list))
			for i, f := range *list {
				entries[i] = domain.PlaylistEntry{Filename: f}
			}
			data, _ := json.Marshal(entries)
			return fmt.Sprintf(`{"request_id":%d,"error":"success","data":%s}`, req.RequestID, data)
		case req.Command[0] == "get_property" && req.Command[1] == PropPlaylistPos:
			return fmt.Sprintf(`{"request_id":%d,"error":"success","data":%d}`, req.RequestID, *pos)
		case req.Command[0] != "playlist-move":
			return fmt.Sprintf(`{"request_id":%d,"error":"unexpected command"}`, req.RequestID)
		}
		*ops = append(*ops, req.Command)
		from, to := int(req.Command[1].(float64)), int(req.Command[2].(float64))
		playing := ""
		if *pos >= 0 {
			playing = (*list)[*pos]
		}
		entry := (*list)[from]
		*list = slices.Delete(*list, from, from+1)
		if to > from {
			to--
		}
		*list = slices.Insert(*list, to, entry)
		if playing != "" {
			*pos = slices.Index(*list, playing)
		}
		return fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID)
	})}
}

var queue = []string{"A", "B", "C", "D", "E"}

func tailOrder(n, after int, r *rand.Rand) []int {
	order := make([]int, n)
	for i, j := range r.Perm(n - after - 1) {
		order[after+1+i] = after + 1 + j
	}
	for i := 0; i <= after; i++ {
		order[i] = i
	}
	return order
}

func shuffleWithSeed(t *testing.T, seed uint64, after int) (list []string, order []int, ops [][]any) {
	t.Helper()
	list = slices.Clone(queue)
	pos, current := after, ""
	if after >= 0 {
		current = list[after]
	}
	p := newFakePlaylist(t, &list, &pos, &ops)
	order = tailOrder(len(list), after, rand.New(rand.NewPCG(seed, seed)))
	if err := p.Reorder(context.Background(), after, current, order); err != nil {
		t.Fatalf("seed %d: Reorder(%v) error = %v", seed, order, err)
	}
	return list, order, ops
}

func TestShuffleKeepsCurrentAndEarlierTracks(t *testing.T) {
	reordered := false
	for seed := range uint64(20) {
		got, order, ops := shuffleWithSeed(t, seed, 1)
		if got[0] != "A" || got[1] != "B" {
			t.Fatalf("seed %d: queue = %v, want A at 0 and B at 1", seed, got)
		}
		if tail := slices.Sorted(slices.Values(got[2:])); !slices.Equal(tail, []string{"C", "D", "E"}) {
			t.Fatalf("seed %d: indices 2-4 = %v, want {C D E}", seed, got[2:])
		}
		for _, op := range ops {
			if op[0] != "playlist-move" || op[1].(float64) <= 1 || op[2].(float64) <= 1 {
				t.Fatalf("seed %d: command %v touches the current track or is not a move", seed, op)
			}
		}
		projected := make([]string, len(order))
		for i, j := range order {
			projected[i] = queue[j]
		}
		if !slices.Equal(got, projected) {
			t.Fatalf("seed %d: mpv queue = %v, order projects %v", seed, got, projected)
		}
		reordered = reordered || !slices.Equal(got, queue)
	}
	if !reordered {
		t.Error("20 seeds never reordered the tail")
	}
}

func TestShuffleWithoutCurrentTrackShufflesAll(t *testing.T) {
	firstMoved := false
	for seed := range uint64(20) {
		got, _, _ := shuffleWithSeed(t, seed, -1)
		if sorted := slices.Sorted(slices.Values(got)); !slices.Equal(sorted, queue) {
			t.Fatalf("seed %d: queue = %v, want a permutation of %v", seed, got, queue)
		}
		firstMoved = firstMoved || got[0] != "A"
	}
	if !firstMoved {
		t.Error("20 seeds never moved the first track; the whole queue is not shuffled")
	}
}

func TestReorderRejectsNonPermutation(t *testing.T) {
	var list []string
	var ops [][]any
	pos := -1
	p := newFakePlaylist(t, &list, &pos, &ops)
	for _, order := range [][]int{{0, 0}, {0, 2}, {-1, 0}} {
		if err := p.Reorder(context.Background(), -1, "", order); err == nil {
			t.Errorf("Reorder(%v) error = nil, want an error", order)
		}
	}
	if len(ops) != 0 {
		t.Errorf("rejected orders sent %v", ops)
	}
}

// R5: the shuffle was computed against a current track; if mpv has moved on
// or the playlist changed since, no entry may be moved.
func TestReorderRefusesStaleAnchor(t *testing.T) {
	for _, tt := range []struct {
		name    string
		list    []string
		mpvPos  int
		pos     int
		current string
	}{
		{"mpv advanced to the next track", []string{"A", "B", "C", "D", "E"}, 2, 1, "B"},
		{"another entry at the expected index", []string{"A", "C", "B", "D", "E"}, 1, 1, "B"},
		{"mpv playing while nothing was expected", []string{"A", "B", "C", "D", "E"}, 0, -1, ""},
		{"nothing playing while a track was expected", []string{"A", "B", "C", "D", "E"}, -1, 1, "B"},
		{"playlist length changed", []string{"A", "B", "C", "D", "E", "F"}, 1, 1, "B"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			list := slices.Clone(tt.list)
			mpvPos := tt.mpvPos
			var ops [][]any
			p := newFakePlaylist(t, &list, &mpvPos, &ops)
			order := []int{0, 1, 4, 3, 2}
			if err := p.Reorder(context.Background(), tt.pos, tt.current, order); err == nil {
				t.Error("Reorder() error = nil, want a stale-queue error")
			}
			if len(ops) != 0 {
				t.Errorf("stale reorder sent %v", ops)
			}
			if !slices.Equal(list, tt.list) {
				t.Errorf("playlist = %v, want untouched %v", list, tt.list)
			}
		})
	}
}

func TestReorderMovesWhenAnchorMatches(t *testing.T) {
	list := []string{"A", "B", "C", "D", "E"}
	pos := 1
	var ops [][]any
	p := newFakePlaylist(t, &list, &pos, &ops)
	if err := p.Reorder(context.Background(), 1, "B", []int{0, 1, 4, 3, 2}); err != nil {
		t.Fatalf("Reorder() error = %v", err)
	}
	if want := []string{"A", "B", "E", "D", "C"}; !slices.Equal(list, want) || pos != 1 {
		t.Errorf("playlist = %v playing %d, want %v playing 1", list, pos, want)
	}
}

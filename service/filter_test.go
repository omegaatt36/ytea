package service

import (
	"reflect"
	"testing"

	"github.com/omegaatt36/ytea/domain"
)

func matchIndices(ms []Match) []int {
	out := make([]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Index)
	}
	return out
}

func TestFilterTracks(t *testing.T) {
	tracks := []domain.Track{
		{Title: "Lofi Hip Hop Radio", Channel: "Lofi Girl"},
		{Title: "Rock Classics", Channel: "Rock Channel"},
		{Title: "lofi beats to study", Channel: "ChilledCow"},
		{Title: "Jazz Night", Channel: "LOFI jazz collective"},
	}
	tests := []struct {
		name  string
		query string
		want  []int
	}{
		{name: "case-insensitive channel match", query: "chilledcow", want: []int{2}},
		{name: "channel match upper-case query", query: "COLLECTIVE", want: []int{3}},
		{name: "title or channel, order preserved", query: "lofi", want: []int{0, 2, 3}},
		{name: "multi-term AND", query: "lofi study", want: []int{2}},
		{name: "multi-term AND any order", query: "study lofi", want: []int{2}},
		{name: "terms may match across title and channel", query: "jazz night lofi", want: []int{3}},
		{name: "one term missing excludes row", query: "rock lofi", want: []int{}},
		{name: "extra spaces between terms", query: "  study   lofi ", want: []int{2}},
		{name: "empty query matches all", query: "", want: []int{0, 1, 2, 3}},
		{name: "whitespace query matches all", query: "   ", want: []int{0, 1, 2, 3}},
		{name: "no match", query: "metal", want: []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchIndices(FilterTracks(tracks, tt.query))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FilterTracks(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestFilterTracksSpans(t *testing.T) {
	tracks := []domain.Track{{Title: "Lofi Beats", Channel: "Chill LOFI"}}
	got := FilterTracks(tracks, "lofi beat")
	want := []Match{{
		Index:   0,
		Title:   []Span{{0, 4}, {5, 9}},
		Channel: []Span{{6, 10}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterTracks spans = %+v, want %+v", got, want)
	}
}

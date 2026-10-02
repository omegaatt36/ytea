package service

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func TestParseSeekTarget(t *testing.T) {
	total := 5 * time.Minute
	for _, tt := range []struct {
		in   string
		want time.Duration
	}{
		{"50%", 150 * time.Second},
		{" 0% ", 0},
		{"90", 90 * time.Second},
		{"1:23", 83 * time.Second},
		{"0:01:05", 65 * time.Second},
		{"4:59.5", 299500 * time.Millisecond},
	} {
		got, err := ParseSeekTarget(tt.in, total)
		if err != nil || got != tt.want {
			t.Errorf("ParseSeekTarget(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "abc", "1:75", "-5", "101%", "1:2:3:4", "6:00"} {
		if got, err := ParseSeekTarget(in, total); err == nil {
			t.Errorf("ParseSeekTarget(%q) = %v, want an error", in, got)
		}
	}
}

func TestTailShuffleKeepsPrefixAndPermutes(t *testing.T) {
	moved := false
	for seed := range uint64(20) {
		order := tailShuffle(8, 2, rand.New(rand.NewPCG(seed, seed)))
		if !slices.Equal(order[:3], []int{0, 1, 2}) {
			t.Fatalf("seed %d: order = %v, want 0..2 in place", seed, order)
		}
		if sorted := slices.Sorted(slices.Values(order)); !slices.Equal(sorted, []int{0, 1, 2, 3, 4, 5, 6, 7}) {
			t.Fatalf("seed %d: order = %v, not a permutation", seed, order)
		}
		moved = moved || !slices.IsSorted(order)
	}
	if !moved {
		t.Error("20 seeds never reordered the tail")
	}
}

func TestTailShuffleWithoutCurrentShufflesAll(t *testing.T) {
	first := false
	for seed := range uint64(20) {
		first = first || tailShuffle(5, -1, rand.New(rand.NewPCG(seed, seed)))[0] != 0
	}
	if !first {
		t.Error("20 seeds never moved the first entry")
	}
}

func TestTailShuffleIsSeedable(t *testing.T) {
	a := tailShuffle(10, 2, rand.New(rand.NewPCG(7, 7)))
	b := tailShuffle(10, 2, rand.New(rand.NewPCG(7, 7)))
	if len(a) != 10 || !slices.Equal(a, b) {
		t.Errorf("same seed gave %v and %v, want equal orders of 10", a, b)
	}
}

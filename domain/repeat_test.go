package domain

import (
	"slices"
	"testing"
)

func TestRepeatNextCycles(t *testing.T) {
	got := []Repeat{RepeatOff}
	for range 3 {
		got = append(got, got[len(got)-1].Next())
	}
	if want := []Repeat{RepeatOff, RepeatAll, RepeatOne, RepeatOff}; !slices.Equal(got, want) {
		t.Errorf("Next() cycle = %v, want %v", got, want)
	}
}

func TestRepeatZeroValueActsAsOff(t *testing.T) {
	var r Repeat
	if got := r.Next(); got != RepeatAll {
		t.Errorf("zero Repeat.Next() = %q, want %q", got, RepeatAll)
	}
}

func TestParseRepeat(t *testing.T) {
	tests := map[string]Repeat{
		"all":  RepeatAll,
		"one":  RepeatOne,
		"off":  RepeatOff,
		"":     RepeatOff,
		"loop": RepeatOff,
		"ALL":  RepeatOff,
	}
	for in, want := range tests {
		if got := ParseRepeat(in); got != want {
			t.Errorf("ParseRepeat(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRepeatFrom(t *testing.T) {
	tests := []struct {
		loopPlaylist, loopFile bool
		want                   Repeat
	}{
		{want: RepeatOff},
		{loopPlaylist: true, want: RepeatAll},
		{loopFile: true, want: RepeatOne},
		{loopPlaylist: true, loopFile: true, want: RepeatOne},
	}
	for _, tt := range tests {
		if got := RepeatFrom(tt.loopPlaylist, tt.loopFile); got != tt.want {
			t.Errorf("RepeatFrom(%v, %v) = %v, want %v", tt.loopPlaylist, tt.loopFile, got, tt.want)
		}
	}
}

package mpv

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestSetRepeatProperties(t *testing.T) {
	tests := []struct {
		mode                   Repeat
		loopFile, loopPlaylist string
	}{
		{mode: RepeatOff, loopFile: "no", loopPlaylist: "no"},
		{mode: RepeatAll, loopFile: "no", loopPlaylist: "inf"},
		{mode: RepeatOne, loopFile: "inf", loopPlaylist: "no"},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			var ops [][]any
			c := newFakePair(t, func(req request) string {
				ops = append(ops, req.Command)
				return fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID)
			})
			if err := (&Player{client: c}).SetRepeat(context.Background(), tt.mode); err != nil {
				t.Fatalf("SetRepeat() error = %v", err)
			}
			// loop-file goes first so switching all → one never passes through off.
			want := [][]any{
				{"set_property", PropLoopFile, tt.loopFile},
				{"set_property", PropLoopPlaylist, tt.loopPlaylist},
			}
			gotJSON, _ := json.Marshal(ops)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("SetRepeat(%v) commands = %s, want %s", tt.mode, gotJSON, wantJSON)
			}
		})
	}
}

func TestRepeatObservesLoopProperties(t *testing.T) {
	for _, prop := range []string{PropLoopPlaylist, PropLoopFile} {
		if !slices.Contains(observed, prop) {
			t.Errorf("observed = %v, missing %s", observed, prop)
		}
	}
}

// Values are what mpv reports: false for no, "inf" for inf and yes, a count, or "force".
func TestRepeatFromReportedValues(t *testing.T) {
	tests := []struct {
		loopPlaylist, loopFile string
		want                   Repeat
	}{
		{loopPlaylist: `false`, loopFile: `false`, want: RepeatOff},
		{loopPlaylist: `null`, loopFile: `null`, want: RepeatOff},
		{loopPlaylist: `"no"`, loopFile: `0`, want: RepeatOff},
		{loopPlaylist: `"inf"`, loopFile: `false`, want: RepeatAll},
		{loopPlaylist: `3`, loopFile: `false`, want: RepeatAll},
		{loopPlaylist: `"force"`, loopFile: `false`, want: RepeatAll},
		{loopPlaylist: `false`, loopFile: `"inf"`, want: RepeatOne},
		{loopPlaylist: `"inf"`, loopFile: `"inf"`, want: RepeatOne},
		{loopPlaylist: `false`, loopFile: `2`, want: RepeatOne},
	}
	for _, tt := range tests {
		got := RepeatFrom(LoopOn(json.RawMessage(tt.loopPlaylist)), LoopOn(json.RawMessage(tt.loopFile)))
		if got != tt.want {
			t.Errorf("loop-playlist=%s loop-file=%s: mode = %v, want %v", tt.loopPlaylist, tt.loopFile, got, tt.want)
		}
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

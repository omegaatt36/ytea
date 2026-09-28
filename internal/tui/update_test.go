package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestApplyPlaylistPos(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "index", data: "2", want: 2},
		{name: "none selected", data: "-1", want: -1},
		{name: "unavailable", data: "null", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.applyProperty(mpv.Event{Name: "property-change", Prop: mpv.PropPlaylistPos, Data: json.RawMessage(tt.data)})
			if m.queue.pos != tt.want {
				t.Errorf("pos = %d, want %d", m.queue.pos, tt.want)
			}
		})
	}
}

func TestWaitMPVDropsTimePosWithinShownSecond(t *testing.T) {
	timePos := func(data string) mpv.Event {
		return mpv.Event{Name: "property-change", Prop: mpv.PropTimePos, Data: json.RawMessage(data)}
	}
	tests := []struct {
		name   string
		shown  time.Duration
		events []mpv.Event
		want   tea.Msg
	}{
		{
			name:   "same second is dropped",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.2"), timePos("12.4"), timePos("12.6")},
			want:   mpvEventMsg(timePos("12.6")),
		},
		{
			name:   "backward seek into a new second",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("3.1")},
			want:   mpvEventMsg(timePos("3.1")),
		},
		{
			name:   "unavailable",
			shown:  0,
			events: []mpv.Event{timePos("null")},
			want:   mpvEventMsg(timePos("null")),
		},
		{
			name:   "other events pass",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.1"), {Name: "playback-restart"}},
			want:   mpvEventMsg(mpv.Event{Name: "playback-restart"}),
		},
		{
			name:   "closed after dropped events",
			shown:  12 * time.Second,
			events: []mpv.Event{timePos("12.1")},
			want:   mpvClosedMsg{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan mpv.Event, len(tt.events))
			for _, ev := range tt.events {
				ch <- ev
			}
			close(ch)
			got := waitMPV(ch, tt.shown)()
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("waitMPV() = %v, want %v", got, tt.want)
			}
		})
	}
}

// queue-repeat-shuffle R1, R2: L asks mpv for the next mode; only mpv's loop-* reports change the display.
func TestRepeatKeyCyclesThroughMPV(t *testing.T) {
	for _, f := range []focus{focusResults, focusQueue, focusPlaylists} {
		player := &spyPlayer{}
		m := New(Deps{Player: player})
		m.focus = f
		got, cmd := m.update(keyPress("L"))
		m = got.(Model)
		if cmd == nil {
			t.Fatalf("focus %d: L returned no command", f)
		}
		cmd()
		if !slices.Equal(player.repeats, []mpv.Repeat{mpv.RepeatAll}) {
			t.Fatalf("focus %d: SetRepeat calls = %v, want [all]", f, player.repeats)
		}
		if strings.Contains(m.modeLine(), "repeat") {
			t.Fatalf("focus %d: display changed before mpv reported: %q", f, m.modeLine())
		}
	}

	player := &spyPlayer{}
	m := New(Deps{Player: player})
	m.focus = focusResults
	steps := []struct {
		prop, value string
		display     string
		next        mpv.Repeat
	}{
		{mpv.PropLoopPlaylist, `"inf"`, "repeat all", mpv.RepeatOne},
		{mpv.PropLoopFile, `"inf"`, "repeat one", mpv.RepeatOff},
		{mpv.PropLoopPlaylist, `false`, "repeat one", mpv.RepeatOff},
		{mpv.PropLoopFile, `false`, "", mpv.RepeatAll},
	}
	for _, step := range steps {
		m.applyProperty(mpv.Event{Name: "property-change", Prop: step.prop, Data: json.RawMessage(step.value)})
		line := m.modeLine()
		if step.display == "" && strings.Contains(line, "repeat") || step.display != "" && !strings.Contains(line, step.display) {
			t.Fatalf("after %s=%s audio line = %q, want %q", step.prop, step.value, line, step.display)
		}
		got, cmd := m.update(keyPress("L"))
		m = got.(Model)
		cmd()
		if last := player.repeats[len(player.repeats)-1]; last != step.next {
			t.Fatalf("after %s=%s L requested %v, want %v", step.prop, step.value, last, step.next)
		}
	}
}

func TestPlaybackErrorClearedOnFileLoaded(t *testing.T) {
	m := New(Deps{})
	m.applyEvent(mpv.Event{Name: "end-file", Reason: "error", FileError: "loading failed"})
	if !m.statusErr || !strings.Contains(m.status, "playback failed: loading failed") {
		t.Fatalf("statusErr = %v, status = %q, want error status", m.statusErr, m.status)
	}

	m.applyEvent(mpv.Event{Name: "file-loaded"})
	if m.statusErr || m.status != "" {
		t.Errorf("after file-loaded: statusErr = %v, status = %q, want empty", m.statusErr, m.status)
	}
}

func TestPlaybackErrorClearedOnPlaybackRestart(t *testing.T) {
	m := New(Deps{})
	m.applyEvent(mpv.Event{Name: "end-file", Reason: "error", FileError: "loading failed"})
	if !m.statusErr || !strings.Contains(m.status, "playback failed: loading failed") {
		t.Fatalf("statusErr = %v, status = %q, want error status", m.statusErr, m.status)
	}

	m.applyEvent(mpv.Event{Name: "playback-restart"})
	if m.statusErr || m.status != "" {
		t.Errorf("after playback-restart: statusErr = %v, status = %q, want empty", m.statusErr, m.status)
	}
}

func TestStatusTimeoutDismissesError(t *testing.T) {
	m := New(Deps{})
	m.setError("something failed")
	v := m.statusVersion

	// Update schedules a timer tick
	got, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	gm := got.(Model)
	if cmd == nil {
		t.Fatal("Update returned nil cmd, want status timeout tick")
	}

	// Stale timeout message is ignored
	gotStale, _ := gm.Update(statusTimeoutMsg{version: v - 1})
	if gotStale.(Model).status == "" {
		t.Error("stale statusTimeoutMsg cleared status")
	}

	// Matching timeout message clears status
	gotFresh, _ := gm.Update(statusTimeoutMsg{version: v})
	mFresh := gotFresh.(Model)
	if mFresh.status != "" || mFresh.statusErr {
		t.Errorf("status = %q, statusErr = %v, want cleared", mFresh.status, mFresh.statusErr)
	}
}

func TestMPRISSync(t *testing.T) {
	spy := &spyMPRIS{}
	m := New(Deps{MPRIS: spy})
	m.queue.entries = []mpv.PlaylistEntry{{Filename: "https://example.com/song"}}
	m.queue.pos = 0
	m.player.idle = false
	m.tracks["https://example.com/song"] = youtube.Track{Title: "Song Title", Channel: "Artist"}

	m.syncMPRIS()

	if len(spy.updates) == 0 {
		t.Fatal("expected MPRIS update, got none")
	}
	last := spy.updates[len(spy.updates)-1]
	if last.Title != "Song Title" || last.Artist != "Artist" {
		t.Errorf("MPRIS state = %+v, want title and artist", last)
	}
}

func TestMPRISSeeked(t *testing.T) {
	spy := &spyMPRIS{}
	m := New(Deps{MPRIS: spy})
	m.player.timePos = 15 * time.Second

	m.applyEvent(mpv.Event{Name: "playback-restart"})

	if len(spy.seeked) == 0 {
		t.Fatal("expected MPRIS Seeked, got none")
	}
	if spy.seeked[0] != 15*time.Second {
		t.Errorf("MPRIS seeked = %v, want 15s", spy.seeked[0])
	}
}

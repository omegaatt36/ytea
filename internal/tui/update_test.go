package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

func TestApplyPlaylistPos(t *testing.T) {
	tests := []struct {
		name string
		pos  int
		want int
	}{
		{name: "index", pos: 2, want: 2},
		{name: "none selected", pos: -1, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Deps{})
			m.applyChange(service.QueuePosChanged{Pos: tt.pos})
			if m.core.Queue.Pos != tt.want {
				t.Errorf("pos = %d, want %d", m.core.Queue.Pos, tt.want)
			}
		})
	}
}

func TestWaitPlayerDropsPositionWithinShownSecond(t *testing.T) {
	pos := func(d time.Duration) service.PlayerEvent { return service.PositionChanged{Pos: d} }
	tests := []struct {
		name   string
		shown  time.Duration
		events []service.PlayerEvent
		want   tea.Msg
	}{
		{
			name:   "same second is dropped",
			shown:  12 * time.Second,
			events: []service.PlayerEvent{pos(12200 * time.Millisecond), pos(12400 * time.Millisecond), pos(12600 * time.Millisecond)},
			want:   playerEventMsg{pos(12600 * time.Millisecond)},
		},
		{
			name:   "backward seek into a new second",
			shown:  12 * time.Second,
			events: []service.PlayerEvent{pos(3100 * time.Millisecond)},
			want:   playerEventMsg{pos(3100 * time.Millisecond)},
		},
		{
			name:   "other events pass",
			shown:  12 * time.Second,
			events: []service.PlayerEvent{pos(12100 * time.Millisecond), service.PlaybackRestarted{}},
			want:   playerEventMsg{service.PlaybackRestarted{}},
		},
		{
			name:   "closed after dropped events",
			shown:  12 * time.Second,
			events: []service.PlayerEvent{pos(12100 * time.Millisecond)},
			want:   playerClosedMsg{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan service.PlayerEvent, len(tt.events))
			for _, ev := range tt.events {
				ch <- ev
			}
			close(ch)
			got := waitPlayer(ch, tt.shown)()
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("waitPlayer() = %v, want %v", got, tt.want)
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
		if !slices.Equal(player.repeats, []domain.Repeat{domain.RepeatAll}) {
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
		ev      service.PlayerEvent
		display string
		next    domain.Repeat
	}{
		{service.LoopPlaylistChanged{On: true}, "repeat all", domain.RepeatOne},
		{service.LoopFileChanged{On: true}, "repeat one", domain.RepeatOff},
		{service.LoopPlaylistChanged{}, "repeat one", domain.RepeatOff},
		{service.LoopFileChanged{}, "", domain.RepeatAll},
	}
	for _, step := range steps {
		m.applyChange(step.ev)
		line := m.modeLine()
		if step.display == "" && strings.Contains(line, "repeat") || step.display != "" && !strings.Contains(line, step.display) {
			t.Fatalf("after %#v audio line = %q, want %q", step.ev, line, step.display)
		}
		got, cmd := m.update(keyPress("L"))
		m = got.(Model)
		cmd()
		if last := player.repeats[len(player.repeats)-1]; last != step.next {
			t.Fatalf("after %#v L requested %v, want %v", step.ev, last, step.next)
		}
	}
}

func TestPlaybackErrorClearedOnFileLoaded(t *testing.T) {
	m := New(Deps{})
	m.applyEvent(service.PlaybackFailed{Reason: "loading failed"})
	if !m.statusErr || !strings.Contains(m.status, "playback failed: loading failed") {
		t.Fatalf("statusErr = %v, status = %q, want error status", m.statusErr, m.status)
	}

	m.applyEvent(service.FileLoaded{})
	if m.statusErr || m.status != "" {
		t.Errorf("after file-loaded: statusErr = %v, status = %q, want empty", m.statusErr, m.status)
	}
}

func TestPlaybackErrorClearedOnPlaybackRestart(t *testing.T) {
	m := New(Deps{})
	m.applyEvent(service.PlaybackFailed{Reason: "loading failed"})
	if !m.statusErr || !strings.Contains(m.status, "playback failed: loading failed") {
		t.Fatalf("statusErr = %v, status = %q, want error status", m.statusErr, m.status)
	}

	m.applyEvent(service.PlaybackRestarted{})
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
	m.core.Queue.Entries = []domain.PlaylistEntry{{Filename: "https://example.com/song"}}
	m.core.Queue.Pos = 0
	m.core.Playback.Idle = false
	m.core.Tracks["https://example.com/song"] = domain.Track{Title: "Song Title", Channel: "Artist"}

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
	m.core.Playback.TimePos = 15 * time.Second

	m.applyEvent(service.PlaybackRestarted{})

	if len(spy.seeked) == 0 {
		t.Fatal("expected MPRIS Seeked, got none")
	}
	if spy.seeked[0] != 15*time.Second {
		t.Errorf("MPRIS seeked = %v, want 15s", spy.seeked[0])
	}
}

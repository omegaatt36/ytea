package tui

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/youtube"
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
		got, err := parseSeekTarget(tt.in, total)
		if err != nil || got != tt.want {
			t.Errorf("parseSeekTarget(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "abc", "1:75", "-5", "101%", "1:2:3:4", "6:00"} {
		if got, err := parseSeekTarget(in, total); err == nil {
			t.Errorf("parseSeekTarget(%q) = %v, want an error", in, got)
		}
	}
}

func seekModel(t *testing.T) (Model, *spyPlayer) {
	t.Helper()
	m := overlayModel(t, focusQueue)
	player := m.deps.Player.(*spyPlayer)
	m.player.duration, m.player.timePos = 4*time.Minute, 30*time.Second
	return m, player
}

func TestDigitsSeekToPercent(t *testing.T) {
	m, player := seekModel(t)
	_, cmd := m.update(keyPress("7"))
	runCmd(cmd)
	if !slices.Equal(player.calls, []string{"seek 70%"}) {
		t.Errorf("7: calls %q, want seek 70%%", player.calls)
	}

	m.tracks[watchURL] = youtube.Track{Title: "Song", URL: watchURL, Live: true}
	if _, cmd := m.update(keyPress("5")); cmd != nil {
		t.Error("a digit sought within a live stream")
	}
}

func TestGoToSeeksToTypedTime(t *testing.T) {
	m, player := seekModel(t)
	m = press(t, m, keyPress("t"))
	if m.overlay != overlayName || m.nameMode != nameSeek || !strings.Contains(rendered(m), "Seek to") {
		t.Fatalf("t: overlay=%v mode=%v, want the Seek to dialog", m.overlay, m.nameMode)
	}
	// Digits type into the dialog rather than seeking.
	m = typeText(t, m, "9:99")
	if len(player.calls) != 0 {
		t.Fatalf("typing sent %q", player.calls)
	}
	m = press(t, m, keyPress("enter"))
	if m.overlay != overlayName || !m.statusErr {
		t.Fatalf("invalid time closed the dialog: overlay=%v status=%q", m.overlay, m.status)
	}
	m.nameInput.SetValue("1:30")
	got, cmd := m.update(keyPress("enter"))
	m = got.(Model)
	runCmd(cmd)
	if m.overlay != overlayNone || !slices.Equal(player.calls, []string{"seek to 1m30s"}) {
		t.Errorf("enter: overlay=%v calls=%q", m.overlay, player.calls)
	}
}

func TestGoToNeedsASeekableTrack(t *testing.T) {
	m := New(Deps{Player: &spyPlayer{}})
	m.input.Blur()
	m.focus = focusQueue
	m = press(t, m, keyPress("t"))
	if m.overlay != overlayNone {
		t.Error("t opened the seek dialog with nothing playing")
	}
}

func TestClickingTheProgressBarSeeks(t *testing.T) {
	m, player := seekModel(t)
	bar := m.progressBar()
	if bar.Empty() {
		t.Fatal("no progress bar laid out")
	}
	if line := strings.Split(rendered(m), "\n")[bar.Min.Y]; !strings.Contains(line, "━") && !strings.Contains(line, "─") {
		t.Fatalf("progress row %d is %q", bar.Min.Y, line)
	}
	_, cmd := m.Update(tea.MouseClickMsg{X: bar.Min.X + bar.Dx()/2, Y: bar.Min.Y, Button: tea.MouseLeft})
	runCmd(cmd)
	if len(player.calls) != 1 || !strings.HasPrefix(player.calls[0], "seek to 2m") {
		t.Errorf("click in the middle: calls %q, want a seek to about 2m", player.calls)
	}

	player.calls = nil
	m = click(m, image.Pt(bar.Min.X-1, bar.Min.Y))
	if len(player.calls) != 0 {
		t.Errorf("click beside the bar sought: %q", player.calls)
	}
	m.queue.entries = nil
	if !m.progressBar().Empty() {
		t.Error("progress bar laid out with nothing playing")
	}
}

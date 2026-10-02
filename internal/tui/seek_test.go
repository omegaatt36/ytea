package tui

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/domain"
)

func seekModel(t *testing.T) (Model, *spyPlayer) {
	t.Helper()
	m := overlayModel(t, focusQueue)
	player := m.deps.Player.(*spyPlayer)
	m.core.Playback.Duration, m.core.Playback.TimePos = 4*time.Minute, 30*time.Second
	return m, player
}

func TestDigitsSeekToPercent(t *testing.T) {
	m, player := seekModel(t)
	_, cmd := m.update(keyPress("7"))
	runCmd(cmd)
	if !slices.Equal(player.calls, []string{"seek 70%"}) {
		t.Errorf("7: calls %q, want seek 70%%", player.calls)
	}

	m.core.Tracks[watchURL] = domain.Track{Title: "Song", URL: watchURL, Live: true}
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
	m.core.Queue.Entries = nil
	if !m.progressBar().Empty() {
		t.Error("progress bar laid out with nothing playing")
	}
}

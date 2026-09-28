package tui

import (
	"encoding/json"
	"fmt"
	"image"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "unknown", in: 0, want: "--:--"},
		{name: "minutes", in: 3*time.Minute + 5*time.Second, want: "3:05"},
		{name: "rounds", in: 59*time.Second + 600*time.Millisecond, want: "1:00"},
		{name: "hours", in: 8*time.Hour + 28*time.Second, want: "8:00:28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.in); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderSpectrumSize(t *testing.T) {
	m := New(Deps{})
	m.levels = []float64{0, 0.25, 0.5, 1}
	got := m.renderSpectrum(40, detailRows)
	if w, h := lipgloss.Width(got), lipgloss.Height(got); w != 40 || h != detailRows {
		t.Errorf("renderSpectrum() size = %dx%d, want 40x%d", w, h, detailRows)
	}
}

func TestRestoredMetadataTitlesUnplayedQueueEntries(t *testing.T) {
	m := New(Deps{InitialTracks: map[string]youtube.Track{
		watchURL: {URL: watchURL, ID: "abc", Title: "Saved song", Channel: "Artist"},
	}})
	m.applyProperty(mpv.Event{Prop: mpv.PropPlaylist, Data: json.RawMessage(`[{"filename":"` + watchURL + `"}]`)})
	for _, detailed := range []bool{false, true} {
		if got := m.queueLine(0, m.queueTracks()[0], 60, 5, detailed, false); !strings.Contains(got, "Saved song") || strings.Contains(got, watchURL) {
			t.Errorf("restored queue line (detailed=%v) = %q, want saved title", detailed, got)
		}
	}
}

func TestQueueTabShowsDetailedQueueFullWidth(t *testing.T) {
	m := mouseModel()
	m.help.SetWidth(m.width)
	m.input.Blur()
	m.tracks["u1"] = youtube.Track{Title: "queued 1", Channel: "Some Channel", Duration: 256 * time.Second}

	m.focus = focusResults
	split := rendered(m)
	if !strings.Contains(split, "song 00") || strings.Contains(split, "Some Channel") {
		t.Fatalf("results tab should show results beside a compact queue:\n%s", split)
	}

	m.focus = focusQueue
	got := rendered(m)
	if strings.Contains(got, "song 00") {
		t.Errorf("queue tab still renders search results:\n%s", got)
	}
	if !regexp.MustCompile(`2 queued 1 +Some Channel +4:16`).MatchString(got) {
		t.Errorf("queue tab row lacks position, channel or length:\n%s", got)
	}
}

func TestNarrowResultsTabDropsQueuePane(t *testing.T) {
	m := mouseModel()
	m.input.Blur()
	m.focus = focusResults

	m.width = splitMinWidth - 1
	if got := rendered(m); strings.Contains(got, "Queue (") || !strings.Contains(got, "song 00") {
		t.Errorf("width %d: want Results alone:\n%s", m.width, got)
	}
	m.width = splitMinWidth
	if got := rendered(m); !strings.Contains(got, "Queue (3)") || !strings.Contains(got, "song 00") {
		t.Errorf("width %d: want Results beside the queue:\n%s", m.width, got)
	}
}

// Every screen fills the window exactly: no row is wider, and the row count
// matches so the terminal never scrolls or leaves stale rows.
func TestRenderFillsWindow(t *testing.T) {
	type screen struct {
		name    string
		focus   focus
		overlay overlay
		help    bool
	}
	screens := []screen{
		{name: "search", focus: focusSearch},
		{name: "results", focus: focusResults},
		{name: "queue", focus: focusQueue},
		{name: "playlists", focus: focusPlaylists},
		{name: "playlist tracks", focus: focusPlaylistTracks},
		{name: "devices", focus: focusQueue, overlay: overlayDevices},
		{name: "info", focus: focusQueue, overlay: overlayInfo},
		{name: "name", focus: focusQueue, overlay: overlayName},
		{name: "picker", focus: focusQueue, overlay: overlayPicker},
		{name: "full help", focus: focusQueue, help: true},
	}
	for _, size := range []image.Point{{40, 24}, {80, 24}, {120, 40}} {
		for _, sc := range screens {
			for _, viz := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/%s/viz=%v", size.X, size.Y, sc.name, viz), func(t *testing.T) {
					m := overlayModel(t, sc.focus)
					m.results = []youtube.Track{{Title: "Result", Channel: "Channel", Duration: time.Minute}}
					got, _ := m.update(tea.WindowSizeMsg{Width: size.X, Height: size.Y})
					m = got.(Model)
					m.overlay, m.fullHelp = sc.overlay, sc.help
					m.showViz, m.levels = viz, []float64{0.5, 1}
					lines := strings.Split(m.render(), "\n")
					if len(lines) != size.Y {
						t.Errorf("%d rows, want %d", len(lines), size.Y)
					}
					for i, line := range lines {
						if w := lipgloss.Width(line); w > size.X {
							t.Errorf("row %d is %d wide: %q", i, w, ansi.Strip(line))
						}
					}
				})
			}
		}
	}
}

func TestHourLongLengthKeepsColumnsAligned(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 120, 30
	m.input.Blur()
	m.focus = focusResults
	m.results = []youtube.Track{
		{Title: "short", Channel: "Chan A", Duration: 3 * time.Minute},
		{Title: "long mix", Channel: "Chan B", Duration: 6*time.Hour + 10*time.Minute},
		{Title: "stream", Channel: "Chan C", Live: true},
	}
	var cols []int
	for _, ch := range []string{"Chan A", "Chan B", "Chan C"} {
		cols = append(cols, cellAt(t, m, ch).X)
	}
	if cols[0] != cols[1] || cols[1] != cols[2] {
		t.Errorf("channel columns = %v, want one column", cols)
	}
}

// Only the 16 palette colors follow the terminal theme; a fixed 256-color or
// truecolor SGR would not.
func TestUIColorsComeFromTerminalPalette(t *testing.T) {
	fixed := regexp.MustCompile(`\x1b\[[0-9;]*[34]8;[25];`)
	m := overlayModel(t, focusResults)
	m.showViz, m.levels = true, []float64{0.2, 0.9}
	m.results = []youtube.Track{{Title: "lofi beats", Channel: "Lofi Girl"}, {Title: "rock"}}
	m.filterInput.SetValue("lofi")
	m.setError("search failed")
	for name, mm := range map[string]Model{
		"results":    m,
		"full help":  func() Model { m := m; m.fullHelp = true; return m }(),
		"device":     func() Model { m := m; m.overlay = overlayDevices; return m }(),
		"name input": func() Model { m := m; m.overlay = overlayName; m.nameInput.Focus(); return m }(),
	} {
		if seq := fixed.FindString(mm.render()); seq != "" {
			t.Errorf("%s: render uses fixed color %q", name, seq)
		}
	}
}

func TestStatusRidesPlayerBottomEdge(t *testing.T) {
	m := overlayModel(t, focusQueue)
	edge := func() string {
		lines := strings.Split(rendered(m), "\n")
		return lines[len(lines)-1-footerRows]
	}

	m.setStatus("saved to “Chill”")
	if got := edge(); !strings.HasPrefix(got, "╰─ saved to “Chill” ─") {
		t.Errorf("player bottom edge = %q, want the status on it", got)
	}
	m.setStatus("")
	if got, want := edge(), "╰"+strings.Repeat("─", m.width-2)+"╯"; got != want {
		t.Errorf("player bottom edge without status = %q, want a plain border", got)
	}
}

func TestResultsRenderWithEscapeInChannel(t *testing.T) {
	m, _ := filterModel()
	m = resize(m, 100)
	m.results[0].Channel = "chanchanchanchanchan1\x1b[0m"
	m.results[1].Title = "abc\x1b[31mred\x1b[0m tail"
	pane := m.renderPanes(20)
	for i, line := range strings.Split(pane, "\n") {
		if w := lipgloss.Width(line); w != m.width {
			t.Errorf("line %d width = %d, want %d: %q", i, w, m.width, ansi.Strip(line))
		}
	}
}

package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// seekable is the playing track when it has a known length to seek within.
func (m Model) seekable() bool {
	_, t, ok := m.current()
	return ok && !t.Live && m.player.duration > 0
}

func (m Model) openSeek() (tea.Model, tea.Cmd) {
	if !m.seekable() {
		m.setStatus("nothing seekable playing")
		return m, nil
	}
	return m.openName(nameSeek, "", "seek within "+formatDuration(m.player.duration))
}

func (m Model) submitSeek() (tea.Model, tea.Cmd) {
	if !m.seekable() {
		m.closeName()
		m.setStatus("nothing seekable playing")
		return m, nil
	}
	pos, err := parseSeekTarget(m.nameInput.Value(), m.player.duration)
	if err != nil {
		m.setError(err.Error())
		return m, nil
	}
	m.closeName()
	return m, m.seekTo(pos)
}

func (m *Model) seekTo(pos time.Duration) tea.Cmd {
	p := m.deps.Player
	m.setStatus("seek to " + formatDuration(pos))
	return do(func(ctx context.Context) error { return p.SeekTo(ctx, pos) })
}

// seekPercent jumps to percent of the playing track, as YouTube's digit keys do.
func (m Model) seekPercent(percent int) tea.Cmd {
	if !m.seekable() {
		return nil
	}
	p := m.deps.Player
	return do(func(ctx context.Context) error { return p.SeekPercent(ctx, float64(percent)) })
}

// parseSeekTarget reads "50%", "90" (seconds), "1:23" or "1:02:03" as a
// position within a track of length total.
func parseSeekTarget(s string, total time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if pct, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(strings.TrimSpace(pct), 64)
		if err != nil || v < 0 || v > 100 {
			return 0, fmt.Errorf("invalid percentage %q", s)
		}
		return time.Duration(float64(total) * v / 100), nil
	}
	parts := strings.Split(s, ":")
	if s == "" || len(parts) > 3 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	var secs float64
	for i, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		// Only the leading field may exceed its unit: "90" and "75:00" are fine, "1:75" is not.
		if err != nil || v < 0 || (i > 0 && v >= 60) {
			return 0, fmt.Errorf("invalid time %q", s)
		}
		secs = secs*60 + v
	}
	pos := time.Duration(secs * float64(time.Second))
	if pos > total {
		return 0, errors.New("past the end of " + formatDuration(total))
	}
	return pos, nil
}

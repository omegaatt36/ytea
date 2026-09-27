package tui

import (
	"context"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Opening an overlay replaces any open one and never changes m.focus, so
// closing it returns to the pane focused before the first overlay opened.
type overlay int

const (
	overlayNone overlay = iota
	overlayDevices
	overlayInfo
	overlayPicker
	overlayName
)

func (m Model) handleDeviceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.devices
	switch {
	case key.Matches(msg, k.Up):
		m.deviceCur = max(0, m.deviceCur-1)
	case key.Matches(msg, k.Down):
		m.deviceCur = min(len(m.devices)-1, m.deviceCur+1)
	case key.Matches(msg, k.Close):
		m.overlay = overlayNone
	case key.Matches(msg, k.Select):
		m.overlay = overlayNone
		if m.deviceCur < len(m.devices) {
			d := m.devices[m.deviceCur]
			m.setStatus("output → " + d.Label())
			// Routing mpv itself (rather than the system mixer) makes the choice
			// stick across tracks and survive stream re-creation.
			return m, do(func(ctx context.Context) error {
				return m.deps.Player.SetAudioDevice(ctx, d.Name)
			})
		}
	}
	return m, nil
}

func (m Model) handleInfoKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys.info
	switch {
	case key.Matches(msg, k.Close):
		m.overlay = overlayNone
		return m, nil
	case key.Matches(msg, k.Copy):
		if e, _, ok := m.current(); ok {
			m.setStatus("copied " + e.Filename)
			// OSC 52: the alt screen with mouse reporting leaves no way to select text.
			return m, tea.SetClipboard(e.Filename)
		}
		return m, nil
	}
	cmd, _ := m.handlePlaybackKey(msg)
	return m, cmd
}

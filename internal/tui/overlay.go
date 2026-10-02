package tui

import (
	"context"
	"os/exec"
	"runtime"

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
		m.deviceCur = min(len(m.core.Devices)-1, m.deviceCur+1)
	case key.Matches(msg, k.Close):
		m.overlay = overlayNone
	case key.Matches(msg, k.Select):
		m.overlay = overlayNone
		if m.deviceCur < len(m.core.Devices) {
			d := m.core.Devices[m.deviceCur]
			m.setStatus("output → " + d.Label())
			// Routing mpv itself (rather than the system mixer) makes the choice
			// stick across tracks and survive stream re-creation.
			return m, teaCmd(m.core.SetAudioDevice(d.Name))
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
		if e, _, ok := m.core.Current(); ok {
			m.setStatus("copied " + e.Filename)
			// OSC 52: the alt screen with mouse reporting leaves no way to select text.
			return m, tea.SetClipboard(e.Filename)
		}
		return m, nil
	case key.Matches(msg, k.Open):
		if e, _, ok := m.core.Current(); ok && e.Filename != "" {
			m.setStatus("opening in browser…")
			return m, m.openURL(e.Filename)
		}
		return m, nil
	}
	cmd, _ := m.handlePlaybackKey(msg)
	return m, cmd
}

func (m Model) openURL(raw string) tea.Cmd {
	return func() tea.Msg {
		fn := m.deps.OpenURL
		if fn == nil {
			fn = defaultOpenURL
		}
		if err := fn(raw); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}

func defaultOpenURL(raw string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", raw)
	case "windows":
		cmd = exec.CommandContext(context.Background(), "rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.CommandContext(context.Background(), "xdg-open", raw)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

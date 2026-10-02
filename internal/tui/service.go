package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/service"
)

func teaCmd(c service.Cmd) tea.Cmd {
	if c == nil {
		return nil
	}
	return func() tea.Msg { return c() }
}

func teaCmds(cmds []service.Cmd) tea.Cmd {
	if len(cmds) == 0 {
		return nil
	}
	wrapped := make([]tea.Cmd, len(cmds))
	for i, cmd := range cmds {
		wrapped[i] = teaCmd(cmd)
	}
	return tea.Batch(wrapped...)
}

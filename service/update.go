package service

// UpdateResult contains core work and state transitions a front end may use to
// keep its view aligned. A command loop only needs Cmds.
type UpdateResult struct {
	Cmds           []Cmd
	SearchAdded    int
	SearchAccepted bool
	SwitchesTrack  bool
}

// Update feeds a command result or player event back into the core. Run the
// returned commands outside the state owner and feed their results back here;
// all state transitions remain on the caller's loop.
func (c *Core) Update(msg Msg) UpdateResult {
	var result UpdateResult
	var cmd Cmd
	switch msg := msg.(type) {
	case PlayerEvent:
		cmd, result.SwitchesTrack = c.ApplyEvent(msg)
		if cmd != nil {
			result.Cmds = append(result.Cmds, cmd)
		}
		cmd = c.RecordPlay()
	case SearchDone:
		result.SearchAdded, result.SearchAccepted = c.SearchDone(msg)
	case LookupDone:
		result.Cmds = c.LookupDone(msg)
	case AppendDone:
		cmd = c.AppendDone(msg)
	case ActionDone, Refreshed, Debounced:
		cmd = c.Queue.Update(msg)
	case AccountPlaylistsDone, AccountTracksDone, AccountQueueFailed:
		c.Account.Update(msg)
	case HistoryRecorded:
		c.History.Reload()
	case DevicesLoaded:
		c.ApplyDevices(msg)
	case StreamLoaded:
		c.ApplyStream(msg)
	}
	if cmd != nil {
		result.Cmds = append(result.Cmds, cmd)
	}
	return result
}

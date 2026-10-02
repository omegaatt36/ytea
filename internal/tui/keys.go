package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
)

type keyMap struct {
	global         globalKeyMap
	playback       playbackKeyMap
	closeHelp      key.Binding
	search         searchKeyMap
	results        resultKeyMap
	filter         filterKeyMap
	queue          queueKeyMap
	playlists      playlistKeyMap
	playlistTracks playlistTrackKeyMap
	history        historyKeyMap
	devices        deviceKeyMap
	info           infoKeyMap
	picker         pickerKeyMap
	name           nameKeyMap
}

type globalKeyMap struct {
	Search, Paste, NextPane, PrevPane, Output, Info, Viz, Radio, GoTo, Help, Quit, ForceQuit key.Binding
	// SeekPercent acts like a playback key, but is listed here, where a
	// narrow full help still has room for it.
	SeekPercent key.Binding
}

type playbackKeyMap struct {
	Pause, SeekBack, SeekForward, Next, Prev, VolumeUp, VolumeDown, Normalize, Repeat key.Binding
}

type searchKeyMap struct {
	Submit, Leave key.Binding
}

type navKeyMap struct {
	Up, Down, Top, Bottom key.Binding
}

type resultKeyMap struct {
	navKeyMap
	Play, Enqueue, Save, More, Filter, ClearFilter key.Binding
}

type filterKeyMap struct {
	Up, Down, Apply, Clear key.Binding
}

type queueKeyMap struct {
	navKeyMap
	Jump, Remove, Clear, MoveUp, MoveDown, Shuffle, Save, SaveQueue key.Binding
}

type playlistKeyMap struct {
	navKeyMap
	Browse, PlayAll, Create, Rename, EnqueueAll, MoveUp, MoveDown, Delete, Reload, Back key.Binding
}

type playlistTrackKeyMap struct {
	navKeyMap
	Play, PlayAll, Enqueue, MoveUp, MoveDown, Remove, Reload, Back key.Binding
}

type historyKeyMap struct {
	navKeyMap
	Play, Enqueue, Save, Remove key.Binding
}

type deviceKeyMap struct {
	Up, Down, Select, Close key.Binding
}

type infoKeyMap struct {
	Open, Copy, Close key.Binding
}

type pickerKeyMap struct {
	navKeyMap
	Save, Create, Cancel key.Binding
}

type nameKeyMap struct {
	Create, Cancel key.Binding
}

func newKeyMap() keyMap {
	nav := navKeyMap{
		Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Top:    key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g/home", "top")),
		Bottom: key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G/end", "bottom")),
	}
	create := key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "new playlist"))
	moveUp := key.NewBinding(key.WithKeys("K", "shift+up"), key.WithHelp("K", "move up"))
	moveDown := key.NewBinding(key.WithKeys("J", "shift+down"), key.WithHelp("J", "move down"))
	return keyMap{
		global: globalKeyMap{
			Search: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
			// ctrl+shift+v and shift+insert are normally the terminal's own paste, but
			// under a kitty-keyboard multiplexer (Zellij) they arrive as key events.
			Paste:    key.NewBinding(key.WithKeys("ctrl+v", "ctrl+shift+v", "shift+insert"), key.WithHelp("ctrl+v", "paste")),
			NextPane: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next pane")),
			PrevPane: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev pane")),
			Output:   key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "output")),
			Info:     key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "info")),
			Viz:      key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "viz mode")),
			Radio:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "radio")),
			GoTo:     key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "go to")),
			// Digit n seeks to n×10%, as on YouTube.
			SeekPercent: key.NewBinding(key.WithKeys("0", "1", "2", "3", "4", "5", "6", "7", "8", "9"), key.WithHelp("0-9", "to %")),
			Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more")),
			Quit:        key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
			ForceQuit:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		},
		// q closes rather than quits, as it does in the device and info overlays.
		closeHelp: key.NewBinding(key.WithKeys("?", "esc", "q"), key.WithHelp("?/esc/q", "close")),
		playback: playbackKeyMap{
			Pause:       key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "pause")),
			SeekBack:    key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "seek -5s")),
			SeekForward: key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "seek +5s")),
			Next:        key.NewBinding(key.WithKeys("n", ">"), key.WithHelp("n", "next")),
			Prev:        key.NewBinding(key.WithKeys("p", "<"), key.WithHelp("p", "prev")),
			VolumeUp:    key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "vol up")),
			VolumeDown:  key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "vol down")),
			Normalize:   key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "leveling")),
			Repeat:      key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "repeat")),
		},
		search: searchKeyMap{
			Submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "search")),
			Leave:  key.NewBinding(key.WithKeys("esc", "tab"), key.WithHelp("esc", "leave")),
		},
		results: resultKeyMap{
			navKeyMap:   nav,
			Play:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "play now")),
			Enqueue:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "queue")),
			Save:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
			More:        key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "more results")),
			Filter:      key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter")),
			ClearFilter: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		},
		filter: filterKeyMap{
			Up:    key.NewBinding(key.WithKeys("up", "ctrl+k"), key.WithHelp("↑/ctrl+k", "up")),
			Down:  key.NewBinding(key.WithKeys("down", "ctrl+j"), key.WithHelp("↓/ctrl+j", "down")),
			Apply: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply filter")),
			Clear: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		},
		queue: queueKeyMap{
			navKeyMap: nav,
			Jump:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "jump")),
			Remove:    key.NewBinding(key.WithKeys("d", "x", "delete"), key.WithHelp("d", "remove")),
			Clear:     key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "clear queue")),
			MoveUp:    moveUp,
			MoveDown:  moveDown,
			Shuffle:   key.NewBinding(key.WithKeys("Z"), key.WithHelp("Z", "shuffle")),
			Save:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save track")),
			SaveQueue: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "save queue")),
		},
		playlists: playlistKeyMap{
			navKeyMap:  nav,
			Browse:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "browse")),
			PlayAll:    key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "play all")),
			Create:     create,
			Rename:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "rename")),
			EnqueueAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "queue all")),
			MoveUp:     moveUp,
			MoveDown:   moveDown,
			Delete:     key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete playlist")),
			Reload:     key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reload YouTube"), key.WithDisabled()),
			Back:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		},
		playlistTracks: playlistTrackKeyMap{
			navKeyMap: nav,
			Play:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "play now")),
			PlayAll:   key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "play all from here")),
			Enqueue:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "queue")),
			MoveUp:    moveUp,
			MoveDown:  moveDown,
			Remove:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove saved track")),
			Reload:    key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reload YouTube"), key.WithDisabled()),
			Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		},
		history: historyKeyMap{
			navKeyMap: nav,
			Play:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "play now")),
			Enqueue:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "queue")),
			Save:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
			Remove:    key.NewBinding(key.WithKeys("d", "x", "delete"), key.WithHelp("d", "forget")),
		},
		devices: deviceKeyMap{
			Up:     nav.Up,
			Down:   nav.Down,
			Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "switch")),
			Close:  key.NewBinding(key.WithKeys("esc", "o", "q"), key.WithHelp("esc", "cancel")),
		},
		info: infoKeyMap{
			Open:  key.NewBinding(key.WithKeys("o", "enter"), key.WithHelp("o/enter", "open in browser")),
			Copy:  key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy url")),
			Close: key.NewBinding(key.WithKeys("esc", "i", "q"), key.WithHelp("esc", "close")),
		},
		picker: pickerKeyMap{
			navKeyMap: nav,
			Save:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save track")),
			Create:    create,
			Cancel:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		},
		name: nameKeyMap{
			Create: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "create playlist")),
			Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		},
	}
}

func (m Model) contextKeys() (string, help.KeyMap) {
	switch m.overlay {
	case overlayDevices:
		return "Output", m.keys.devices
	case overlayInfo:
		return "Track info", m.keys.info
	case overlayPicker:
		return "Save to playlist", m.keys.picker
	case overlayName:
		return m.nameMode.title(), m.nameKeys()
	case overlayNone:
	}
	switch m.focus {
	case focusSearch:
		return "Search", m.keys.search
	case focusQueue:
		return "Queue", m.keys.queue
	case focusPlaylists:
		keys := m.keys.playlists
		local := !m.accountSelected()
		keys.Reload.SetEnabled(!local)
		for _, b := range []*key.Binding{&keys.Create, &keys.Rename, &keys.MoveUp, &keys.MoveDown, &keys.Delete} {
			b.SetEnabled(local)
		}
		return "Playlists", keys
	case focusPlaylistTracks:
		keys := m.keys.playlistTracks
		local := !m.accountSelected()
		keys.Reload.SetEnabled(!local)
		for _, b := range []*key.Binding{&keys.MoveUp, &keys.MoveDown, &keys.Remove} {
			b.SetEnabled(local)
		}
		return "Playlist tracks", keys
	case focusHistory:
		return "History", m.keys.history
	case focusResults:
	}
	if m.results.filter.Focused() {
		return "Results filter", m.keys.filter
	}
	results := m.keys.results
	results.ClearFilter.SetEnabled(m.results.filter.Value() != "")
	results.More.SetEnabled(m.core.Search.CanLoadMore())
	return "Results", results
}

func (k globalKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Search, k.NextPane, k.Output, k.Info, k.Viz, k.Radio, k.Quit}
}

func (k globalKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Search, k.Paste, k.NextPane, k.PrevPane},
		{k.Output, k.Info, k.Viz, k.Radio, k.GoTo},
		{k.SeekPercent, k.Quit, k.ForceQuit},
	}
}

func (k playbackKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Pause, k.SeekBack, k.SeekForward, k.Next, k.Prev, k.VolumeUp, k.VolumeDown, k.Normalize}
}

func (k playbackKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Pause, k.SeekBack, k.SeekForward},
		{k.Next, k.Prev},
		{k.VolumeUp, k.VolumeDown, k.Normalize, k.Repeat},
	}
}

func (k searchKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Leave}
}

func (k searchKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

func (k navKeyMap) bindings() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Top, k.Bottom}
}

func (k resultKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Play, k.Enqueue, k.Save, k.More, k.Filter, k.ClearFilter}
}

func (k resultKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.bindings(), k.ShortHelp()}
}

func (k queueKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Jump, k.Save, k.SaveQueue, k.Remove, k.Clear, k.MoveDown, k.MoveUp}
}

func (k queueKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		k.bindings(),
		{k.Jump, k.Remove, k.Clear},
		{k.MoveUp, k.MoveDown, k.Shuffle},
		{k.Save, k.SaveQueue},
	}
}

func (k playlistKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Create, k.Browse, k.PlayAll, k.EnqueueAll, k.Rename, k.Delete, k.Reload}
}

func (k playlistKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		k.bindings(),
		{k.Browse, k.PlayAll, k.EnqueueAll, k.Back},
		{k.Create, k.Rename, k.MoveUp, k.MoveDown, k.Delete, k.Reload},
	}
}

func (k playlistTrackKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Play, k.PlayAll, k.Enqueue, k.Remove, k.Reload, k.Back}
}

func (k playlistTrackKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		k.bindings(),
		{k.Play, k.PlayAll, k.Enqueue, k.Back},
		{k.MoveUp, k.MoveDown, k.Remove, k.Reload},
	}
}

func (k historyKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Play, k.Enqueue, k.Save, k.Remove}
}

func (k historyKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.bindings(), k.ShortHelp()}
}

func (k deviceKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Select, k.Close}
}

func (k deviceKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down}, k.ShortHelp()}
}

func (k infoKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Open, k.Copy, k.Close}
}

func (k infoKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

func (k pickerKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Save, k.Create, k.Cancel}
}

func (k pickerKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.bindings(), k.ShortHelp()}
}

func (k nameKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Create, k.Cancel}
}

func (k nameKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

func (k filterKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Apply, k.Clear}
}

func (k filterKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

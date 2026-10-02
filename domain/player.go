package domain

type PlaylistEntry struct {
	Filename string
	Title    string
	Current  bool
	Playing  bool
}

type AudioDevice struct {
	Name        string // e.g. "auto", "pipewire/<sink>", "coreaudio/<id>"
	Description string
}

// Label is a human-readable device name.
func (d AudioDevice) Label() string {
	if d.Description != "" {
		return d.Description
	}
	return d.Name
}

type StreamInfo struct {
	Path    string
	Opened  string
	Codec   string
	Bitrate int
}

type AudioParams struct {
	Format     string
	SampleRate int
	Channels   string
}

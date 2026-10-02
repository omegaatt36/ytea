package service

import "time"

type PlayState uint8

const (
	Stopped PlayState = iota
	Playing
	Paused
)

type NowPlaying struct {
	State    PlayState
	TrackID  string
	Title    string
	Artist   string
	URL      string
	ArtURL   string
	Length   time.Duration
	Volume   float64 // 0..1
	Position time.Duration
	CanNext  bool
	CanPrev  bool
}

func (c Core) NowPlaying() NowPlaying {
	np := NowPlaying{
		Volume:   min(c.Playback.Volume/100, 1),
		Position: c.Playback.TimePos,
		CanPrev:  c.Queue.Pos > 0,
		CanNext:  c.Queue.Pos >= 0 && c.Queue.Pos < len(c.Queue.Entries)-1,
	}
	if e, t, ok := c.Current(); ok {
		np.State = Playing
		if c.Playback.Paused {
			np.State = Paused
		}
		np.TrackID = t.ID
		if np.TrackID == "" {
			np.TrackID = e.Filename
		}
		np.Title = DisplayTitle(e, t)
		np.Artist = t.Channel
		np.URL = e.Filename
		// A live stream's duration is its DVR window, not a track length.
		if !t.Live {
			np.Length = c.Playback.Duration
		}
		if t.ID != "" {
			np.ArtURL = t.ThumbnailURL()
		}
	}
	return np
}

// Package trackfile is how a domain.Track is written to disk. Keeping the
// shape here, apart from domain, means renaming a domain field cannot change
// a stored file.
package trackfile

import (
	"time"

	"github.com/omegaatt36/ytea/domain"
)

type Track struct {
	ID       string        `json:"ID"`
	Title    string        `json:"Title"`
	Channel  string        `json:"Channel"`
	URL      string        `json:"URL"`
	Duration time.Duration `json:"Duration"`
	Live     bool          `json:"Live"`
}

func From(t domain.Track) Track {
	return Track{ID: t.ID, Title: t.Title, Channel: t.Channel, URL: t.URL, Duration: t.Duration, Live: t.Live}
}

func (t Track) To() domain.Track {
	return domain.Track{ID: t.ID, Title: t.Title, Channel: t.Channel, URL: t.URL, Duration: t.Duration, Live: t.Live}
}

func FromAll(tracks []domain.Track) []Track {
	if tracks == nil {
		return nil
	}
	out := make([]Track, len(tracks))
	for i, t := range tracks {
		out[i] = From(t)
	}
	return out
}

func ToAll(tracks []Track) []domain.Track {
	if tracks == nil {
		return nil
	}
	out := make([]domain.Track, len(tracks))
	for i, t := range tracks {
		out[i] = t.To()
	}
	return out
}

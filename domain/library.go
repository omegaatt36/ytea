package domain

import "time"

// Playlist is a named, ordered collection of YouTube tracks.
type Playlist struct {
	Name   string
	Tracks []Track
}

type HistoryEntry struct {
	Track    Track
	PlayedAt time.Time
}

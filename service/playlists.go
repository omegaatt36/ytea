package service

import "github.com/omegaatt36/ytea/domain"

type PlaylistStore interface {
	Playlists() []domain.Playlist
	CreateWithTracks(name string, tracks []domain.Track) (int, error)
	Add(index int, track domain.Track) error
	Rename(index int, name string) error
	Move(from, to int) error
	MoveTrack(playlistIndex, from, to int) error
	RemoveTrack(playlistIndex, trackIndex int) error
	Delete(index int) error
}

type Playlists struct {
	store PlaylistStore
	List  []domain.Playlist
}

func (p Playlists) Enabled() bool { return p.store != nil }

func (p Playlists) Tracks(i int) []domain.Track {
	if i < 0 || i >= len(p.List) {
		return nil
	}
	return p.List[i].Tracks
}

func (p *Playlists) reload(err error) error {
	if err == nil {
		p.List = p.store.Playlists()
	}
	return err
}

func (p *Playlists) Create(name string, tracks []domain.Track) (int, error) {
	index, err := p.store.CreateWithTracks(name, tracks)
	return index, p.reload(err)
}

func (p *Playlists) Add(index int, track domain.Track) error {
	return p.reload(p.store.Add(index, track))
}

func (p *Playlists) Rename(index int, name string) error {
	return p.reload(p.store.Rename(index, name))
}

func (p *Playlists) Move(from, to int) error {
	return p.reload(p.store.Move(from, to))
}

func (p *Playlists) MoveTrack(index, from, to int) error {
	return p.reload(p.store.MoveTrack(index, from, to))
}

func (p *Playlists) RemoveTrack(index, trackIndex int) error {
	return p.reload(p.store.RemoveTrack(index, trackIndex))
}

func (p *Playlists) Delete(index int) error {
	return p.reload(p.store.Delete(index))
}

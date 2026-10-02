package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

func playing(names ...string) Core {
	c := New(Deps{})
	c.Queue.Entries, c.Queue.Pos, c.Playback.Idle = entries(names...), 0, false
	return c
}

func TestNowPlayingDescribesCurrentTrack(t *testing.T) {
	c := playing("a", "b")
	c.Tracks["a"] = domain.Track{ID: "vid", Title: "Song", Channel: "Chan", URL: "a"}
	c.Playback.Duration, c.Playback.TimePos, c.Playback.Volume = time.Minute, 5*time.Second, 50

	np := c.NowPlaying()
	if np.State != Playing || np.Title != "Song" || np.Artist != "Chan" || np.TrackID != "vid" || np.Length != time.Minute {
		t.Errorf("playing = %+v", np)
	}
	if np.Volume != 0.5 || np.Position != 5*time.Second || np.CanPrev || !np.CanNext {
		t.Errorf("transport = %+v", np)
	}
	if np.ArtURL == "" {
		t.Error("a track with an ID has no art URL")
	}

	c.Playback.Paused = true
	if got := c.NowPlaying().State; got != Paused {
		t.Errorf("paused state = %v", got)
	}
	c.Tracks["a"] = domain.Track{Title: "Live", URL: "a", Live: true}
	if got := c.NowPlaying(); got.Length != 0 || got.TrackID != "a" {
		t.Errorf("live = %+v, want no length and the URL as ID", got)
	}
	c.Playback.Idle = true
	if got := c.NowPlaying().State; got != Stopped {
		t.Errorf("idle state = %v", got)
	}
}

type fakeHistory struct {
	recorded []domain.Track
	err      error
}

func (h *fakeHistory) Entries() []domain.HistoryEntry { return nil }
func (h *fakeHistory) Remove(int) error               { return nil }
func (h *fakeHistory) Record(t domain.Track, _ time.Time) error {
	h.recorded = append(h.recorded, t)
	return h.err
}

func TestRecordPlayWaitsForAudiblePlaybackAndRecordsOnce(t *testing.T) {
	store := &fakeHistory{}
	c := New(Deps{History: store})
	c.Queue.Entries, c.Queue.Pos, c.Playback.Idle = entries("a"), 0, false
	c.Playback.Paused = true
	c.Playback.TimePos = 3 * time.Second
	if c.RecordPlay() != nil {
		t.Fatal("a paused, restored track was recorded")
	}
	c.Playback.Paused, c.Playback.TimePos = false, 0
	if c.RecordPlay() != nil {
		t.Fatal("a track not yet playing was recorded")
	}

	c.Playback.TimePos, c.Playback.Duration = time.Second, 3*time.Minute
	cmd := c.RecordPlay()
	if cmd == nil {
		t.Fatal("a playing track was not recorded")
	}
	if _, ok := cmd().(HistoryRecorded); !ok {
		t.Fatal("recording did not report success")
	}
	if c.RecordPlay() != nil {
		t.Error("one play was recorded twice")
	}
	if len(store.recorded) != 1 || store.recorded[0].Duration != 3*time.Minute {
		t.Errorf("recorded = %+v, want the track with mpv's duration", store.recorded)
	}

	store.err = errors.New("disk full")
	c.Queue.Entries = entries("b")
	failed, ok := c.RecordPlay()().(Failed)
	if !ok || !errors.Is(failed.Err, store.err) {
		t.Errorf("failed record = %+v", failed)
	}
}

func TestRecordPlayWithoutHistoryDoesNothing(t *testing.T) {
	c := playing("a")
	c.Playback.TimePos = time.Second
	if c.RecordPlay() != nil {
		t.Error("recorded without a history")
	}
}

func TestPlaylistsReloadAfterEachEdit(t *testing.T) {
	c := New(Deps{Library: &fakePlaylistStore{}})
	if len(c.Playlists.List) != 0 {
		t.Fatalf("playlists = %+v, want none", c.Playlists.List)
	}
	index, err := c.Playlists.Create("Keep", []domain.Track{{Title: "one", URL: "1"}})
	if err != nil || index != 0 || len(c.Playlists.List) != 1 {
		t.Fatalf("create = %d, %v, list %+v", index, err, c.Playlists.List)
	}
	if err := c.Playlists.Add(0, domain.Track{Title: "two", URL: "2"}); err != nil || len(c.Playlists.Tracks(0)) != 2 {
		t.Fatalf("add: %v, tracks %+v", err, c.Playlists.Tracks(0))
	}
	if err := c.Playlists.MoveTrack(0, 0, 1); err != nil || c.Playlists.Tracks(0)[0].URL != "2" {
		t.Fatalf("move track: %v, tracks %+v", err, c.Playlists.Tracks(0))
	}
	if err := c.Playlists.Rename(0, "Renamed"); err != nil || c.Playlists.List[0].Name != "Renamed" {
		t.Fatalf("rename: %v, list %+v", err, c.Playlists.List)
	}
	if err := c.Playlists.Delete(5); err == nil {
		t.Error("deleting a missing playlist succeeded")
	}
	if err := c.Playlists.Delete(0); err != nil || len(c.Playlists.List) != 0 {
		t.Fatalf("delete: %v, list %+v", err, c.Playlists.List)
	}
	if c.Playlists.Tracks(3) != nil {
		t.Error("a missing playlist has tracks")
	}
	if New(Deps{}).Playlists.Enabled() {
		t.Error("playlists enabled without a store")
	}
}

type fakePlaylistStore struct{ lists []domain.Playlist }

var errNoPlaylist = errors.New("playlist or track not found")

func (s *fakePlaylistStore) Playlists() []domain.Playlist {
	return slices.Clone(s.lists)
}

func (s *fakePlaylistStore) CreateWithTracks(name string, tracks []domain.Track) (int, error) {
	s.lists = append(s.lists, domain.Playlist{Name: name, Tracks: slices.Clone(tracks)})
	return len(s.lists) - 1, nil
}

func (s *fakePlaylistStore) Add(index int, track domain.Track) error {
	if index < 0 || index >= len(s.lists) {
		return errNoPlaylist
	}
	s.lists[index].Tracks = append(slices.Clone(s.lists[index].Tracks), track)
	return nil
}

func (s *fakePlaylistStore) Rename(index int, name string) error {
	if index < 0 || index >= len(s.lists) {
		return errNoPlaylist
	}
	s.lists[index].Name = name
	return nil
}

func (s *fakePlaylistStore) Move(from, to int) error {
	if from < 0 || from >= len(s.lists) || to < 0 || to >= len(s.lists) {
		return errNoPlaylist
	}
	list := s.lists[from]
	s.lists = slices.Insert(slices.Delete(s.lists, from, from+1), to, list)
	return nil
}

func (s *fakePlaylistStore) MoveTrack(playlistIndex, from, to int) error {
	if playlistIndex < 0 || playlistIndex >= len(s.lists) {
		return errNoPlaylist
	}
	tracks := slices.Clone(s.lists[playlistIndex].Tracks)
	if from < 0 || from >= len(tracks) || to < 0 || to >= len(tracks) {
		return errNoPlaylist
	}
	track := tracks[from]
	s.lists[playlistIndex].Tracks = slices.Insert(slices.Delete(tracks, from, from+1), to, track)
	return nil
}

func (s *fakePlaylistStore) RemoveTrack(playlistIndex, trackIndex int) error {
	if playlistIndex < 0 || playlistIndex >= len(s.lists) {
		return errNoPlaylist
	}
	tracks := slices.Clone(s.lists[playlistIndex].Tracks)
	if trackIndex < 0 || trackIndex >= len(tracks) {
		return errNoPlaylist
	}
	s.lists[playlistIndex].Tracks = slices.Delete(tracks, trackIndex, trackIndex+1)
	return nil
}

func (s *fakePlaylistStore) Delete(index int) error {
	if index < 0 || index >= len(s.lists) {
		return errNoPlaylist
	}
	s.lists = slices.Delete(s.lists, index, index+1)
	return nil
}

type fakeAccount struct {
	playlists []domain.AccountPlaylist
	tracks    []domain.Track
	err       error
}

func (a fakeAccount) ListPlaylists(context.Context) ([]domain.AccountPlaylist, error) {
	return a.playlists, a.err
}

func (a fakeAccount) ListTracks(context.Context, string) ([]domain.Track, error) {
	return a.tracks, a.err
}

func TestAccountDropsResultsOfEarlierReload(t *testing.T) {
	source := fakeAccount{playlists: []domain.AccountPlaylist{{ID: "p", Title: "Mine"}}}
	c := New(Deps{Account: source})
	first := c.Account.Reload()
	if first == nil || !c.Account.Loading {
		t.Fatal("reload did not start")
	}
	if c.Account.Reload() != nil {
		t.Error("a second reload started while loading")
	}
	stale := first()
	c.Account.Loading = false
	second := c.Account.Reload()
	c.Account.Update(stale)
	if !c.Account.Loading || len(c.Account.Playlists) != 0 {
		t.Fatal("a result of the earlier reload was applied")
	}
	c.Account.Update(second())
	if c.Account.Loading || c.Account.Err != nil || len(c.Account.Playlists) != 1 {
		t.Fatalf("account = %+v", c.Account)
	}

	tracksCmd := c.Account.LoadTracks("p")
	if tracksCmd == nil || !c.Account.TrackLoading["p"] {
		t.Fatal("tracks did not start loading")
	}
	if c.Account.LoadTracks("p") != nil {
		t.Error("tracks loaded twice at once")
	}
	c.Account.Update(tracksCmd())
	if c.Account.TrackLoading["p"] || c.Account.LoadTracks("p") != nil {
		t.Error("loaded tracks are fetched again")
	}
}

func TestAccountEmptyListIsAnError(t *testing.T) {
	c := New(Deps{Account: fakeAccount{}})
	c.Account.Update(c.Account.Reload()())
	if c.Account.Err == nil {
		t.Error("an account without playlists reported no error")
	}
}

func TestQueueAccountPlaylistFailureKeepsWriteOrder(t *testing.T) {
	want := errors.New("lookup failed")
	player := appendRecorder{writes: make(chan string, 1)}
	c := New(Deps{Player: player, Account: fakeAccount{err: want}})
	c.Account.Update(AccountPlaylistsDone{generation: c.Account.generation, playlists: []domain.AccountPlaylist{{ID: "p"}}})
	cmd := c.QueueAccountPlaylist("p")
	failed, ok := cmd().(AccountQueueFailed)
	if !ok || !errors.Is(failed.err, want) {
		t.Fatalf("result = %+v, want a queue failure", failed)
	}
	c.Account.Update(failed)
	if !errors.Is(c.Account.QueueErrors["p"], want) {
		t.Error("the failure was not recorded for the playlist")
	}
	// The failed write must still release the slot behind it.
	released := make(chan struct{})
	go func() { _ = c.Queue.Reserve().Run(func() error { return nil }); close(released) }()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("a failed account queue left the write order blocked")
	}
}

func TestShuffleKeepsCurrentAndPrefix(t *testing.T) {
	c := playing("A", "B", "C", "D", "E")
	c.Queue.Pos = 1
	cmd, order, err := c.Shuffle(rand.New(rand.NewPCG(1, 2)))
	if err != nil || cmd == nil {
		t.Fatalf("shuffle = %v, %v", cmd != nil, err)
	}
	if order[0] != 0 || order[1] != 1 {
		t.Errorf("order = %v, want the prefix and current track in place", order)
	}
	if tail := slices.Sorted(slices.Values(order[2:])); !slices.Equal(tail, []int{2, 3, 4}) {
		t.Errorf("order tail = %v, want a permutation of 2, 3, 4", order[2:])
	}
	if !c.Queue.Projected() {
		t.Error("shuffle was not projected through the queue-sync write path")
	}
	if _, _, err := c.Shuffle(rand.New(rand.NewPCG(1, 2))); !errors.Is(err, ErrEditsPending) {
		t.Errorf("shuffle with edits pending = %v, want ErrEditsPending", err)
	}
}

func TestShuffleNeedsTwoTracks(t *testing.T) {
	c := playing("A", "B", "C")
	c.Queue.Pos = 1
	if _, _, err := c.Shuffle(rand.New(rand.NewPCG(1, 2))); !errors.Is(err, ErrNothingToShuffle) {
		t.Errorf("one track after the current = %v, want ErrNothingToShuffle", err)
	}
}

func TestMoveEntryWalksNeighbours(t *testing.T) {
	c := playing("A", "B", "C", "D")
	cmds := c.MoveEntry(0, 3)
	if len(cmds) != 3 {
		t.Fatalf("commands = %d, want one swap per slot", len(cmds))
	}
	assertQueue(t, c.Queue, "B", "C", "D", "A")
	if c.MoveEntry(2, 2) != nil || c.MoveEntry(-1, 1) != nil || c.MoveEntry(0, 9) != nil {
		t.Error("an invalid move issued commands")
	}
	c.Queue.InsertPending = true
	if c.MoveEntry(0, 1) != nil {
		t.Error("an index edit ran while an insert was unconfirmed")
	}
}

func TestIndexEditsWaitForInsert(t *testing.T) {
	c := playing("A", "B")
	if c.PlayIndex(5) != nil || c.RemoveEntry(-1) != nil {
		t.Error("an out-of-range edit issued a command")
	}
	c.Queue.InsertPending = true
	if c.PlayIndex(0) != nil || c.RemoveEntry(0) != nil {
		t.Error("an index edit ran while an insert was unconfirmed")
	}
	c.Queue.InsertPending = false
	if c.RemoveEntry(0) == nil {
		t.Fatal("remove did not run")
	}
	assertQueue(t, c.Queue, "B")
}

func TestClearQueueStopsPlayback(t *testing.T) {
	c := playing("A", "B")
	c.Playback.TimePos, c.Playback.Duration = 10*time.Second, time.Minute
	if c.ClearQueue() == nil {
		t.Fatal("clear issued no command")
	}
	if !c.Playback.Idle || c.Playback.TimePos != 0 || c.Playback.Duration != 0 {
		t.Errorf("playback = %+v, want stopped", c.Playback)
	}
	assertQueue(t, c.Queue)
}

func TestQueueTracksDropsBlankAndDuplicateEntries(t *testing.T) {
	c := New(Deps{})
	c.Queue.Entries = []domain.PlaylistEntry{{Filename: "a", Title: "mpv title"}, {Filename: ""}, {Filename: "a"}, {Filename: "b"}}
	c.Tracks["b"] = domain.Track{Title: "Known", URL: "b"}
	got := c.QueueTracks()
	if len(got) != 2 || got[0].URL != "a" || got[0].Title != "mpv title" || got[1].Title != "Known" {
		t.Errorf("tracks = %+v", got)
	}
}

type failingPlayer struct {
	Player
	err error
}

func (p failingPlayer) TogglePause(context.Context) error { return p.err }

func TestCommandFailureComesBackAsFailed(t *testing.T) {
	want := errors.New("ipc closed")
	c := New(Deps{Player: failingPlayer{err: want}})
	failed, ok := c.TogglePause()().(Failed)
	if !ok || !errors.Is(failed.Err, want) {
		t.Fatalf("result = %+v, want Failed", failed)
	}
	c = New(Deps{Player: failingPlayer{}})
	if msg := c.TogglePause()(); msg != nil {
		t.Errorf("successful command reported %+v", msg)
	}
}

func TestSeekPercentNeedsSeekableTrack(t *testing.T) {
	c := playing("a")
	if c.SeekPercent(50) != nil {
		t.Error("seeked a track of unknown length")
	}
	c.Playback.Duration = time.Minute
	if c.SeekPercent(50) == nil {
		t.Error("a seekable track was not seeked")
	}
	c.Tracks["a"] = domain.Track{URL: "a", Live: true}
	if c.SeekPercent(50) != nil {
		t.Error("seeked a live stream")
	}
}

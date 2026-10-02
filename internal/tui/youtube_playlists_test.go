package tui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/service"
)

type spyPlaylistPlayer struct {
	service.Player
	calls          []string
	appendAllCalls [][]string
	err            error
}

func (p *spyPlaylistPlayer) PlayNow(_ context.Context, url string) error {
	p.calls = append(p.calls, "play "+url)
	return p.err
}

func (p *spyPlaylistPlayer) Append(_ context.Context, url string) error {
	p.calls = append(p.calls, "append "+url)
	return p.err
}

func (p *spyPlaylistPlayer) AppendAll(_ context.Context, urls []string) error {
	p.calls = append(p.calls, "append-all")
	p.appendAllCalls = append(p.appendAllCalls, append([]string(nil), urls...))
	return p.err
}

// spyAccountPlaylistSource returns fixed account data and records lazy fetches.
type spyAccountPlaylistSource struct {
	playlists []domain.AccountPlaylist
	tracks    map[string][]domain.Track
	listErr   error
	trackErr  error
	trackErrs map[string]error
	calls     []string
}

func (s *spyAccountPlaylistSource) ListPlaylists(context.Context) ([]domain.AccountPlaylist, error) {
	s.calls = append(s.calls, "playlists")
	return s.playlists, s.listErr
}

func (s *spyAccountPlaylistSource) ListTracks(_ context.Context, id string) ([]domain.Track, error) {
	s.calls = append(s.calls, "tracks "+id)
	if err := s.trackErrs[id]; err != nil {
		return nil, err
	}
	return s.tracks[id], s.trackErr
}

func accountPlaylistFixture() *spyAccountPlaylistSource {
	return &spyAccountPlaylistSource{
		playlists: []domain.AccountPlaylist{{ID: "mine", Title: "My uploads"}},
		tracks: map[string][]domain.Track{"mine": {
			{ID: "one", Title: "First account song", URL: "https://www.youtube.com/watch?v=one"},
			{ID: "two", Title: "Second account song", URL: "https://www.youtube.com/watch?v=two"},
		}},
	}
}

func accountPlaylistView(m Model) string { return ansi.Strip(m.render()) }

func accountPlaylistKey(t *testing.T, m Model, code rune) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return next.(Model), cmd
}

func accountPlaylistFocus(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for range 4 {
		m, cmd = accountPlaylistKey(t, m, tea.KeyTab)
		if m.focus == focusPlaylists {
			return m, cmd
		}
	}
	t.Fatalf("tab navigation focused %v, want Playlists", m.focus)
	return m, nil
}

func accountPlaylistComplete(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("background account request command = nil")
	}
	result := cmd()
	next, _ := m.Update(result)
	return next.(Model)
}

func TestSavePickerSelectsLocalPlaylistAfterAccountBrowsing(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWithTracks("Local", nil); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store, AccountPlaylists: accountPlaylistFixture()})
	m.playlistCur = len(m.core.Playlists.List)
	next, _ := m.openPlaylistPicker(domain.Track{Title: "Song", URL: "song"})
	m = next.(Model)
	if m.overlay != overlayPicker || m.playlistCur != 0 {
		t.Fatalf("picker opened with overlay %v and cursor %d, want local playlist 0", m.overlay, m.playlistCur)
	}
}

func TestAccountPlaylistSectionHiddenWithoutSource(t *testing.T) {
	store, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWithTracks("Local favorites", []domain.Track{{Title: "Saved song", URL: "saved"}}); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Library: store})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	if cmd != nil {
		t.Fatalf("no account source scheduled a request: %T", cmd())
	}
	view := accountPlaylistView(m)
	if strings.Contains(view, "YouTube") || !strings.Contains(view, "Local favorites") {
		t.Fatalf("Playlists view without account source = %q", view)
	}
	m, _ = accountPlaylistKey(t, m, tea.KeyEnter)
	if m.focus != focusPlaylistTracks || !strings.Contains(accountPlaylistView(m), "Saved song") {
		t.Fatalf("local playlist stopped working without account source: focus=%v view=%q", m.focus, accountPlaylistView(m))
	}
}

func TestAccountPlaylistsLoadLazilyOnceAndReloadInSection(t *testing.T) {
	source := accountPlaylistFixture()
	m := New(Deps{AccountPlaylists: source})
	m.width, m.height = 100, 30
	if len(source.calls) != 0 {
		t.Fatalf("account source called before Playlists focus: %v", source.calls)
	}
	m, cmd := accountPlaylistFocus(t, m)
	if len(source.calls) != 0 || !strings.Contains(accountPlaylistView(m), "YouTube") || !strings.Contains(strings.ToLower(accountPlaylistView(m)), "loading") {
		t.Fatalf("first focus did not show background loading: calls=%v view=%q", source.calls, accountPlaylistView(m))
	}
	m = accountPlaylistComplete(t, m, cmd)
	if !reflect.DeepEqual(source.calls, []string{"playlists"}) || !strings.Contains(accountPlaylistView(m), "My uploads") {
		t.Fatalf("loaded account playlists: calls=%v view=%q", source.calls, accountPlaylistView(m))
	}
	m, _ = accountPlaylistKey(t, m, tea.KeyTab)
	m, cmd = accountPlaylistFocus(t, m)
	if cmd != nil || len(source.calls) != 1 {
		t.Fatalf("returning to Playlists fetched again: calls=%v", source.calls)
	}
	next, reload := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = next.(Model)
	if !strings.Contains(strings.ToLower(accountPlaylistView(m)), "loading") {
		t.Fatalf("ctrl+r did not show loading: %q", accountPlaylistView(m))
	}
	m = accountPlaylistComplete(t, m, reload)
	if !reflect.DeepEqual(source.calls, []string{"playlists", "playlists"}) || !strings.Contains(accountPlaylistView(m), "My uploads") {
		t.Fatalf("ctrl+r reload: calls=%v view=%q", source.calls, accountPlaylistView(m))
	}
}

func TestAccountPlaylistErrorsStayInSection(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "cookie failure", err: errors.New("yt-dlp: cookies expired"), want: "yt-dlp: cookies expired"},
		{name: "rejected refresh", err: errors.New("run `ytea auth login` again"), want: "run `ytea auth login` again"},
		{name: "no account data", want: "no account data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &spyAccountPlaylistSource{listErr: tc.err}
			m := New(Deps{AccountPlaylists: source})
			m.width, m.height = 100, 30
			m, cmd := accountPlaylistFocus(t, m)
			m = accountPlaylistComplete(t, m, cmd)
			view := accountPlaylistView(m)
			if !strings.Contains(view, "YouTube") || !strings.Contains(view, tc.want) {
				t.Fatalf("account error missing from YouTube section: %q", view)
			}
			m, _ = accountPlaylistKey(t, m, tea.KeyTab)
			if m.focus == focusPlaylists {
				t.Fatal("account error blocked other tabs")
			}
		})
	}
}

func TestAccountPlaylistTracksFetchOnFirstOpenAndQueueReadOnly(t *testing.T) {
	source := accountPlaylistFixture()
	player := &spyPlaylistPlayer{}
	m := New(Deps{AccountPlaylists: source, Player: player})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	m = accountPlaylistComplete(t, m, cmd)
	if !reflect.DeepEqual(source.calls, []string{"playlists"}) {
		t.Fatalf("tracks fetched before opening playlist: %v", source.calls)
	}
	m, cmd = accountPlaylistKey(t, m, tea.KeyEnter)
	if len(source.calls) != 1 || !strings.Contains(strings.ToLower(accountPlaylistView(m)), "loading") {
		t.Fatalf("first open did not start background track load: calls=%v view=%q", source.calls, accountPlaylistView(m))
	}
	m = accountPlaylistComplete(t, m, cmd)
	if !reflect.DeepEqual(source.calls, []string{"playlists", "tracks mine"}) || !strings.Contains(accountPlaylistView(m), "First account song") {
		t.Fatalf("track load: calls=%v view=%q", source.calls, accountPlaylistView(m))
	}
	for _, key := range []rune{'d', 'D'} {
		var action tea.Cmd
		m, action = accountPlaylistKey(t, m, key)
		if action != nil || !strings.Contains(accountPlaylistView(m), "First account song") {
			t.Fatalf("%q changed read-only account tracks: view=%q", key, accountPlaylistView(m))
		}
	}
	m, cmd = accountPlaylistKey(t, m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("enter on account track did not schedule play")
	}
	_ = cmd()
	if !reflect.DeepEqual(player.calls, []string{"play https://www.youtube.com/watch?v=one"}) {
		t.Fatalf("enter played %v", player.calls)
	}
	m, cmd = accountPlaylistKey(t, m, 'a')
	if cmd == nil {
		t.Fatal("a on account track did not schedule append")
	}
	_ = cmd()
	if !reflect.DeepEqual(player.calls, []string{"play https://www.youtube.com/watch?v=one", "append https://www.youtube.com/watch?v=one"}) {
		t.Fatalf("a queued %v", player.calls)
	}
	m, _ = accountPlaylistKey(t, m, tea.KeyEscape)
	if !reflect.DeepEqual(source.calls, []string{"playlists", "tracks mine"}) {
		t.Fatalf("returning from account tracks fetched again: %v", source.calls)
	}
	for _, key := range []rune{'d', 'D'} {
		var action tea.Cmd
		m, action = accountPlaylistKey(t, m, key)
		if action != nil || !strings.Contains(accountPlaylistView(m), "My uploads") {
			t.Fatalf("%q changed read-only account playlist: view=%q", key, accountPlaylistView(m))
		}
	}
	m, cmd = accountPlaylistKey(t, m, 'a')
	if cmd == nil {
		t.Fatal("a on account playlist did not schedule whole-playlist append")
	}
	_ = cmd()
	if !reflect.DeepEqual(player.calls[2:], []string{"append-all"}) ||
		!reflect.DeepEqual(player.appendAllCalls, [][]string{{"https://www.youtube.com/watch?v=one", "https://www.youtube.com/watch?v=two"}}) {
		t.Fatalf("whole-playlist append calls = %v, batches = %v", player.calls[2:], player.appendAllCalls)
	}
}

func TestAccountPlaylistActionsLeaveLocalPlaylistFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	store, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWithTracks("Local favorites", []domain.Track{{Title: "Saved song", URL: "https://www.youtube.com/watch?v=saved"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "playlists.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	source := accountPlaylistFixture()
	player := &spyPlaylistPlayer{}
	m := New(Deps{Library: store, AccountPlaylists: source, Player: player})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	m = accountPlaylistComplete(t, m, cmd)
	if !strings.Contains(accountPlaylistView(m), "My uploads") {
		t.Fatalf("account playlist not visible: %q", accountPlaylistView(m))
	}
	m, _ = accountPlaylistKey(t, m, tea.KeyDown)
	m, cmd = accountPlaylistKey(t, m, tea.KeyEnter)
	m = accountPlaylistComplete(t, m, cmd)
	for _, key := range []rune{'d', 'D', tea.KeyEnter, 'a'} {
		m, cmd = accountPlaylistKey(t, m, key)
		if cmd != nil {
			_ = cmd()
		}
	}
	m, _ = accountPlaylistKey(t, m, tea.KeyEscape)
	for _, key := range []rune{'d', 'D', 'a'} {
		m, cmd = accountPlaylistKey(t, m, key)
		if cmd != nil {
			_ = cmd()
		}
	}
	if len(player.calls) == 0 {
		t.Fatal("account play and queue actions did not reach player")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("YouTube account actions changed playlists.json:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestAccountPlaylistMouseWheelOpensTracksLazily(t *testing.T) {
	source := accountPlaylistFixture()
	m := New(Deps{AccountPlaylists: source})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	m = accountPlaylistComplete(t, m, cmd)
	row := cellAt(t, m, "My uploads")
	at := image.Pt(m.width-10, row.Y)
	next, load := m.Update(tea.MouseWheelMsg{X: at.X, Y: at.Y, Button: tea.MouseWheelDown})
	m = next.(Model)
	if m.focus != focusPlaylistTracks {
		t.Fatalf("wheel did not enter account tracks: focus=%v view=%q", m.focus, accountPlaylistView(m))
	}
	if load == nil || !strings.Contains(strings.ToLower(accountPlaylistView(m)), "loading") {
		t.Fatalf("wheel into unopened account tracks: focus=%v command=%v view=%q", m.focus, load != nil, accountPlaylistView(m))
	}
	if !reflect.DeepEqual(source.calls, []string{"playlists"}) {
		t.Fatalf("track fetch ran synchronously: %v", source.calls)
	}
	m = accountPlaylistComplete(t, m, load)
	if !reflect.DeepEqual(source.calls, []string{"playlists", "tracks mine"}) || !strings.Contains(accountPlaylistView(m), "First account song") {
		t.Fatalf("wheel did not load account tracks: calls=%v view=%q", source.calls, accountPlaylistView(m))
	}
}

func TestStaleAccountQueueFailureDoesNotReplaceNewPlaylistLoading(t *testing.T) {
	source := &spyAccountPlaylistSource{
		playlists: []domain.AccountPlaylist{{ID: "A", Title: "Playlist A"}, {ID: "B", Title: "Playlist B"}},
		tracks:    map[string][]domain.Track{"B": {{ID: "b", Title: "B song", URL: "https://www.youtube.com/watch?v=b"}}},
		trackErrs: map[string]error{"A": errors.New("A fetch failed")},
	}
	player := &spyPlaylistPlayer{}
	m := New(Deps{AccountPlaylists: source, Player: player})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	m = accountPlaylistComplete(t, m, cmd)
	m, staleQueue := accountPlaylistKey(t, m, 'a')
	if staleQueue == nil {
		t.Fatal("queueing uncached A did not schedule a fetch")
	}
	next, reload := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = accountPlaylistComplete(t, next.(Model), reload)
	m, _ = accountPlaylistKey(t, m, tea.KeyDown)
	m, loadB := accountPlaylistKey(t, m, tea.KeyEnter)
	if loadB == nil || !strings.Contains(strings.ToLower(accountPlaylistView(m)), "loading") {
		t.Fatalf("B did not begin loading: command=%v view=%q", loadB != nil, accountPlaylistView(m))
	}
	staleResult := staleQueue()
	next, _ = m.Update(staleResult)
	m = next.(Model)
	view := accountPlaylistView(m)
	if !strings.Contains(view, "Playlist B") || !strings.Contains(strings.ToLower(view), "loading") || strings.Contains(view, "A fetch failed") {
		t.Fatalf("late A queue failure replaced B loading: %q", view)
	}
	m = accountPlaylistComplete(t, m, loadB)
	if !strings.Contains(accountPlaylistView(m), "B song") || strings.Contains(accountPlaylistView(m), "A fetch failed") {
		t.Fatalf("B load after stale A failure: %q", accountPlaylistView(m))
	}
}

func TestAccountPlaylistBrowseRecoversAfterQueueFetchFailure(t *testing.T) {
	source := accountPlaylistFixture()
	source.trackErrs = map[string]error{"mine": errors.New("queue fetch failed")}
	player := &spyPlaylistPlayer{}
	m := New(Deps{AccountPlaylists: source, Player: player})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	m = accountPlaylistComplete(t, m, cmd)
	m, queue := accountPlaylistKey(t, m, 'a')
	if queue == nil {
		t.Fatal("queueing uncached account playlist did not schedule a fetch")
	}
	m = accountPlaylistComplete(t, m, queue)
	if !strings.Contains(accountPlaylistView(m), "queue fetch failed") {
		t.Fatalf("queue fetch error was not shown: %q", accountPlaylistView(m))
	}
	delete(source.trackErrs, "mine")
	m, browse := accountPlaylistKey(t, m, tea.KeyEnter)
	if browse == nil || !strings.Contains(strings.ToLower(accountPlaylistView(m)), "loading") {
		t.Fatalf("browse after queue failure did not retry: command=%v view=%q", browse != nil, accountPlaylistView(m))
	}
	m = accountPlaylistComplete(t, m, browse)
	view := accountPlaylistView(m)
	if !strings.Contains(view, "First account song") || strings.Contains(view, "queue fetch failed") {
		t.Fatalf("successful browse still covered by queue error: %q", view)
	}
	m, play := accountPlaylistKey(t, m, tea.KeyEnter)
	if play == nil {
		t.Fatal("recovered account track could not be played")
	}
	_ = play()
	if !reflect.DeepEqual(player.calls, []string{"play https://www.youtube.com/watch?v=one"}) {
		t.Fatalf("recovered track play calls = %v", player.calls)
	}
}

func TestLateSamePlaylistQueueFailureCannotHideBrowsedTracks(t *testing.T) {
	source := accountPlaylistFixture()
	player := &spyPlaylistPlayer{}
	m := New(Deps{AccountPlaylists: source, Player: player})
	m.width, m.height = 100, 30
	m, cmd := accountPlaylistFocus(t, m)
	m = accountPlaylistComplete(t, m, cmd)
	m, lateQueue := accountPlaylistKey(t, m, 'a')
	if lateQueue == nil {
		t.Fatal("queueing uncached account playlist did not schedule a fetch")
	}
	m, browse := accountPlaylistKey(t, m, tea.KeyEnter)
	if browse == nil {
		t.Fatal("opening account playlist did not schedule first browse fetch")
	}
	m = accountPlaylistComplete(t, m, browse)
	if !strings.Contains(accountPlaylistView(m), "First account song") {
		t.Fatalf("browse did not show loaded tracks: %q", accountPlaylistView(m))
	}
	source.trackErrs = map[string]error{"mine": errors.New("late queue fetch failed")}
	m = accountPlaylistComplete(t, m, lateQueue)
	view := accountPlaylistView(m)
	if !strings.Contains(view, "First account song") || strings.Contains(view, "late queue fetch failed") {
		t.Fatalf("late queue failure hid successfully browsed tracks: %q", view)
	}
	m, play := accountPlaylistKey(t, m, tea.KeyEnter)
	if play == nil {
		t.Fatal("late queue failure made loaded track unplayable")
	}
	_ = play()
	if !reflect.DeepEqual(player.calls, []string{"play https://www.youtube.com/watch?v=one"}) {
		t.Fatalf("play after late queue failure = %v", player.calls)
	}
}

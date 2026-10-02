// Package engine builds the service layer's collaborators, so front ends stay
// unaware of which implementations back it.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/internal/history"
	"github.com/omegaatt36/ytea/internal/library"
	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/session"
	"github.com/omegaatt36/ytea/internal/youtube"
	"github.com/omegaatt36/ytea/service"
)

type Config struct {
	MPVBin, YtDlpBin string
	// StateDir must already exist.
	StateDir string
	// ClientName names mpv's audio stream, which spectrum taps find it by.
	ClientName string
	// Empty means the system default.
	AudioDevice string
	Volume      int
	Normalize   bool
	// Cookies or CookiesFromBrowser sign playback in for Premium formats; search stays anonymous.
	Cookies, CookiesFromBrowser string
	Account                     service.AccountSource
}

type Engine struct {
	cfg    Config
	player *mpv.Player
	deps   service.Deps
	// restoreFailed keeps SaveSession from overwriting a session that could not be restored.
	restoreFailed bool
}

func New(ctx context.Context, cfg Config) (*Engine, error) {
	player, err := mpv.Start(ctx, mpv.Config{
		Bin:                cfg.MPVBin,
		ClientName:         cfg.ClientName,
		AudioDevice:        cfg.AudioDevice,
		Volume:             cfg.Volume,
		Normalize:          cfg.Normalize,
		LogFile:            filepath.Join(cfg.StateDir, "mpv.log"),
		Cookies:            cfg.Cookies,
		CookiesFromBrowser: cfg.CookiesFromBrowser,
	})
	if err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg, player: player}
	saved, err := session.Load(cfg.StateDir)
	if err != nil {
		slog.Warn("load previous session", "error", err)
	} else if saved.Version != 0 {
		e.restore(ctx, saved)
	}

	libraryStore, err := library.Open(cfg.StateDir)
	if err != nil {
		_ = player.Quit()
		return nil, fmt.Errorf("load local playlists: %w", err)
	}
	e.deps = service.Deps{
		Searcher:      youtube.NewSearcher(cfg.YtDlpBin),
		Player:        player,
		Library:       libraryStore,
		Account:       cfg.Account,
		InitialTracks: tracksFromSession(saved),
		Normalize:     cfg.Normalize,
	}
	if historyStore, err := history.Open(cfg.StateDir); err != nil {
		slog.Warn("history disabled", "error", err)
	} else {
		e.deps.History = historyStore
	}
	return e, nil
}

func (e *Engine) restore(ctx context.Context, saved session.State) {
	restoreCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := e.player.Restore(restoreCtx, playbackFromSession(saved))
	if err == nil {
		return
	}
	slog.Warn("restore previous session", "error", err)
	e.restoreFailed = true
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := e.player.Stop(stopCtx); err != nil {
		slog.Warn("stop partially restored session", "error", err)
	}
}

func (e *Engine) Deps() service.Deps { return e.deps }

// PlayerPID is mpv's process id, which the macOS spectrum tap captures audio by.
func (e *Engine) PlayerPID() int { return e.player.PID() }

// Does nothing when the previous session could not be restored, so that session
// survives for the next try.
func (e *Engine) SaveSession(ctx context.Context, known func(url string) domain.Track) error {
	if e.restoreFailed {
		return nil
	}
	snapshot, err := e.player.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot playback: %w", err)
	}
	return session.Save(e.cfg.StateDir, sessionFromSnapshot(snapshot, known))
}

func (e *Engine) Close() error { return e.player.Quit() }

func tracksFromSession(saved session.State) map[string]domain.Track {
	tracks := make(map[string]domain.Track, len(saved.Metadata))
	for url, info := range saved.Metadata {
		tracks[url] = domain.Track{URL: url, ID: info.ID, Title: info.Title, Channel: info.Channel, Duration: info.Duration, Live: info.Live}
	}
	return tracks
}

func playbackFromSession(saved session.State) mpv.PlaybackState {
	return mpv.PlaybackState{URLs: saved.URLs, Index: saved.Index, Volume: saved.Volume, Repeat: domain.ParseRepeat(saved.Repeat)}
}

func sessionFromSnapshot(snapshot mpv.PlaybackState, known func(url string) domain.Track) session.State {
	state := session.State{URLs: snapshot.URLs, Index: snapshot.Index, Volume: snapshot.Volume}
	if snapshot.Repeat != domain.RepeatOff {
		state.Repeat = snapshot.Repeat.String()
	}
	for i, url := range snapshot.URLs {
		track := known(url)
		info := session.Metadata{ID: track.ID, Title: track.Title, Channel: track.Channel, Duration: track.Duration, Live: track.Live}
		if info.Title == "" && i < len(snapshot.Entries) {
			info.Title = snapshot.Entries[i].Title
		}
		if info != (session.Metadata{}) {
			if state.Metadata == nil {
				state.Metadata = make(map[string]session.Metadata)
			}
			state.Metadata[url] = info
		}
	}
	return state
}

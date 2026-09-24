// Package mpris exposes playback on the session bus as an MPRIS2 media player,
// so media keys, desktop widgets and playerctl can drive ytea.
package mpris

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	path        = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	ifaceRoot   = "org.mpris.MediaPlayer2"
	ifacePlayer = "org.mpris.MediaPlayer2.Player"
	noTrack     = dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")
	callTimeout = 2 * time.Second
)

// Controller is the playback surface MPRIS clients may drive.
// It is satisfied by mpv.Player.
type Controller interface {
	TogglePause(ctx context.Context) error
	SetPause(ctx context.Context, pause bool) error
	Next(ctx context.Context) error
	Prev(ctx context.Context) error
	Stop(ctx context.Context) error
	Seek(ctx context.Context, offset time.Duration) error
	SeekTo(ctx context.Context, pos time.Duration) error
	SetVolume(ctx context.Context, volume float64) error
}

// Status values for PlaybackStatus.
const (
	Playing = "Playing"
	Paused  = "Paused"
	Stopped = "Stopped"
)

// State is the player snapshot mirrored onto the bus.
type State struct {
	Status   string
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

// Server is a registered MPRIS player.
type Server struct {
	conn  *dbus.Conn
	props *prop.Properties
	ctrl  Controller

	mu   sync.Mutex
	last State
}

// Start claims org.mpris.MediaPlayer2.<name> on the session bus.
func Start(name string, ctrl Controller) (*Server, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect session bus: %w", err)
	}

	s := &Server{conn: conn, ctrl: ctrl, last: State{Status: Stopped, Volume: 1}}
	if err := s.export(name); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) export(name string) error {
	if err := s.conn.Export(root{}, path, ifaceRoot); err != nil {
		return fmt.Errorf("export %s: %w", ifaceRoot, err)
	}
	if err := s.conn.ExportWithMap(player{s}, playerMethodNames, path, ifacePlayer); err != nil {
		return fmt.Errorf("export %s: %w", ifacePlayer, err)
	}

	props, err := prop.Export(s.conn, path, prop.Map{
		ifaceRoot: {
			"CanQuit":             {Value: false, Emit: prop.EmitConst},
			"CanRaise":            {Value: false, Emit: prop.EmitConst},
			"HasTrackList":        {Value: false, Emit: prop.EmitConst},
			"Identity":            {Value: name, Emit: prop.EmitConst},
			"SupportedUriSchemes": {Value: []string{}, Emit: prop.EmitConst},
			"SupportedMimeTypes":  {Value: []string{}, Emit: prop.EmitConst},
		},
		ifacePlayer: {
			"PlaybackStatus": {Value: Stopped, Emit: prop.EmitTrue},
			"LoopStatus":     {Value: "None", Emit: prop.EmitConst},
			"Rate":           {Value: 1.0, Emit: prop.EmitConst},
			"Shuffle":        {Value: false, Emit: prop.EmitConst},
			"Metadata":       {Value: map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(noTrack)}, Emit: prop.EmitTrue},
			"Volume":         {Value: 1.0, Writable: true, Emit: prop.EmitTrue, Callback: s.onVolume},
			// Position changes continuously; the spec forbids signalling it, clients poll or listen for Seeked.
			"Position":      {Value: int64(0), Emit: prop.EmitFalse},
			"MinimumRate":   {Value: 1.0, Emit: prop.EmitConst},
			"MaximumRate":   {Value: 1.0, Emit: prop.EmitConst},
			"CanGoNext":     {Value: false, Emit: prop.EmitTrue},
			"CanGoPrevious": {Value: false, Emit: prop.EmitTrue},
			"CanPlay":       {Value: true, Emit: prop.EmitConst},
			"CanPause":      {Value: true, Emit: prop.EmitConst},
			"CanSeek":       {Value: true, Emit: prop.EmitConst},
			"CanControl":    {Value: true, Emit: prop.EmitConst},
		},
	})
	if err != nil {
		return fmt.Errorf("export mpris properties: %w", err)
	}
	s.props = props

	node := &introspect.Node{
		Name: string(path),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{Name: ifaceRoot, Methods: introspect.Methods(root{}), Properties: props.Introspection(ifaceRoot)},
			{Name: ifacePlayer, Methods: playerMethods(player{s}), Properties: props.Introspection(ifacePlayer)},
		},
	}
	if err := s.conn.Export(introspect.NewIntrospectable(node), path, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("export introspection: %w", err)
	}

	busName := "org.mpris.MediaPlayer2." + name
	reply, err := s.conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("request bus name %s: %w", busName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("request bus name %s: already taken", busName)
	}
	return nil
}

// Update mirrors st onto the bus, signalling only properties that changed.
func (s *Server) Update(st State) {
	s.mu.Lock()
	prev := s.last
	s.last = st
	s.mu.Unlock()

	s.props.SetMust(ifacePlayer, "Position", st.Position.Microseconds())
	if st.Status != prev.Status {
		s.props.SetMust(ifacePlayer, "PlaybackStatus", st.Status)
	}
	if st.Volume != prev.Volume {
		s.props.SetMust(ifacePlayer, "Volume", st.Volume)
	}
	if st.CanNext != prev.CanNext {
		s.props.SetMust(ifacePlayer, "CanGoNext", st.CanNext)
	}
	if st.CanPrev != prev.CanPrev {
		s.props.SetMust(ifacePlayer, "CanGoPrevious", st.CanPrev)
	}
	if trackOf(st) != trackOf(prev) {
		s.props.SetMust(ifacePlayer, "Metadata", metadata(st))
	}
}

// Seeked tells clients the position jumped, so they resync their progress bars.
func (s *Server) Seeked(pos time.Duration) {
	_ = s.conn.Emit(path, ifacePlayer+".Seeked", pos.Microseconds())
}

// Close releases the bus connection.
func (s *Server) Close() error {
	return s.conn.Close()
}

func (s *Server) onVolume(c *prop.Change) *dbus.Error {
	v, _ := c.Value.(float64)
	return s.call(func(ctx context.Context) error {
		return s.ctrl.SetVolume(ctx, min(max(v, 0), 1)*100)
	})
}

func (s *Server) call(fn func(ctx context.Context) error) *dbus.Error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := fn(ctx); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

func (s *Server) snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// track holds the fields that define Metadata, so Update can compare
// states with == instead of reflecting over dbus.Variant maps.
type track struct {
	id, title, artist, url, art string
	length                      time.Duration
}

func trackOf(st State) track {
	return track{st.TrackID, st.Title, st.Artist, st.URL, st.ArtURL, st.Length}
}

func metadata(st State) map[string]dbus.Variant {
	if st.TrackID == "" {
		return map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(noTrack)}
	}
	m := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(TrackPath(st.TrackID)),
		"xesam:title":   dbus.MakeVariant(st.Title),
		"xesam:url":     dbus.MakeVariant(st.URL),
	}
	if st.Artist != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{st.Artist})
	}
	if st.ArtURL != "" {
		m["mpris:artUrl"] = dbus.MakeVariant(st.ArtURL)
	}
	if st.Length > 0 {
		m["mpris:length"] = dbus.MakeVariant(st.Length.Microseconds())
	}
	return m
}

// TrackPath maps a track id to a valid D-Bus object path. YouTube ids contain
// '-' which object paths reject, hence the hex encoding.
func TrackPath(id string) dbus.ObjectPath {
	return dbus.ObjectPath("/org/omegaatt36/ytea/track/" + hex.EncodeToString([]byte(id)))
}

type root struct{}

func (root) Raise() *dbus.Error { return nil }
func (root) Quit() *dbus.Error  { return nil }

// player implements org.mpris.MediaPlayer2.Player methods.
type player struct{ s *Server }

// playerMethodNames renames Go methods to their D-Bus names. SeekBy exists
// because a Go method named Seek must match io.Seeker's signature to pass vet.
var playerMethodNames = map[string]string{"SeekBy": "Seek"}

func playerMethods(p player) []introspect.Method {
	methods := introspect.Methods(p)
	for i, m := range methods {
		if name, ok := playerMethodNames[m.Name]; ok {
			methods[i].Name = name
		}
	}
	return methods
}

func (p player) Next() *dbus.Error { return p.s.call(p.s.ctrl.Next) }

func (p player) Previous() *dbus.Error { return p.s.call(p.s.ctrl.Prev) }

func (p player) Stop() *dbus.Error { return p.s.call(p.s.ctrl.Stop) }

func (p player) PlayPause() *dbus.Error { return p.s.call(p.s.ctrl.TogglePause) }

func (p player) Pause() *dbus.Error {
	return p.s.call(func(ctx context.Context) error { return p.s.ctrl.SetPause(ctx, true) })
}

func (p player) Play() *dbus.Error {
	return p.s.call(func(ctx context.Context) error { return p.s.ctrl.SetPause(ctx, false) })
}

func (p player) SeekBy(offsetUS int64) *dbus.Error {
	return p.s.call(func(ctx context.Context) error {
		return p.s.ctrl.Seek(ctx, time.Duration(offsetUS)*time.Microsecond)
	})
}

func (p player) SetPosition(track dbus.ObjectPath, posUS int64) *dbus.Error {
	st := p.s.snapshot()
	// The spec requires ignoring stale requests aimed at a previous track.
	if st.TrackID == "" || track != TrackPath(st.TrackID) {
		return nil
	}
	pos := time.Duration(posUS) * time.Microsecond
	if pos < 0 || (st.Length > 0 && pos > st.Length) {
		return nil
	}
	return p.s.call(func(ctx context.Context) error { return p.s.ctrl.SeekTo(ctx, pos) })
}

func (p player) OpenUri(string) *dbus.Error {
	return dbus.MakeFailedError(fmt.Errorf("OpenUri is not supported"))
}

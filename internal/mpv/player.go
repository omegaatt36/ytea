package mpv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Observed properties. mpv pushes a property-change event whenever one changes.
const (
	PropTimePos     = "time-pos"
	PropDuration    = "duration"
	PropPause       = "pause"
	PropVolume      = "volume"
	PropPlaylist    = "playlist"
	PropPlaylistPos = "playlist-pos"
	PropIdle        = "idle-active"
	PropCodec       = "audio-codec-name"
	PropAudioParams = "audio-params"
	PropAudioDevice = "audio-device"
	PropAF          = "af"
	// PropLoopPlaylist and PropLoopFile carry the Repeat mode.
	PropLoopPlaylist = "loop-playlist"
	PropLoopFile     = "loop-file"
)

// PropAudioDeviceList lists the devices of the audio output mpv picked. Unlike
// the observed properties it is only read on demand, when the picker opens.
const PropAudioDeviceList = "audio-device-list"

var observed = []string{
	PropTimePos, PropDuration, PropPause, PropVolume, PropPlaylist,
	PropPlaylistPos, PropIdle, PropCodec, PropAudioParams, PropAudioDevice, PropAF,
	PropLoopPlaylist, PropLoopFile,
}

// normalizeFilter evens out loudness across uploads, which on YouTube can
// differ by 10dB+. dynaudnorm is used over loudnorm because loudnorm's
// realtime mode upsamples to 192kHz.
const normalizeFilter = "@norm:lavfi=[dynaudnorm=f=250:g=15:p=0.95]"

// NormalizeLabel is the af label of the loudness filter.
const NormalizeLabel = "norm"

// Config controls how mpv is launched.
type Config struct {
	Bin string
	// ClientName names mpv's audio stream (--audio-client-name); the PipeWire
	// spectrum tap finds the stream by it on Linux.
	ClientName string
	// AudioDevice is an mpv device name from AudioDevices, such as
	// "pipewire/<sink>" (Linux) or "coreaudio/<id>" (macOS); empty means the default.
	AudioDevice        string
	Volume             int
	Normalize          bool
	LogFile            string
	Cookies            string
	CookiesFromBrowser string
}

const premiumClients = "youtube:player_client=default,web_music"

// Player owns an mpv process and its IPC connection.
type Player struct {
	cmd    *exec.Cmd
	client *Client
	exited chan struct{}
}

// ipcFD is where mpv finds its end of the IPC socketpair: the first of cmd.ExtraFiles.
const ipcFD = 3

// Start launches mpv in idle audio-only mode and connects to it.
//
// mpv talks over one end of a socketpair and quits when the other end closes.
// The kernel closes it whenever ytea exits, SIGKILL included, so mpv can never
// outlive ytea.
func Start(ctx context.Context, cfg Config) (*Player, error) {
	ours, theirs, err := socketpair()
	if err != nil {
		return nil, err
	}
	defer theirs.Close()

	// WithoutCancel: mpv must outlive the startup context and is stopped via Quit.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), cfg.Bin, cfg.args()...)
	cmd.ExtraFiles = []*os.File{theirs}
	if err := cmd.Start(); err != nil {
		_ = ours.Close()
		return nil, fmt.Errorf("start mpv: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	conn, err := net.FileConn(ours)
	_ = ours.Close()
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("wrap mpv ipc socket: %w", err)
	}

	p := &Player{cmd: cmd, client: newClient(conn), exited: exited}
	for i, prop := range observed {
		if _, err := p.client.Command(ctx, "observe_property", i+1, prop); err != nil {
			_ = p.Quit()
			return nil, fmt.Errorf("observe mpv property %s: %w", prop, err)
		}
	}
	return p, nil
}

// args is the mpv command line for cfg.
func (cfg Config) args() []string {
	args := []string{
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--osc=no",
		"--load-stats-overlay=no",
		"--audio-client-name=" + cfg.ClientName,
		fmt.Sprintf("--input-ipc-client=fd://%d", ipcFD),
		"--ytdl-format=bestaudio/best",
		"--prefetch-playlist=yes",
		"--gapless-audio=weak",
		"--cache=yes",
		fmt.Sprintf("--volume=%d", cfg.Volume),
	}
	if cfg.AudioDevice != "" {
		args = append(args, "--audio-device="+cfg.AudioDevice)
	}
	if cfg.Normalize {
		args = append(args, "--af="+normalizeFilter)
	}
	if cfg.LogFile != "" {
		args = append(args, "--log-file="+cfg.LogFile)
	}
	var cookies string
	switch {
	case cfg.Cookies != "":
		cookies = "cookies=" + cfg.Cookies
	case cfg.CookiesFromBrowser != "":
		cookies = "cookies-from-browser=" + cfg.CookiesFromBrowser
	}
	if cookies != "" {
		args = append(args,
			"--ytdl-raw-options-append="+cookies,
			"--ytdl-raw-options-append=extractor-args="+premiumClients,
		)
	}
	return args
}

// socketpair returns a connected pair of unix sockets, both close-on-exec so
// no other child inherits them; exec.Cmd clears the flag on the ExtraFiles it passes.
func socketpair() (ours, theirs *os.File, err error) {
	// ForkLock keeps a concurrent exec from inheriting the fds before CloseOnExec.
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("create mpv ipc socketpair: %w", err)
	}
	return os.NewFile(uintptr(fds[0]), "mpv-ipc"), os.NewFile(uintptr(fds[1]), "mpv-ipc-child"), nil
}

// Events delivers mpv events; it is closed when mpv goes away.
func (p *Player) Events() <-chan Event {
	return p.client.Events()
}

// Append adds url to the end of the playlist, starting playback if idle.
func (p *Player) Append(ctx context.Context, url string) error {
	_, err := p.client.Command(ctx, "loadfile", url, "append-play")
	return err
}

// AppendAll adds urls to the end of the playlist, starting playback if idle.
func (p *Player) AppendAll(ctx context.Context, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	if err := p.Append(ctx, urls[0]); err != nil {
		return err
	}
	for _, url := range urls[1:] {
		// Plain append: a -play here would jump a player still leaving idle.
		if _, err := p.client.Command(ctx, "loadfile", url, "append"); err != nil {
			return err
		}
	}
	return nil
}

// PlayAll replaces the playlist with urls and starts urls[start].
func (p *Player) PlayAll(ctx context.Context, urls []string, start int) error {
	if start < 0 || start >= len(urls) {
		return fmt.Errorf("play all: start %d outside %d tracks", start, len(urls))
	}
	if err := p.Stop(ctx); err != nil {
		return err
	}
	// Plain appends leave the stopped player idle, so no other entry starts
	// resolving before the jump to start.
	for _, url := range urls {
		if _, err := p.client.Command(ctx, "loadfile", url, "append"); err != nil {
			return err
		}
	}
	return p.PlayIndex(ctx, start)
}

// PlayNow inserts url right after the current entry and starts it, keeping the rest of the queue intact.
func (p *Player) PlayNow(ctx context.Context, url string) error {
	if _, err := p.client.Command(ctx, "loadfile", url, "insert-next-play"); err != nil {
		return err
	}
	return p.SetPause(ctx, false)
}

// PlayIndex jumps to a playlist entry.
func (p *Player) PlayIndex(ctx context.Context, index int) error {
	if _, err := p.client.Command(ctx, "playlist-play-index", index); err != nil {
		return err
	}
	return p.SetPause(ctx, false)
}

// Remove deletes a playlist entry.
func (p *Player) Remove(ctx context.Context, index int) error {
	_, err := p.client.Command(ctx, "playlist-remove", index)
	return err
}

// Move moves the playlist entry at from so that it lands at index to.
func (p *Player) Move(ctx context.Context, from, to int) error {
	// playlist-move inserts before the target index, so moving down needs +1.
	if to > from {
		to++
	}
	_, err := p.client.Command(ctx, "playlist-move", from, to)
	return err
}

// Playlist reads the current entries and selected position from mpv.
func (p *Player) Playlist(ctx context.Context) ([]PlaylistEntry, int, error) {
	data, err := p.client.Command(ctx, "get_property", PropPlaylist)
	if err != nil {
		return nil, -1, fmt.Errorf("read playlist: %w", err)
	}
	entries := Decode[[]PlaylistEntry](data)

	data, err = p.client.Command(ctx, "get_property", PropPlaylistPos)
	if err != nil {
		return nil, -1, fmt.Errorf("read playlist position: %w", err)
	}
	pos := -1
	if string(data) != "null" {
		pos = Decode[int](data)
	}
	if pos < -1 || pos >= len(entries) {
		pos = -1
	}
	return entries, pos, nil
}

// Next skips to the next playlist entry.
func (p *Player) Next(ctx context.Context) error {
	_, err := p.client.Command(ctx, "playlist-next")
	return err
}

// Prev returns to the previous playlist entry.
func (p *Player) Prev(ctx context.Context) error {
	_, err := p.client.Command(ctx, "playlist-prev")
	return err
}

// Stop clears the playlist and halts playback.
func (p *Player) Stop(ctx context.Context) error {
	_, err := p.client.Command(ctx, "stop")
	return err
}

// TogglePause flips the pause state.
func (p *Player) TogglePause(ctx context.Context) error {
	_, err := p.client.Command(ctx, "cycle", "pause")
	return err
}

// SetPause sets the pause state.
func (p *Player) SetPause(ctx context.Context, pause bool) error {
	_, err := p.client.Command(ctx, "set_property", PropPause, pause)
	return err
}

// Seek moves playback by offset from the current position.
func (p *Player) Seek(ctx context.Context, offset time.Duration) error {
	_, err := p.client.Command(ctx, "seek", offset.Seconds(), "relative")
	return err
}

// SeekPercent moves playback to percent of the current track's length.
func (p *Player) SeekPercent(ctx context.Context, percent float64) error {
	_, err := p.client.Command(ctx, "seek", percent, "absolute-percent")
	return err
}

// SeekTo moves playback to an absolute position.
func (p *Player) SeekTo(ctx context.Context, pos time.Duration) error {
	_, err := p.client.Command(ctx, "seek", pos.Seconds(), "absolute")
	return err
}

// AddVolume changes volume by delta percent.
func (p *Player) AddVolume(ctx context.Context, delta int) error {
	_, err := p.client.Command(ctx, "add", PropVolume, delta)
	return err
}

// SetVolume sets volume in percent.
func (p *Player) SetVolume(ctx context.Context, volume float64) error {
	_, err := p.client.Command(ctx, "set_property", PropVolume, volume)
	return err
}

// AudioDevice is one entry of mpv's audio-device-list property.
type AudioDevice struct {
	Name        string `json:"name"` // e.g. "auto", "pipewire/<sink>", "coreaudio/<id>"
	Description string `json:"description"`
}

// Label is a human-readable device name.
func (d AudioDevice) Label() string {
	if d.Description != "" {
		return d.Description
	}
	return d.Name
}

// AudioDevices lists what mpv can output to, following whichever audio output
// driver it picked (pipewire, coreaudio, …).
func (p *Player) AudioDevices(ctx context.Context) ([]AudioDevice, error) {
	data, err := p.client.Command(ctx, "get_property", PropAudioDeviceList)
	if err != nil {
		return nil, err
	}
	return Decode[[]AudioDevice](data), nil
}

// StreamInfo describes the file mpv is playing. Like the device list it is
// only read on demand, when the info panel opens.
type StreamInfo struct {
	// Path is the playlist entry, such as a watch URL.
	Path string
	// Opened is what mpv opened once yt-dlp resolved Path, often an edl://
	// wrapper around the media URL.
	Opened string
	// Codec is the decoder's long name, e.g. "Opus (Opus Interactive Audio Codec)".
	Codec string
	// Bitrate is the decoder's running estimate in bits per second.
	Bitrate int
}

// StreamInfo reads what mpv knows about the current file. Properties that are
// unavailable, as while the file is still loading, are left zero.
func (p *Player) StreamInfo(ctx context.Context) (StreamInfo, error) {
	var info StreamInfo
	for prop, set := range map[string]func(json.RawMessage){
		"path":                 func(d json.RawMessage) { info.Path = Decode[string](d) },
		"stream-open-filename": func(d json.RawMessage) { info.Opened = Decode[string](d) },
		"audio-codec":          func(d json.RawMessage) { info.Codec = Decode[string](d) },
		"audio-bitrate":        func(d json.RawMessage) { info.Bitrate = int(Decode[float64](d)) },
	} {
		data, err := p.client.Command(ctx, "get_property", prop)
		if _, unavailable := errors.AsType[*CommandError](err); err != nil && !unavailable {
			return StreamInfo{}, fmt.Errorf("read %s: %w", prop, err)
		}
		set(data)
	}
	return info, nil
}

// SetAudioDevice routes output to an mpv audio device from AudioDevices.
func (p *Player) SetAudioDevice(ctx context.Context, device string) error {
	_, err := p.client.Command(ctx, "set_property", PropAudioDevice, device)
	return err
}

// SetNormalize adds or removes the loudness normalization filter.
func (p *Player) SetNormalize(ctx context.Context, on bool) error {
	op := "remove"
	if on {
		op = "add"
	}
	_, err := p.client.Command(ctx, "af", op, normalizeFilter)
	return err
}

// PID is mpv's process id; the macOS spectrum tap captures audio by it.
func (p *Player) PID() int {
	return p.cmd.Process.Pid
}

// Quit stops mpv and waits for it to exit. Closing the IPC connection is what
// makes mpv quit.
func (p *Player) Quit() error {
	_ = p.client.Close()
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill mpv: %w", err)
		}
		<-p.exited
	}
	return nil
}

// PlaylistEntry is one element of mpv's playlist property.
type PlaylistEntry struct {
	Filename string `json:"filename"`
	Title    string `json:"title"`
	Current  bool   `json:"current"`
	Playing  bool   `json:"playing"`
}

// Filter is one element of mpv's af property.
type Filter struct {
	Label string `json:"label"`
	Name  string `json:"name"`
}

// AudioParams is mpv's audio-params property.
type AudioParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"samplerate"`
	Channels   string `json:"channels"`
}

// Decode unmarshals a property value; a JSON null (property unavailable) yields the zero value.
func Decode[T any](data json.RawMessage) T {
	var v T
	if len(data) == 0 {
		return v
	}
	_ = json.Unmarshal(data, &v)
	return v
}

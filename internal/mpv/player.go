package mpv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
)

// PropAudioDeviceList lists the devices of the audio output mpv picked. Unlike
// the observed properties it is only read on demand, when the picker opens.
const PropAudioDeviceList = "audio-device-list"

var observed = []string{
	PropTimePos, PropDuration, PropPause, PropVolume, PropPlaylist,
	PropPlaylistPos, PropIdle, PropCodec, PropAudioParams, PropAudioDevice, PropAF,
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
	// Socket is the IPC socket path; any stale file is removed.
	Socket string
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
	socket string
	exited chan struct{}
}

// Start launches mpv in idle audio-only mode and connects to it.
func Start(ctx context.Context, cfg Config) (*Player, error) {
	if err := os.Remove(cfg.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale mpv socket %s: %w", cfg.Socket, err)
	}

	// WithoutCancel: mpv must outlive the startup context and is stopped via Quit.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), cfg.Bin, cfg.args()...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mpv: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	client, err := dialWithRetry(ctx, cfg.Socket, exited)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}

	p := &Player{cmd: cmd, client: client, socket: cfg.Socket, exited: exited}
	for i, prop := range observed {
		if _, err := client.Command(ctx, "observe_property", i+1, prop); err != nil {
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
		"--audio-client-name=" + cfg.ClientName,
		"--input-ipc-server=" + cfg.Socket,
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

// dialWithRetry waits for mpv to create its socket, which happens shortly after exec.
func dialWithRetry(ctx context.Context, socket string, exited <-chan struct{}) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := Dial(ctx, socket)
		if err == nil {
			return client, nil
		}
		select {
		case <-exited:
			return nil, fmt.Errorf("mpv exited before opening %s", socket)
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for mpv socket: %w", err)
		case <-ticker.C:
		}
	}
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

// Quit stops mpv, waits for it to exit and removes its socket.
func (p *Player) Quit() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = p.client.Command(ctx, "quit")
	_ = p.client.Close()

	var errs []error
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		if err := p.cmd.Process.Kill(); err != nil {
			errs = append(errs, fmt.Errorf("kill mpv: %w", err))
		}
		<-p.exited
	}
	// mpv leaves the socket file behind on quit.
	if err := os.Remove(p.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove mpv socket: %w", err))
	}
	return errors.Join(errs...)
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

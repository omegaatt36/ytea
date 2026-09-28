# ytea

A terminal YouTube music player.

Search with yt-dlp, play through mpv, and get the parts a shell-script
frontend can't give you: a queue you can edit while music plays, output
device switching, a live spectrum of ytea's own audio, media-key support,
and cover art in the terminal, including inside Zellij.

```
┌ Bubble Tea TUI ──────────────────────────────────┐
│ search │ results / playlists │ queue │ now playing │ spectrum │
└──┬─────────┬──────────┬───────────┬──────────────┘
 yt-dlp    mpv IPC    mpv IPC     PipeWire      D-Bus
 search    playback   devices     pw-cat tap    MPRIS
                                  pw-dump
```

## Features

- **Search while you listen.** Queue results with `a`, or play one right after the current track with `enter`. The queue is mpv's own playlist, so the next track is resolved ahead of time and track changes are near-gapless. On terminals at least 72 columns wide the Results tab shows the queue beside the results; the Queue tab gives the queue the full width, with each track's position, channel, and length.
- **Paste a link.** A playlist URL pasted into search imports its first 200 tracks straight into the queue and switches to the Queue tab, a mix queues its first 25 with the seed playing first, and a single video URL queues just that video. `r` queues a mix seeded by the track playing now.
- **Keep local playlists.** Save a result or queued track with `s`, or save the whole queue with `S`. Create named playlists and queue a saved track or an entire playlist later. Playlists are stored on this device and do not modify your YouTube account.
- **Resume your session.** On exit, ytea saves the queue, selected track, volume, and repeat mode locally. On the next launch it restores them with playback paused.
- **Repeat and shuffle.** `L` cycles repeat through off, all, and one; the now-playing bar shows `repeat all` or `repeat one` while it is on. `Z` in the Queue shuffles the tracks after the current one.
- **Audio-only, best quality.** Streams `bestaudio` (usually Opus, ~130 kbps; 256 kbps with [YouTube Premium](#youtube-premium-audio)) to the audio output mpv picked: PipeWire on Linux, Core Audio on macOS. No video is fetched.
- **Loudness leveling.** An on/off `dynaudnorm` filter (`N`) evens out volume between uploads.
- **Output switching.** Pick any device mpv can output to with `o` — PipeWire sinks on Linux, Core Audio devices on macOS. The choice applies only to ytea, not to the system default.
- **Live spectrum (Linux).** Captured from ytea's own PipeWire stream, so other apps' sound never shows up in it. Auto-disabled where PipeWire is absent.
- **Media keys (Linux).** Registers as an MPRIS player, so keyboard media keys, desktop widgets and `playerctl` can control it.
- **Cover art.** Uses the best method the terminal supports (see [Thumbnails](#thumbnails)).
- **Ghostty niceties.** The window title shows the current track, and playback progress appears in the tab (OSC 9;4).

## Requirements

| Needed for | Dependency |
|---|---|
| everything | `mpv`, `yt-dlp` (runs wherever mpv does; developed on Linux) |
| spectrum | Linux with PipeWire, plus `pw-dump` and `pw-cat` (auto-disabled elsewhere) |
| media keys | a D-Bus session bus (Linux; optional; ytea runs without it) |
| `ctrl+v` paste | Linux: `wl-paste` (Wayland) or `xclip`/`xsel` (X11); macOS works out of the box |
| building | Go 1.27+ |

Keep yt-dlp current. YouTube changes regularly break old versions, and the symptom is failed searches or tracks that won't play.

## Install

```sh
go install -trimpath -ldflags="-s -w" github.com/omegaatt36/ytea/cmd/ytea@latest
```

## Keys

### Playback

| Key | Action |
|---|---|
| `space` | pause / resume |
| `←` | seek -5s |
| `→` | seek +5s |
| `n` `>` | next |
| `p` `<` | prev |
| `+` `=` | vol up (`=` is the unshifted volume up alias) |
| `-` | vol down |
| `N` | toggle loudness leveling |
| `L` | cycle repeat: off → all → one |

### Navigation and global

| Key | Action |
|---|---|
| `/` | focus search |
| `ctrl+v` `ctrl+shift+v` `shift+insert` | paste into search (terminal paste also works) |
| `tab` | next pane (Results, Queue, Playlists) |
| `shift+tab` | prev pane |
| `o` | choose output device |
| `i` | track info: URL, YouTube format (itag, bitrate, size), codec, output |
| `v` | toggle spectrum (Linux with PipeWire; hidden elsewhere) |
| `r` | radio: queue a mix seeded by the current track |
| `?` | more: open full help |
| `q` | quit |
| `ctrl+c` | quit |

While full help is open, `?`, `esc`, or `q` closes it; `q` does not quit.

### Search

| Key | Action |
|---|---|
| `enter` | search YouTube |
| `esc` `tab` | leave |

### Results

| Key | Action |
|---|---|
| `↑` `k` | up |
| `↓` `j` | down |
| `g` `home` | top |
| `G` `end` | bottom |
| `enter` | play now (inserted after the current track) |
| `a` | queue at the end |
| `s` | save to a local playlist |
| `f` | filter the results by title or channel |
| `esc` | clear filter (shown while a filter is applied) |

### Results filter

| Key | Action |
|---|---|
| `↑` `ctrl+k` | up (move while typing) |
| `↓` `ctrl+j` | down (move while typing) |
| `enter` | apply filter and return to the list |
| `esc` | clear filter |

### Queue

| Key | Action |
|---|---|
| `↑` `k` | up |
| `↓` `j` | down |
| `g` `home` | top |
| `G` `end` | bottom |
| `enter` | jump to this track |
| `d` `x` `delete` | remove |
| `C` | clear queue and stop playback |
| `K` `shift+↑` | move up |
| `J` `shift+↓` | move down |
| `Z` | shuffle the tracks after the current one |
| `s` | save track to a local playlist |
| `S` | save queue as a new local playlist |

### Playlists

| Key | Action |
|---|---|
| `↑` `k` | up |
| `↓` `j` | down |
| `g` `home` | top |
| `G` `end` | bottom |
| `enter` | browse the selected playlist |
| `c` | new playlist |
| `a` | queue all tracks in the playlist |
| `D` | delete playlist (press `D` twice) |
| `esc` | back to Results |

### Playlist tracks

| Key | Action |
|---|---|
| `↑` `k` | up |
| `↓` `j` | down |
| `g` `home` | top |
| `G` `end` | bottom |
| `enter` | play now |
| `a` | queue |
| `d` | remove saved track |
| `esc` | back to Playlists |

### Output

| Key | Action |
|---|---|
| `↑` `k` | up |
| `↓` `j` | down |
| `enter` | switch |
| `esc` `o` `q` | cancel |

### Track info

| Key | Action |
|---|---|
| `y` | copy url to the clipboard (OSC 52) |
| `esc` `i` `q` | close |

### Save to playlist

| Key | Action |
|---|---|
| `↑` `k` | up |
| `↓` `j` | down |
| `g` `home` | top |
| `G` `end` | bottom |
| `enter` | save track |
| `c` | new playlist |
| `esc` | cancel |

### New playlist

| Key | Action |
|---|---|
| `enter` | create playlist |
| `esc` | cancel |

When you press `s` on a result or queued track, select a playlist with `enter` or press `c` to create one. The first saved track prompts for a playlist name automatically.

**Mouse**

Click the search box, a tab, or a list row to focus or select it, and scroll the wheel over a list to move its selection. Playing and editing stay on the keys. Because ytea captures the mouse, hold `shift` while dragging to select text (the modifier varies by terminal).

## Flags

```
--config FILE                  TOML config (default: $XDG_CONFIG_HOME/ytea/config.toml)
--volume 80                    initial volume in percent
--[no-]normalize               loudness leveling at start (default on)
--audio-device ""              mpv audio device name (default: system default)
--[no-]thumbnails              cover art (default on)
--[no-]visualizer              spectrum (default on)
--[no-]mpris                   register org.mpris.MediaPlayer2.ytea (default on)
--mpv mpv                      mpv binary
--yt-dlp yt-dlp                yt-dlp binary
--cookies FILE                 Netscape cookies.txt for yt-dlp playback
--cookies-from-browser NAME    read cookies from a browser, e.g. firefox or "chrome:Profile 1"
```

`--cookies` and `--cookies-from-browser` are mutually exclusive.

Every flag can also come from an environment variable (`--audio-device` reads `YTEA_AUDIO_DEVICE`) or from the config file. A flag wins over the environment, which wins over the config file.

Logs go to `$XDG_STATE_HOME/ytea/` (`ytea.log`, `mpv.log`), since the TUI owns the terminal.
The previous queue, known song titles and artists, selected track, volume, and repeat mode are stored in `session.json` in the same directory. Playback resumes paused from the start of the selected track.
Named local playlists are stored separately in `playlists.json` in that directory, so clearing the queue or replacing the last session does not remove them.
Edit local playlists from one ytea instance at a time; simultaneous instances can overwrite each other's playlist changes.

## Config

`~/.config/ytea/config.toml` (or `$XDG_CONFIG_HOME/ytea/config.toml`) uses the flag names as keys:

```toml
volume = 70
normalize = false
audio-device = "pipewire/alsa_output.usb-xxx"   # mpv audio device name
cookies = "cookies.txt"   # relative to this file; ~/ is expanded
```

The file is optional. Unknown keys and wrong types are errors rather than being ignored, so a typo won't silently fall back to a default.

## YouTube Premium audio

Signed in with a YouTube Premium account, yt-dlp is offered two extra formats: `774` (Opus ~256 kbps) and `141` (AAC ~256 kbps). `bestaudio` picks `774`. Not every video has them; the rest fall back to `251` (Opus ~130 kbps).

Put the exported cookies at `~/.config/ytea/cookies.txt` and ytea uses them without any flag. `ytea.log` records which cookie source playback used. Otherwise:

```sh
ytea --cookies ~/Downloads/youtube-cookies.txt
ytea --cookies-from-browser firefox
```

Signed in, playback also queries the YouTube Music (`web_music`) client, which is the one that serves these formats. Search stays signed out.

- **yt-dlp needs a JavaScript runtime.** Without one, a signed-in request can return no audio at all, and the track won't play. yt-dlp only enables deno by default; to use node or bun instead, add `--js-runtimes node` to `~/.config/yt-dlp/config`.
- **Export cookies from a private window.** Browsers rotate YouTube cookies, which invalidates a cookies.txt exported from a normal session. Sign in in a private window, export, then close the window without signing out.
- **Keep the file private and out of version control.** It holds a live session for your account. ytea logs a warning if other users can read it; `chmod 600` it.
- Heavy automated use of a signed-in account can get it rate-limited or flagged.

To see which formats a video offers your account:

```sh
yt-dlp --cookies FILE --extractor-args youtube:player_client=default,web_music -F <url>
```

## Thumbnails

At startup ytea probes the terminal and picks the best of three modes:

| Terminal | Mode | Result |
|---|---|---|
| Ghostty, kitty | kitty graphics with Unicode placeholders | full-resolution image |
| Zellij ≥ 0.45 | kitty graphics with direct placement | full-resolution image |
| Zellij < 0.45, tmux, others | truecolor half-block characters | low-res, but works anywhere |

Zellij 0.45 implements the kitty graphics protocol but rejects Unicode placeholders, so ytea sends two probes (with and without `U=1`) to tell the first two cases apart. `ytea.log` records which mode was chosen.

## How it works

- **Playback.** mpv runs headless (`--idle --no-video`) and is driven over its JSON IPC on an inherited socketpair; mpv quits when that connection closes, so it never outlives ytea, even a SIGKILLed one. mpv picks its own audio output at runtime: `pipewire` where that is compiled in, coreaudio on macOS. ytea watches mpv's properties, so its UI and the MPRIS state always reflect what mpv is actually doing.
- **Output switching.** ytea reads mpv's `audio-device-list` over its IPC socket, so the picker follows whichever audio output mpv picked (PipeWire sinks on Linux, Core Audio devices on macOS). Switching sets mpv's `audio-device`, so the choice survives track changes.
- **Spectrum (Linux).** mpv's stream is named `ytea-<pid>` in the PipeWire graph. `pw-cat --record --target <serial>` records that stream node directly, not the sink monitor. The serial changes whenever mpv reopens its output, so the tap re-resolves it every second.
- **Thumbnails.** Placeholders are ordinary text cells that Bubble Tea's renderer draws like any other text. Direct placement moves the cursor to the thumbnail cell, puts the image, and restores the cursor. It re-places the image when the layout moves or the window is resized.

## Known issues

- Inside Zellij, result rows containing some emoji can leave stray border characters. This is likely a character-width disagreement between ytea and Zellij.
- Built for Linux. Playback and output switching run anywhere mpv runs (macOS plays through Core Audio), but the spectrum depends on PipeWire and media keys on D-Bus.

## Development

```sh
go test -race ./...
go run ./cmd/ytea --audio-device <a muted device>   # test without sound
```

## Acknowledgements

ytea is inspired by [ytfzf](https://github.com/pystardust/ytfzf), the fzf-based YouTube frontend. It started as an attempt to rebuild that workflow around a persistent TUI and PipeWire. ytea shares no code with ytfzf.

## License

[MIT](LICENSE)

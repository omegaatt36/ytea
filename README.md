# ytea

A terminal YouTube music player for Linux desktops running PipeWire.

Search with yt-dlp, play through mpv, and get the parts a shell-script
frontend can't give you: a queue you can edit while music plays, output
device switching, a live spectrum of ytea's own audio, media-key support,
and cover art in the terminal, including inside Zellij.

```
┌ Bubble Tea TUI ──────────────────────────────────┐
│ search │ results │ queue │ now playing │ spectrum │
└──┬─────────┬──────────┬───────────┬──────────────┘
 yt-dlp    mpv IPC    PipeWire     D-Bus
 search    playback   pw-dump      MPRIS
                      pw-cat tap
```

## Features

- **Search while you listen.** Queue results with `a`, or play one right after the current track with `enter`. The queue is mpv's own playlist, so the next track is resolved ahead of time and track changes are near-gapless.
- **Audio-only, best quality.** Streams `bestaudio` (usually Opus, ~160 kbps) straight to PipeWire. No video is fetched.
- **Loudness leveling.** An on/off `dynaudnorm` filter (`N`) evens out volume between uploads.
- **Output switching.** Pick any PipeWire sink with `o`. The choice applies only to ytea, not to the system default.
- **Live spectrum.** Captured from ytea's own PipeWire stream, so other apps' sound never shows up in it.
- **Media keys.** Registers as an MPRIS player, so keyboard media keys, desktop widgets and `playerctl` can control it.
- **Cover art.** Uses the best method the terminal supports (see [Thumbnails](#thumbnails)).
- **Ghostty niceties.** The window title shows the current track, and playback progress appears in the tab (OSC 9;4).

## Requirements

| Needed for | Dependency |
|---|---|
| everything | Linux with PipeWire, `mpv` built with the `pipewire` audio output, `yt-dlp` |
| output switching, spectrum | `pw-dump`, `pw-cat` (PipeWire tools) |
| media keys | a D-Bus session bus (optional; ytea runs without it) |
| `ctrl+v` paste | `wl-paste` (Wayland) or `xclip`/`xsel` (X11) |
| building | Go 1.27+ |

Keep yt-dlp current. YouTube changes regularly break old versions, and the symptom is failed searches or tracks that won't play.

## Install

```sh
go install github.com/omegaatt36/ytea/cmd/ytea@latest
```

## Keys

**Anywhere**

| Key | Action |
|---|---|
| `/` | focus search (`enter` runs it, `esc` leaves) |
| `ctrl+v` `ctrl+shift+v` `shift+insert`, terminal paste | paste into search |
| `tab` | switch between results and queue |
| `space` | pause / resume |
| `←` `→` | seek ±5s |
| `n` `p` (or `>` `<`) | next / previous track |
| `+` `-` | volume |
| `N` | toggle loudness leveling |
| `o` | choose output device (`enter` to switch, `esc` to cancel) |
| `v` | toggle spectrum |
| `q`, `ctrl+c` | quit |

**Results**

| Key | Action |
|---|---|
| `j` `k` / `↑` `↓`, `g` `G` | move, jump to top / bottom |
| `enter` | play now (inserted after the current track) |
| `a` | add to the end of the queue |

**Queue**

| Key | Action |
|---|---|
| `j` `k` / `↑` `↓`, `g` `G` | move, jump to top / bottom |
| `enter` | jump to this track |
| `d` / `x` / `delete` | remove |
| `J` `K` / `shift+↓` `shift+↑` | move track down / up |

## Flags

```
-volume 80          initial volume in percent
-normalize=true     start with loudness leveling on
-audio-device ""    mpv device such as pipewire/<sink node.name> (default: system sink)
-thumbnails=true    cover art
-visualizer=true    spectrum
-mpris=true         register org.mpris.MediaPlayer2.ytea
-mpv mpv            mpv binary
-yt-dlp yt-dlp      yt-dlp binary
```

Logs go to `$XDG_STATE_HOME/ytea/` (`ytea.log`, `mpv.log`), since the TUI owns the terminal.

## Thumbnails

At startup ytea probes the terminal and picks the best of three modes:

| Terminal | Mode | Result |
|---|---|---|
| Ghostty, kitty | kitty graphics with Unicode placeholders | full-resolution image |
| Zellij ≥ 0.45 | kitty graphics with direct placement | full-resolution image |
| Zellij < 0.45, tmux, others | truecolor half-block characters | low-res, but works anywhere |

Zellij 0.45 implements the kitty graphics protocol but rejects Unicode placeholders, so ytea sends two probes (with and without `U=1`) to tell the first two cases apart. `ytea.log` records which mode was chosen.

## How it works

- **Playback.** mpv runs headless (`--idle --no-video --ao=pipewire`) and is driven over its JSON IPC socket. ytea watches mpv's properties, so its UI and the MPRIS state always reflect what mpv is actually doing.
- **Output switching.** mpv's stream is named `ytea-<pid>` in the PipeWire graph. Switching output sets mpv's `audio-device` to `pipewire/<sink>`, so the choice survives track changes.
- **Spectrum.** `pw-cat --record --target <serial>` records that stream node directly, not the sink monitor. The serial changes whenever mpv reopens its output, so the tap re-resolves it every second.
- **Thumbnails.** Placeholders are ordinary text cells that Bubble Tea's renderer draws like any other text. Direct placement moves the cursor to the thumbnail cell, puts the image, and restores the cursor. It re-places the image when the layout moves or the window is resized.

## Known issues

- Inside Zellij, result rows containing some emoji can leave stray border characters. This is likely a character-width disagreement between ytea and Zellij.
- Linux only. Output switching, the spectrum and MPRIS all depend on PipeWire and D-Bus.

## Development

```sh
go test -race ./...
go run ./cmd/ytea -audio-device pipewire/<muted sink>   # test without sound
```

## Acknowledgements

ytea is inspired by [ytfzf](https://github.com/pystardust/ytfzf), the fzf-based YouTube frontend. It started as an attempt to rebuild that workflow around a persistent TUI and PipeWire. ytea shares no code with ytfzf.

## License

[MIT](LICENSE)

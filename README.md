# ytea

Terminal YouTube music player: yt-dlp search, mpv playback, PipeWire-native
output switching and spectrum, MPRIS media keys, inline thumbnails in Ghostty/kitty.

```
┌ Bubble Tea TUI ─────────────────────────────────┐
│ search │ results │ queue │ now playing │ spectrum │
└──┬─────────┬──────────┬───────────┬─────────────┘
 yt-dlp    mpv IPC    PipeWire     D-Bus
 search    playback   pw-dump      MPRIS
                      pw-cat tap
```

## Requirements

- `mpv` (with the `pipewire` audio output) and `yt-dlp` — keep yt-dlp current; YouTube breaks old versions.
- `pw-dump`, `pw-cat` (PipeWire tools) for output switching and the spectrum.
- A session D-Bus for MPRIS (optional).
- Ghostty or kitty for thumbnails (optional; other terminals just skip them).

## Install

```sh
go install github.com/omegaatt36/ytea/cmd/ytea@latest
```

## Keys

| Key | Action |
|---|---|
| `/` | search (enter to run, esc to leave) |
| `enter` | results: play now · queue: jump to entry |
| `a` | append result to queue |
| `tab` | switch results / queue |
| `d` `J` `K` | queue: remove, move down, move up |
| `space` | pause |
| `←` `→` | seek ±5s |
| `n` `p` | next / previous |
| `+` `-` | volume |
| `N` | toggle loudness normalization |
| `o` | pick PipeWire output device |
| `v` | toggle spectrum |
| `q` | quit |

## Flags

```
-volume 80           initial volume
-normalize=true      dynaudnorm loudness leveling
-audio-device ""     e.g. pipewire/alsa_output.usb-...  (default: system sink)
-thumbnails=true     kitty graphics thumbnails
-visualizer=true     spectrum from the PipeWire stream
-mpris=true          register org.mpris.MediaPlayer2.ytea
```

Logs: `$XDG_STATE_HOME/ytea/{ytea,mpv}.log`.

## How the PipeWire parts work

- mpv runs with `--ao=pipewire --audio-client-name=ytea-<pid>`, so its stream is a
  named node. Switching output sets mpv's `audio-device` to `pipewire/<sink>`.
- The spectrum runs `pw-cat --record --target <serial>` against that stream
  node (not the sink monitor), so only ytea's audio is analysed. The serial
  changes when mpv reopens its output, so the tap re-resolves it every second.
- Thumbnails use kitty Unicode placeholders, which Bubble Tea's cell renderer
  treats as normal text.

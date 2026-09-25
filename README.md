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
- **Audio-only, best quality.** Streams `bestaudio` (usually Opus, ~130 kbps; 256 kbps with [YouTube Premium](#youtube-premium-audio)) straight to PipeWire. No video is fetched.
- **Loudness leveling.** An on/off `dynaudnorm` filter (`N`) evens out volume between uploads.
- **Output switching.** Pick any PipeWire sink with `o`. The choice applies only to ytea, not to the system default.
- **Live spectrum.** Captured from ytea's own PipeWire stream, so other apps' sound never shows up in it.
- **Media keys.** Registers as an MPRIS player, so keyboard media keys, desktop widgets and `playerctl` can control it.
- **Cover art.** Uses the best method the terminal supports (see [Thumbnails](#thumbnails)).
- **Ghostty niceties.** The window title shows the current track, and playback progress appears in the tab (OSC 9;4).

## Requirements

| Needed for | Dependency |
|---|---|
| everything | Linux with PipeWire, `mpv`, `yt-dlp` |
| output switching, spectrum | `pw-dump`, `pw-cat` (PipeWire tools); switching also needs `mpv` built with the `pipewire` audio output |
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
--config FILE                  TOML config (default: $XDG_CONFIG_HOME/ytea/config.toml)
--volume 80                    initial volume in percent
--[no-]normalize               loudness leveling at start (default on)
--audio-device ""              mpv device such as pipewire/<sink node.name> (default: system sink)
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

## Config

`~/.config/ytea/config.toml` (or `$XDG_CONFIG_HOME/ytea/config.toml`) uses the flag names as keys:

```toml
volume = 70
normalize = false
audio-device = "pipewire/alsa_output.usb-xxx"
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

- **Playback.** mpv runs headless (`--idle --no-video`) and is driven over its JSON IPC socket. mpv picks its own audio output at runtime: `pipewire` where that is compiled in, coreaudio on macOS. ytea watches mpv's properties, so its UI and the MPRIS state always reflect what mpv is actually doing.
- **Output switching.** mpv's stream is named `ytea-<pid>` in the PipeWire graph. Switching output sets mpv's `audio-device` to `pipewire/<sink>`, so the choice survives track changes.
- **Spectrum.** `pw-cat --record --target <serial>` records that stream node directly, not the sink monitor. The serial changes whenever mpv reopens its output, so the tap re-resolves it every second.
- **Thumbnails.** Placeholders are ordinary text cells that Bubble Tea's renderer draws like any other text. Direct placement moves the cursor to the thumbnail cell, puts the image, and restores the cursor. It re-places the image when the layout moves or the window is resized.

## Known issues

- Inside Zellij, result rows containing some emoji can leave stray border characters. This is likely a character-width disagreement between ytea and Zellij.
- Built for Linux. Playback runs anywhere mpv runs (macOS plays through coreaudio), but output switching, the spectrum and MPRIS depend on PipeWire and D-Bus.

## Development

```sh
go test -race ./...
go run ./cmd/ytea -audio-device pipewire/<muted sink>   # test without sound
```

## Acknowledgements

ytea is inspired by [ytfzf](https://github.com/pystardust/ytfzf), the fzf-based YouTube frontend. It started as an attempt to rebuild that workflow around a persistent TUI and PipeWire. ytea shares no code with ytfzf.

## License

[MIT](LICENSE)

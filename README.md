# ytea

A clean, lightweight YouTube music player for your terminal.

Stream music without opening a browser. Features an interactive queue, local playlists, album art, audio spectrum, and seamless background playback.

## Install

Requires [`mpv`](https://mpv.io) and [`yt-dlp`](https://github.com/yt-dlp/yt-dlp).

```sh
go install -trimpath -ldflags="-s -w" github.com/omegaatt36/ytea/cmd/ytea@latest
```

## Quick Start

Launch `ytea` in your terminal:

```sh
ytea
```

- Press `/` to search YouTube, or paste any video / playlist URL directly.
- Press `enter` to play, or `a` to append to the queue.
- Press `?` anytime to open the full interactive keymap.

## Controls

| Key | Action |
|---|---|
| `/` | Search or paste URL |
| `space` | Pause / Resume |
| `←` / `→` | Seek ±5s |
| `n` / `p` | Next / Previous |
| `tab` | Switch pane (Results, Queue, Playlists) |
| `enter` | Play now |
| `a` | Add to queue |
| `s` | Save to playlist |
| `o` | Audio output device |
| `?` | Help |
| `q` | Quit |

*Mouse is also supported: click tabs, search, or list rows, and use the scroll wheel to navigate.*

## Configuration (Optional)

`~/.config/ytea/config.toml`:

```toml
volume = 70
normalize = false
audio-device = ""       # mpv audio device name
# optional, for YouTube Premium 256k audio; set one of:
cookies-from-browser = "firefox" # or "firefox:<profile dir>" for Firefox forks, e.g. Zen
# cookies = "cookies.txt"        # full login export
# youtube-channel-id = "UC..."     # for cookie-backed account playlists
```

To show your own YouTube playlists and Liked videos, see [YouTube Account Playlists](docs/youtube-account.md).

## Acknowledgements

Inspired by [ytfzf](https://github.com/pystardust/ytfzf).

## License

[MIT](LICENSE)

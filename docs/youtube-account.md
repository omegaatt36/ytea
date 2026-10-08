# YouTube Account Playlists

ytea can list your own playlists and Liked videos in the Playlists pane. Pick one source: cookies or OAuth. If both are set, OAuth wins.

## Cookies

Add your channel ID (starts with `UC`, not your handle) to `~/.config/ytea/config.toml`:

```toml
cookies-from-browser = "firefox"
youtube-channel-id = "UC..."
```

## OAuth

OAuth reads playlists through the YouTube Data API v3. It does not unlock Premium audio. That still needs cookies.

### 1. Create a Google OAuth client

1. In [Google Cloud](https://console.cloud.google.com/apis/library), enable the [YouTube Data API v3](https://console.cloud.google.com/apis/library/youtube.googleapis.com).
2. [Create an OAuth client](https://console.cloud.google.com/auth/clients) of type **TVs and Limited Input devices**.
3. If the app audience is **Testing**, add yourself as a test user. You must log in again every 7 days.

### 2. Add the credentials

`~/.config/ytea/config.toml`:

```toml
google-client-id = "..."
google-client-secret = "..."
```

You can also use the `--google-client-id` / `--google-client-secret` flags or the `YTEA_GOOGLE_CLIENT_ID` / `YTEA_GOOGLE_CLIENT_SECRET` environment variables.

### 3. Log in

```sh
ytea auth login    # open the printed URL and enter the code
ytea auth logout   # delete the local token
```

The token is saved to `~/.config/ytea/google-token.json`. To revoke access, remove ytea from your [Google Account connections](https://myaccount.google.com/connections).

## Ignore playlists

In the Playlists pane, select a YouTube playlist and press `I` (Shift+i).

ytea saves the playlist title to the ignore list and hides all YouTube playlists with that exact title.
The comparison is case-sensitive.
The ignore list applies after reloads and restarts, with either cookies or OAuth.
Local playlists and playlists on YouTube stay unchanged.

The ignore list is in `~/.config/ytea/youtube-ignored-playlists.json`:

```json
["interview", "練字"]
```

If `XDG_CONFIG_HOME` is set, the file is at `$XDG_CONFIG_HOME/ytea/youtube-ignored-playlists.json` instead.
The shortcut does not change `config.toml` or the local playlists.

If the new file is missing, ytea copies the old ignore list from the state directory's `playlists.json`.
An existing new file takes priority, including an empty array `[]`.
The old file stays unchanged.

To restore a hidden playlist:

1. Quit ytea.
2. Open `youtube-ignored-playlists.json`.
3. Remove the title from the array.
4. Restart ytea.

To restore all hidden playlists, replace the array with `[]`.

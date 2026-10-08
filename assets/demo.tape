Set Shell "zsh"
Set FontSize 14
Set Width 1200
Set Height 720
Set Theme { "background": "#100f0f", "foreground": "#cecdc3", "cursor": "#cecdc3", "selection": "#403e3c", "black": "#100f0f", "red": "#d14d41", "green": "#879a39", "yellow": "#d0a215", "blue": "#4385be", "magenta": "#ce5d97", "cyan": "#3aa99f", "white": "#878580", "brightBlack": "#575653", "brightRed": "#af3029", "brightGreen": "#66800b", "brightYellow": "#ad8301", "brightBlue": "#205ea6", "brightMagenta": "#a02f6f", "brightCyan": "#24837b", "brightWhite": "#cecdc3" }
Set WindowBar "Colorful"
Set WindowBarSize 40
Set Padding 20

Output "assets/demo.gif"

Type "ytea --volume 0"
Enter
Sleep 3s

# Switch to Queue
Tab
Sleep 1.5s
Down 2
Sleep 1s

# Switch to Playlists
Tab
Sleep 1.5s

# Switch to History
Tab
Sleep 1.5s

# Show Help dialog
Type "?"
Sleep 2.5s
Escape
Sleep 1s

# Back to search and quit
Type "/"
Sleep 1s
Type "q"
Sleep 500ms

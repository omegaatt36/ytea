package tui

import (
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
)

type readmeFullHelpMap interface {
	FullHelp() [][]key.Binding
}

type readmeKeyGroup struct {
	title       string
	keymap      readmeFullHelpMap
	addBindings []key.Binding
}

type readmeKeyRow struct {
	keys   []string
	action string
}

// tui-keymap-help R7 [derived]: binding rows come from the keymap; the task brief supplies the explicit help behavior and platform wording.
func TestREADMEKeysMatchFullHelpKeymap(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	section := readmeKeysSection(t, string(readme))

	keys := newKeyMap()
	groups := []readmeKeyGroup{
		{title: "Playback", keymap: keys.playback},
		{title: "Navigation and global", keymap: keys.global, addBindings: []key.Binding{keys.global.Help}},
	}
	contexts := []Model{
		{keys: keys, focus: focusSearch, overlay: overlayNone},
		{keys: keys, focus: focusResults, overlay: overlayNone},
		readmeFilterInputModel(keys),
		{keys: keys, focus: focusQueue, overlay: overlayNone},
		{keys: keys, focus: focusPlaylists, overlay: overlayNone},
		{keys: keys, focus: focusPlaylistTracks, overlay: overlayNone},
		{keys: keys, overlay: overlayDevices},
		{keys: keys, overlay: overlayInfo},
		{keys: keys, overlay: overlayPicker},
		{keys: keys, overlay: overlayName},
	}
	for _, model := range contexts {
		title, keymap := model.contextKeys()
		groups = append(groups, readmeKeyGroup{title: title, keymap: keymap})
	}

	wantTitles := make([]string, len(groups))
	for i, group := range groups {
		wantTitles[i] = group.title
	}
	if got := readmeKeyGroupTitles(section); !slices.Equal(got, wantTitles) {
		t.Fatalf("README key groups = %q, want %q", got, wantTitles)
	}

	for _, group := range groups {
		got := readmeKeyRows(readmeKeyGroupBody(section, group.title))
		want := readmeFullHelpRows(group.keymap)
		for _, binding := range group.addBindings {
			want = append(want, readmeRowForBinding(binding))
		}
		if gotRows, wantRows := readmeKeyRowIDs(got), readmeKeyRowIDs(want); !slices.Equal(gotRows, wantRows) {
			t.Errorf("%s README key rows = %q, want keymap rows %q", group.title, gotRows, wantRows)
		}
		for _, expected := range want {
			id := readmeKeyRowID(expected.keys)
			for _, actual := range got {
				if readmeKeyRowID(actual.keys) == id && !strings.Contains(actual.action, expected.action) {
					t.Errorf("%s README action for keys %q = %q, want it to include %q", group.title, actual.keys, actual.action, expected.action)
				}
			}
		}
	}

	if !slices.Contains(keys.playback.VolumeUp.Keys(), "=") {
		t.Fatal("keymap volume-up binding is missing the required = alias")
	}
	playbackRows := readmeKeyRows(readmeKeyGroupBody(section, "Playback"))
	if !readmeKeyRowHasAction(playbackRows, readmeKeyTokens(keys.playback.VolumeUp.Keys()), "volume up") {
		t.Error("README does not document = as a volume-up alias")
	}

	if !slices.Contains(keys.global.Viz.Keys(), "v") {
		t.Fatal("keymap no longer binds v to the spectrum")
	}
	globalRows := readmeKeyRows(readmeKeyGroupBody(section, "Navigation and global"))
	if !readmeKeyRowHasAction(globalRows, readmeKeyTokens(keys.global.Viz.Keys()), "Linux with PipeWire") {
		t.Error("README does not document v as Linux with PipeWire")
	}
	if !slices.Contains(keys.global.Help.Keys(), "?") || keys.global.Help.Help().Desc != "more" {
		t.Fatal("keymap no longer provides the required ? more binding")
	}

	if !slices.Contains(keys.closeHelp.Keys(), "?") || !slices.Contains(keys.closeHelp.Keys(), "q") {
		t.Fatal("keymap no longer closes full help with ? and q")
	}
	const closeHelpNote = "While full help is open, `?`, `esc`, or `q` closes it; `q` does not quit."
	if !strings.Contains(section, closeHelpNote) {
		t.Errorf("README Keys section does not state that ? and q close full help without quitting")
	}
}

// queue-repeat-shuffle R6 [derived]: session.json also carries repeat mode, restored on launch.
func TestREADMEDocumentsRepeatInSession(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(readme), "\n") {
		if strings.Contains(line, "`session.json`") {
			if !strings.Contains(line, "repeat mode") {
				t.Errorf("README session.json paragraph does not mention repeat mode: %q", line)
			}
			return
		}
	}
	t.Fatal("README does not describe session.json")
}

func readmeFilterInputModel(keys keyMap) Model {
	m := Model{keys: keys, focus: focusResults, overlay: overlayNone, results: resultsPane{filter: textinput.New()}}
	m.results.filter.Focus()
	return m
}

func readmeKeysSection(t *testing.T, markdown string) string {
	t.Helper()
	_, after, ok := strings.Cut(markdown, "## Keys\n")
	if !ok {
		t.Fatal("README has no Keys section")
	}
	section := after
	if before, _, ok := strings.Cut(section, "\n## "); ok {
		return before
	}
	t.Fatal("README Keys section has no following level-two section")
	return ""
}

func readmeKeyGroupTitles(section string) []string {
	var titles []string
	for line := range strings.SplitSeq(section, "\n") {
		if after, ok := strings.CutPrefix(line, "### "); ok {
			titles = append(titles, after)
		}
	}
	return titles
}

func readmeKeyGroupBody(section, title string) string {
	heading := "### " + title + "\n"
	_, after, ok := strings.Cut(section, heading)
	if !ok {
		return ""
	}
	body := after
	if before, _, ok := strings.Cut(body, "\n### "); ok {
		return before
	}
	return body
}

func readmeFullHelpRows(keymap readmeFullHelpMap) []readmeKeyRow {
	var rows []readmeKeyRow
	for _, column := range keymap.FullHelp() {
		for _, binding := range column {
			rows = append(rows, readmeRowForBinding(binding))
		}
	}
	return rows
}

func readmeRowForBinding(binding key.Binding) readmeKeyRow {
	return readmeKeyRow{keys: readmeKeyTokens(binding.Keys()), action: binding.Help().Desc}
}

func readmeKeyTokens(keys []string) []string {
	displayNames := map[string]string{
		"up":         "↑",
		"down":       "↓",
		"left":       "←",
		"right":      "→",
		"shift+up":   "shift+↑",
		"shift+down": "shift+↓",
	}
	tokens := make([]string, len(keys))
	for i, key := range keys {
		if display, ok := displayNames[key]; ok {
			key = display
		}
		tokens[i] = key
	}
	return tokens
}

func readmeKeyRows(markdown string) []readmeKeyRow {
	var rows []readmeKeyRow
	for line := range strings.SplitSeq(markdown, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		columns := strings.Split(line, "|")
		if len(columns) < 4 {
			continue
		}
		keys := readmeInlineCode(columns[1])
		if len(keys) == 0 {
			continue
		}
		rows = append(rows, readmeKeyRow{keys: keys, action: strings.TrimSpace(columns[2])})
	}
	return rows
}

func readmeInlineCode(text string) []string {
	parts := strings.Split(text, "`")
	var values []string
	for i := 1; i < len(parts); i += 2 {
		values = append(values, parts[i])
	}
	return values
}

func readmeKeyRowID(keys []string) string {
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	return strings.Join(keys, "\x00")
}

func readmeKeyRowIDs(rows []readmeKeyRow) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = readmeKeyRowID(row.keys)
	}
	sort.Strings(ids)
	return ids
}

func readmeKeyRowHasAction(rows []readmeKeyRow, keys []string, action string) bool {
	id := readmeKeyRowID(keys)
	for _, row := range rows {
		if readmeKeyRowID(row.keys) == id && strings.Contains(row.action, action) {
			return true
		}
	}
	return false
}

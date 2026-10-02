package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

func urls(entries []domain.HistoryEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Track.URL
	}
	return out
}

func TestRecordKeepsNewestFirstWithoutDuplicates(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for i, url := range []string{"a", "b", "a", "c"} {
		if err := store.Record(domain.Track{Title: url, URL: url}, at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Record(domain.Track{Title: "blank"}, at); err == nil {
		t.Error("Record() without URL: error = nil")
	}

	loaded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := loaded.Entries()
	if want := []string{"c", "a", "b"}; !slices.Equal(urls(entries), want) {
		t.Fatalf("history = %q, want %q", urls(entries), want)
	}
	if !entries[1].PlayedAt.Equal(at.Add(2 * time.Minute)) {
		t.Errorf("replayed track kept its old time %v", entries[1].PlayedAt)
	}
	info, err := os.Stat(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("history file mode = %o", info.Mode().Perm())
	}

	if err := loaded.Remove(1); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Remove(2); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove() out of range = %v, want ErrNotFound", err)
	}
	if want := []string{"c", "b"}; !slices.Equal(urls(loaded.Entries()), want) {
		t.Errorf("after Remove() = %q, want %q", urls(loaded.Entries()), want)
	}
}

func TestRecordDropsOldestPastLimit(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxEntries + 3 {
		if err := store.Record(domain.Track{URL: fmt.Sprint(i)}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	entries := store.Entries()
	if len(entries) != MaxEntries {
		t.Fatalf("len(history) = %d, want %d", len(entries), MaxEntries)
	}
	if first, last := entries[0].Track.URL, entries[len(entries)-1].Track.URL; first != fmt.Sprint(MaxEntries+2) || last != "3" {
		t.Errorf("history spans %s..%s, want newest %d down to 3", first, last, MaxEntries+2)
	}
}

func TestOpenRejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(`{"version":9,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open() of an unknown version: error = nil")
	}
}

func TestOpenReadsAndWritesDocumentedFormat(t *testing.T) {
	dir := t.TempDir()
	const doc = `{"version":1,"entries":[{"track":{"ID":"abc","Title":"Song","Channel":"Artist","URL":"https://www.youtube.com/watch?v=abc","Duration":0,"Live":false},"played_at":"2026-01-02T03:04:05Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Entries()
	if len(got) != 1 || got[0].Track.Title != "Song" || !got[0].PlayedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("Entries() = %+v", got)
	}
	if err := store.Record(domain.Track{ID: "def", URL: "https://www.youtube.com/watch?v=def"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"version"`, `"entries"`, `"track"`, `"played_at"`, `"ID"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("written file lacks key %s:\n%s", key, raw)
		}
	}
}

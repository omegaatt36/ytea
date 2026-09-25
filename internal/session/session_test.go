package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	want := State{
		Version: version,
		URLs:    []string{"https://www.youtube.com/watch?v=a", "https://www.youtube.com/watch?v=b"},
		Index:   1,
		Volume:  75,
		Metadata: map[string]Metadata{
			"https://www.youtube.com/watch?v=b": {ID: "b", Title: "Unplayed song", Channel: "Artist"},
		},
	}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %#v, want %#v", got, want)
	}
	info, err := os.Stat(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("session mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOldSessionWithoutMetadata(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"version":1,"urls":["https://www.youtube.com/watch?v=a"],"index":0,"volume":50}`)
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || len(got.URLs) != 1 || got.Metadata != nil {
		t.Fatalf("old session = %+v, err = %v", got, err)
	}
}

func TestSaveEmptyQueueReplacesPreviousSession(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, State{URLs: []string{"song"}, Index: 0, Volume: 80}); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, State{Index: -1, Volume: 80}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.URLs) != 0 || got.Index != -1 || len(got.Metadata) != 0 {
		t.Errorf("session after clear = %+v, want empty queue", got)
	}
}

func TestLoadMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	got, err := Load(dir)
	if err != nil || got.Version != 0 {
		t.Fatalf("missing session: state = %#v, err = %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(`{"version":1,"urls":["x"],"index":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "index") {
		t.Errorf("invalid session error = %v, want index error", err)
	}
}

package playlistignore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testFilename = "youtube-ignored-playlists.json"

func TestMigration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config", "ytea")
	s, err := Open(dir, nil)
	if err != nil || len(s.IgnoredYouTubePlaylists()) != 0 {
		t.Fatalf("empty open: %v, %v", s, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("empty open created directory: %v", err)
	}
	legacy := []string{"interview", "練字"}
	s, err = Open(dir, legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy[0] = "changed"
	if !slices.Equal(s.IgnoredYouTubePlaylists(), []string{"interview", "練字"}) {
		t.Fatal("migration did not copy exact names")
	}
	reopened, err := Open(dir, nil)
	if err != nil || !slices.Equal(reopened.IgnoredYouTubePlaylists(), s.IgnoredYouTubePlaylists()) {
		t.Fatalf("migration not persisted: %v", err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, testFilename): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v", path, info.Mode())
		}
	}
}

func TestExistingFileWins(t *testing.T) {
	for _, content := range []string{"[]", `["restored other"]`} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, testFilename)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(dir, []string{"legacy"})
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(s.IgnoredYouTubePlaylists(), "legacy") {
				t.Fatal("legacy overrode config")
			}
			data, _ := os.ReadFile(path)
			if string(data) != content {
				t.Fatal("existing file rewritten")
			}
		})
	}
}

func TestInvalidFilePreserved(t *testing.T) {
	for _, content := range []string{"", "null", "{}", `["name",null]`, `["ok"] []`, `["ok"] garbage`, strings.Repeat(" ", (8<<20)+1)} {
		t.Run(content[:min(len(content), 30)], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, testFilename)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir, []string{"legacy"}); err == nil {
				t.Fatal("invalid config accepted")
			}
			data, _ := os.ReadFile(path)
			if string(data) != content {
				t.Fatal("invalid config overwritten")
			}
		})
	}
}

func TestPersistenceCopyAndDedupe(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{" interview ", "練字", " interview "} {
		if err := s.IgnoreYouTubePlaylist(name); err != nil {
			t.Fatal(err)
		}
	}
	names := s.IgnoredYouTubePlaylists()
	names[0] = "changed"
	reopened, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{" interview ", "練字"}
	if !slices.Equal(s.IgnoredYouTubePlaylists(), want) || !slices.Equal(reopened.IgnoredYouTubePlaylists(), want) {
		t.Fatal("copy, exact names, or deduplication failed")
	}
}

func TestFailedWriteDoesNotCommit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, testFilename)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.IgnoreYouTubePlaylist("new"); err == nil {
		t.Fatal("rename failure accepted")
	}
	if !slices.Equal(s.IgnoredYouTubePlaylists(), []string{"old"}) {
		t.Fatal("failed write committed")
	}
	if err := s.IgnoreYouTubePlaylist("old"); err != nil {
		t.Fatalf("duplicate attempted write: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
}

func TestEmptyDirRejected(t *testing.T) {
	if _, err := Open("", nil); err == nil {
		t.Fatal("empty config directory accepted")
	}
}

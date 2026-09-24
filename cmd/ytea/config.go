package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/urfave/cli/v3"
)

var cookieKeys = []string{"cookies", "cookies-from-browser"}

var notConfigurable = []string{"config", "help"}

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(base, appName), nil
}

// loadConfig fills every flag that neither the command line nor the
// environment set from the TOML file at path. Keys are flag names, so values
// go through the same parsing as flags. A missing file is only an error when
// the user named it.
func loadConfig(cmd *cli.Command, path string, required bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	known := flagNames(cmd)
	var cookieSources int
	for _, key := range cookieKeys {
		if _, ok := values[key]; ok {
			cookieSources++
		}
	}
	if cookieSources > 1 {
		return fmt.Errorf("config %s: set only one of %s", path, strings.Join(cookieKeys, ", "))
	}
	cookiesOverridden := slices.ContainsFunc(cookieKeys, cmd.IsSet)

	for _, key := range slices.Sorted(maps.Keys(values)) {
		if !known[key] || slices.Contains(notConfigurable, key) {
			return fmt.Errorf("config %s: unknown key %q", path, key)
		}
		if cmd.IsSet(key) || (cookiesOverridden && slices.Contains(cookieKeys, key)) {
			continue
		}
		value, err := scalar(values[key])
		if err != nil {
			return fmt.Errorf("config %s: key %q: %w", path, key, err)
		}
		if key == "cookies" {
			value = expandHome(value)
			if !filepath.IsAbs(value) {
				value = filepath.Join(filepath.Dir(path), value)
			}
		}
		if err := cmd.Set(key, value); err != nil {
			return fmt.Errorf("config %s: key %q: %w", path, key, err)
		}
	}
	return nil
}

func flagNames(cmd *cli.Command) map[string]bool {
	flags := slices.Clone(cmd.Flags)
	for _, grp := range cmd.MutuallyExclusiveFlags {
		for _, alt := range grp.Flags {
			flags = append(flags, alt...)
		}
	}
	names := make(map[string]bool)
	for _, f := range flags {
		for _, name := range f.Names() {
			names[name] = true
		}
	}
	return names
}

func scalar(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case bool, int64, float64:
		return fmt.Sprint(v), nil
	default:
		return "", fmt.Errorf("want a string, number or boolean, got %T", v)
	}
}

func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}

func cookiesFile(configured string) (string, error) {
	if configured == "" {
		dir, err := configDir()
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, "cookies.txt")
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		configured = path
	}

	path, err := filepath.Abs(expandHome(configured))
	if err != nil {
		return "", fmt.Errorf("resolve cookies path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read cookies file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		slog.Warn("cookies file is readable by other users; chmod 600 it", "path", path, "mode", info.Mode().Perm())
	}
	return path, nil
}

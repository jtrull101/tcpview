package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the optional JSON config file's shape - every field is
// optional, and only ever supplies a default: an explicit CLI flag always
// wins over whatever's in the config file (see mergeArchiveConfig).
type Config struct {
	ArchiveDir    string  `json:"archive_dir,omitempty"`
	ArchiveCapGiB float64 `json:"archive_cap_gib,omitempty"`
	NoArchive     *bool   `json:"no_archive,omitempty"`
}

func defaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "tcpview", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tcpview", "config.json")
}

// loadConfig reads and parses the config file at path. A missing file is
// not an error - it just means "no config file", same as an empty one.
func loadConfig(path string) (Config, error) {
	if path == "" {
		return Config{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return c, nil
}

// mergeArchiveConfig resolves the final archiveConfig from three layers,
// highest precedence first: explicit CLI flags (tracked via explicitFlags,
// as populated by flag.FlagSet.Visit - it only visits flags the user
// actually passed), then the config file, then flagDefaults (whatever the
// flags resolved to otherwise, i.e. their built-in defaults).
//
// Pure and side-effect-free on purpose, so precedence logic is testable
// without actually parsing flags or touching a real config file.
func mergeArchiveConfig(flagDefaults archiveConfig, explicitFlags map[string]bool, cfg Config) archiveConfig {
	ac := flagDefaults

	if !explicitFlags["archive-dir"] && cfg.ArchiveDir != "" {
		ac.dir = cfg.ArchiveDir
	}
	if !explicitFlags["archive-cap-gib"] && cfg.ArchiveCapGiB > 0 {
		ac.capGiB = cfg.ArchiveCapGiB
	}
	if !explicitFlags["no-archive"] && cfg.NoArchive != nil {
		ac.disabled = *cfg.NoArchive
	}

	return ac
}

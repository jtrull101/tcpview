package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// --- positive ---

func TestLoadConfig_ParsesAllFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"archive_dir": "/tmp/foo", "archive_cap_gib": 2.5, "no_archive": true}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig error: %v", err)
	}
	if cfg.ArchiveDir != "/tmp/foo" {
		t.Fatalf("ArchiveDir = %q, want /tmp/foo", cfg.ArchiveDir)
	}
	if cfg.ArchiveCapGiB != 2.5 {
		t.Fatalf("ArchiveCapGiB = %v, want 2.5", cfg.ArchiveCapGiB)
	}
	if cfg.NoArchive == nil || !*cfg.NoArchive {
		t.Fatalf("NoArchive = %v, want true", cfg.NoArchive)
	}
}

func TestMergeArchiveConfig_ConfigFileFillsUnsetFlags(t *testing.T) {
	t.Parallel()
	defaults := archiveConfig{dir: "default-dir", capGiB: 1.0, disabled: false}
	cfg := Config{ArchiveDir: "config-dir", ArchiveCapGiB: 5.0, NoArchive: boolPtr(true)}

	got := mergeArchiveConfig(defaults, map[string]bool{}, cfg)
	want := archiveConfig{dir: "config-dir", capGiB: 5.0, disabled: true}
	if got != want {
		t.Fatalf("mergeArchiveConfig() = %+v, want %+v", got, want)
	}
}

func TestMergeArchiveConfig_ExplicitFlagsOverrideConfigFile(t *testing.T) {
	t.Parallel()
	defaults := archiveConfig{dir: "flag-dir", capGiB: 3.0, disabled: true}
	cfg := Config{ArchiveDir: "config-dir", ArchiveCapGiB: 5.0, NoArchive: boolPtr(false)}
	explicit := map[string]bool{"archive-dir": true, "archive-cap-gib": true, "no-archive": true}

	got := mergeArchiveConfig(defaults, explicit, cfg)
	if got != defaults {
		t.Fatalf("mergeArchiveConfig() = %+v, want unchanged flag defaults %+v (all explicit)", got, defaults)
	}
}

// --- negative ---

func TestLoadConfig_MissingFileReturnsZeroValueNoError(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("loadConfig error: %v", err)
	}
	if cfg != (Config{}) {
		t.Fatalf("loadConfig on a missing file = %+v, want zero value", cfg)
	}
}

func TestLoadConfig_EmptyPathReturnsZeroValueNoError(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig(\"\") error: %v", err)
	}
	if cfg != (Config{}) {
		t.Fatalf("loadConfig(\"\") = %+v, want zero value", cfg)
	}
}

func TestLoadConfig_MalformedJSONErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig on malformed JSON returned no error")
	}
}

func TestLoadConfig_UnreadableFileErrors(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows doesn't enforce Unix permission bits, so chmod can't make this fail")
	}
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte("{}"), 0o000); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig on an unreadable file returned no error")
	}
}

func TestMergeArchiveConfig_ZeroConfigLeavesDefaultsUnchanged(t *testing.T) {
	t.Parallel()
	defaults := archiveConfig{dir: "d", capGiB: 1.0, disabled: false}
	got := mergeArchiveConfig(defaults, map[string]bool{}, Config{})
	if got != defaults {
		t.Fatalf("mergeArchiveConfig() = %+v, want unchanged %+v for a zero-value Config", got, defaults)
	}
}

// --- load ---

func TestLoadConfig_Load_ManyParsesFast(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"archive_dir":"/x","archive_cap_gib":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	for i := 0; i < 50_000; i++ {
		if _, err := loadConfig(path); err != nil {
			t.Fatalf("iteration %d: loadConfig error: %v", i, err)
		}
	}
}

// --- stress ---

func TestMergeArchiveConfig_Stress_ExhaustiveExplicitCombinations(t *testing.T) {
	t.Parallel()
	defaults := archiveConfig{dir: "flag", capGiB: 1.0, disabled: false}
	cfg := Config{ArchiveDir: "cfg", ArchiveCapGiB: 9.0, NoArchive: boolPtr(true)}

	flags := []string{"archive-dir", "archive-cap-gib", "no-archive"}
	for mask := 0; mask < 1<<len(flags); mask++ {
		explicit := map[string]bool{}
		for i, name := range flags {
			if mask&(1<<i) != 0 {
				explicit[name] = true
			}
		}
		got := mergeArchiveConfig(defaults, explicit, cfg)

		wantDir := cfg.ArchiveDir
		if explicit["archive-dir"] {
			wantDir = defaults.dir
		}
		wantCap := cfg.ArchiveCapGiB
		if explicit["archive-cap-gib"] {
			wantCap = defaults.capGiB
		}
		wantDisabled := *cfg.NoArchive
		if explicit["no-archive"] {
			wantDisabled = defaults.disabled
		}

		if got.dir != wantDir || got.capGiB != wantCap || got.disabled != wantDisabled {
			t.Fatalf("mask=%03b: mergeArchiveConfig() = %+v, want dir=%q cap=%v disabled=%v", mask, got, wantDir, wantCap, wantDisabled)
		}
	}
}

// --- chaos ---

func TestLoadConfig_Chaos_RandomBytesNeverPanic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 300; i++ {
		data := make([]byte, rng.Intn(200))
		rng.Read(data)
		path := filepath.Join(t.TempDir(), "chaos.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("loadConfig panicked on iteration %d: %v", i, rec)
				}
			}()
			_, _ = loadConfig(path)
		}()
	}
}

func TestMergeArchiveConfig_Chaos_NilNoArchivePointerNeverPanics(t *testing.T) {
	t.Parallel()
	defaults := archiveConfig{dir: "d", capGiB: 1.0}
	cfg := Config{ArchiveDir: "c", ArchiveCapGiB: 2.0, NoArchive: nil}
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("mergeArchiveConfig panicked with a nil NoArchive: %v", rec)
			}
		}()
		_ = mergeArchiveConfig(defaults, map[string]bool{}, cfg)
	}()
}

// Not parallel: t.Setenv changes process-wide environment, which Go
// refuses to combine with t.Parallel.
func TestDefaultConfigPathFor_WindowsUsesAppData(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", `C:\Users\me\AppData\Roaming`) // what os.UserConfigDir reads on Windows
	got := defaultConfigPathFor("windows")
	if !strings.HasSuffix(got, filepath.Join("tcpview", "config.json")) {
		t.Fatalf("defaultConfigPathFor(windows) = %q, want .../tcpview/config.json", got)
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(got, `C:\Users\me\AppData\Roaming`) {
		t.Fatalf("defaultConfigPathFor(windows) = %q, want it under %%AppData%%", got)
	}
}

// Not parallel: see above.
func TestDefaultConfigPathFor_XDGWinsEverywhere(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if got := defaultConfigPathFor(goos); got != filepath.Join(dir, "tcpview", "config.json") {
			t.Errorf("defaultConfigPathFor(%s) = %q, want it under $XDG_CONFIG_HOME", goos, got)
		}
	}
}

// Not parallel: see above.
func TestDefaultConfigPathFor_UnixUsesDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got := defaultConfigPathFor(goos); got != filepath.Join(home, ".config", "tcpview", "config.json") {
			t.Errorf("defaultConfigPathFor(%s) = %q, want ~/.config/tcpview/config.json", goos, got)
		}
	}
}

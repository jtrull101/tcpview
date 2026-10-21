package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/remote"
	"github.com/jtrull101/tcpview/internal/store"
)

// noopSource is a capture.Source double that never produces anything and
// closes instantly - stands in for newRemoteSource in tests so runRemote's
// wiring is exercised without spawning a real ssh subprocess.
type noopSource struct {
	packets chan store.Packet
	errs    chan error
}

func newNoopSource() *noopSource {
	return &noopSource{packets: make(chan store.Packet), errs: make(chan error)}
}
func (n *noopSource) Packets() <-chan store.Packet { return n.packets }
func (n *noopSource) Errors() <-chan error         { return n.errs }
func (n *noopSource) Close() error                 { return nil }

var _ capture.Source = (*noopSource)(nil)

// withStubs swaps every OS/network-touching seam in main.go for the
// duration of fn, restoring them afterward. Serialized via a mutex since
// these are package-level vars and tests run in parallel.
var stubMu sync.Mutex

func withStubs(t *testing.T, teaErr error, privErr error, fn func(captured *tea.Model)) {
	t.Helper()
	stubMu.Lock()
	defer stubMu.Unlock()

	origTea, origPriv, origRemote, origList, origArchive := runTeaProgram, checkCapturePrivilegesFn, newRemoteSource, listRemoteInterfaces, newArchiveFn
	defer func() {
		runTeaProgram, checkCapturePrivilegesFn, newRemoteSource, listRemoteInterfaces, newArchiveFn = origTea, origPriv, origRemote, origList, origArchive
	}()

	var captured tea.Model
	runTeaProgram = func(m tea.Model) error {
		captured = m
		return teaErr
	}
	checkCapturePrivilegesFn = func() error { return privErr }
	newRemoteSource = func(remote.Options) (capture.Source, error) { return newNoopSource(), nil }
	listRemoteInterfaces = func(remote.Options) ([]capture.Interface, error) {
		return []capture.Interface{{Name: "eth0"}, {Name: "eth1"}}, nil
	}
	// Never let a test touch the real default archive dir (~/.cache/...) -
	// redirect to a throwaway temp dir instead.
	archiveDir := t.TempDir()
	newArchiveFn = func(opts store.ArchiveOptions) (*store.Archive, error) {
		opts.Dir = archiveDir
		return store.NewArchive(opts)
	}

	fn(&captured)
}

func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// --- positive ---

func TestRun_LocalMode_ReachesTeaProgram(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		code := run(nil, devNull(t))
		if code != 0 {
			t.Fatalf("run() = %d, want 0", code)
		}
		if *captured == nil {
			t.Fatal("runTeaProgram was never called")
		}
	})
}

func TestRun_RemoteMode_ReachesTeaProgram(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		code := run([]string{"-remote", "fake-host", "-remote-iface", "eth0"}, devNull(t))
		if code != 0 {
			t.Fatalf("run() = %d, want 0", code)
		}
		if *captured == nil {
			t.Fatal("runTeaProgram was never called")
		}
	})
}

func TestRun_RemoteSourceErrorSkipsTea(t *testing.T) {
	t.Parallel()
	stubMu.Lock()
	defer stubMu.Unlock()
	origTea, origRemote, origList := runTeaProgram, newRemoteSource, listRemoteInterfaces
	defer func() { runTeaProgram, newRemoteSource, listRemoteInterfaces = origTea, origRemote, origList }()

	called := false
	runTeaProgram = func(tea.Model) error { called = true; return nil }
	listRemoteInterfaces = func(remote.Options) ([]capture.Interface, error) { return nil, nil }
	newRemoteSource = func(remote.Options) (capture.Source, error) {
		return nil, fmt.Errorf("ssh: connection refused")
	}

	code := run([]string{"-remote", "fake-host", "-remote-iface", "eth0"}, devNull(t))
	if code != 1 {
		t.Fatalf("run() = %d, want 1 when newRemoteSource errors", code)
	}
	if called {
		t.Fatal("runTeaProgram was called despite a remote source open error")
	}
}

func TestRun_NoArchiveFlagSkipsArchiveSetup(t *testing.T) {
	t.Parallel()
	stubMu.Lock()
	defer stubMu.Unlock()
	origTea, origPriv, origArchive := runTeaProgram, checkCapturePrivilegesFn, newArchiveFn
	defer func() { runTeaProgram, checkCapturePrivilegesFn, newArchiveFn = origTea, origPriv, origArchive }()

	checkCapturePrivilegesFn = func() error { return nil }
	archiveCalled := false
	newArchiveFn = func(store.ArchiveOptions) (*store.Archive, error) {
		archiveCalled = true
		return nil, nil
	}
	runTeaProgram = func(tea.Model) error { return nil }

	code := run([]string{"-no-archive"}, devNull(t))
	if code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if archiveCalled {
		t.Fatal("newArchiveFn was called despite -no-archive")
	}
}

func TestRun_RemoteWithoutIfaceShowsRemotePicker(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		var opened []string
		newRemoteSource = func(opts remote.Options) (capture.Source, error) {
			opened = append(opened, opts.Host+"/"+opts.Iface)
			return newNoopSource(), nil
		}
		code := run([]string{"-remote", "fake-host"}, devNull(t))
		if code != 0 {
			t.Fatalf("run() = %d, want 0", code)
		}
		if *captured == nil {
			t.Fatal("runTeaProgram was never called")
		}
		if len(opened) != 0 {
			t.Fatalf("opened %v before the user picked anything", opened)
		}
		out := (*captured).View()
		if !strings.Contains(out, "choose an interface on fake-host") || !strings.Contains(out, "eth1") {
			t.Fatalf("View() = %q, want the remote host's interface picker", out)
		}

		// Picking one opens it on the remote host.
		next, cmd := (*captured).Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter in the remote picker returned no open cmd")
		}
		next, _ = next.Update(cmd())
		if len(opened) != 1 || opened[0] != "fake-host/eth0" {
			t.Fatalf("opened %v, want [fake-host/eth0]", opened)
		}
		if out := next.View(); !strings.Contains(out, "fake-host (eth0) [1/2") {
			t.Fatalf("View() after pick = %q, want the host-qualified label with switch position", out)
		}
	})
}

func TestRun_RemoteWithIfaceEnablesSwitching(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		code := run([]string{"-remote", "fake-host", "-remote-iface", "eth1"}, devNull(t))
		if code != 0 {
			t.Fatalf("run() = %d, want 0", code)
		}
		if out := (*captured).View(); !strings.Contains(out, "fake-host (eth1) [2/2, tab/shift+tab") {
			t.Fatalf("View() = %q, want switching positioned on eth1", out)
		}
	})
}

// --- negative ---

func TestRun_ArchiveSetupErrorSkipsTea(t *testing.T) {
	t.Parallel()
	stubMu.Lock()
	defer stubMu.Unlock()
	origTea, origPriv, origArchive := runTeaProgram, checkCapturePrivilegesFn, newArchiveFn
	defer func() { runTeaProgram, checkCapturePrivilegesFn, newArchiveFn = origTea, origPriv, origArchive }()

	checkCapturePrivilegesFn = func() error { return nil }
	called := false
	runTeaProgram = func(tea.Model) error { called = true; return nil }
	newArchiveFn = func(store.ArchiveOptions) (*store.Archive, error) {
		return nil, fmt.Errorf("disk full")
	}

	code := run(nil, devNull(t))
	if code != 1 {
		t.Fatalf("run() = %d, want 1 on an archive setup error", code)
	}
	if called {
		t.Fatal("runTeaProgram was called despite an archive setup error")
	}
}

func TestRun_NonPositiveArchiveCapFailsFast(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		for _, capFlag := range []string{"0", "-1"} {
			code := run([]string{"-archive-cap-gib", capFlag}, devNull(t))
			if code != 2 {
				t.Fatalf("run(-archive-cap-gib %s) = %d, want 2", capFlag, code)
			}
		}
		if *captured != nil {
			t.Fatal("runTeaProgram was called despite a non-positive archive cap")
		}
	})
}

func TestRun_UnknownFlagFailsFast(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		code := run([]string{"-does-not-exist"}, devNull(t))
		if code != 2 {
			t.Fatalf("run() = %d, want 2 for an unknown flag", code)
		}
		if *captured != nil {
			t.Fatal("runTeaProgram was called despite a flag parse error")
		}
	})
}

func TestRun_RemoteListFailureWithoutIfaceFailsFast(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		listRemoteInterfaces = func(remote.Options) ([]capture.Interface, error) {
			return nil, fmt.Errorf("tcpdump: command not found")
		}
		code := run([]string{"-remote", "fake-host"}, devNull(t))
		if code != 1 {
			t.Fatalf("run() = %d, want 1 when the remote list fails and no -remote-iface was given", code)
		}
		if *captured != nil {
			t.Fatal("runTeaProgram was called with nothing to show")
		}
	})
}

func TestRun_RemoteListFailureWithIfaceStillCaptures(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		listRemoteInterfaces = func(remote.Options) ([]capture.Interface, error) {
			return nil, fmt.Errorf("tcpdump -D: permission denied")
		}
		code := run([]string{"-remote", "fake-host", "-remote-iface", "eth0"}, devNull(t))
		if code != 0 {
			t.Fatalf("run() = %d, want 0 - a known iface doesn't need the list", code)
		}
		out := (*captured).View()
		if strings.Contains(out, "tab/shift+tab") {
			t.Fatalf("View() = %q, offers switching without an interface list", out)
		}
		if !strings.Contains(out, "interface switching unavailable: tcpdump -D: permission denied") {
			t.Fatalf("View() = %q, want it to say why switching is off", out)
		}
	})
}

func TestRun_LocalMode_PrivilegeCheckFailureSkipsTea(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, fmt.Errorf("permission denied"), func(captured *tea.Model) {
		code := run(nil, devNull(t))
		if code != 1 {
			t.Fatalf("run() = %d, want 1 on a privilege check failure", code)
		}
		if *captured != nil {
			t.Fatal("runTeaProgram was called despite a privilege check failure")
		}
	})
}

func TestRun_TeaProgramErrorPropagates(t *testing.T) {
	t.Parallel()
	withStubs(t, fmt.Errorf("terminal error"), nil, func(captured *tea.Model) {
		code := run(nil, devNull(t))
		if code != 1 {
			t.Fatalf("run() = %d, want 1 when the tea program itself errors", code)
		}
	})
}

// --- chaos ---

func TestRun_RepeatedInvocationsDoNotLeakOrPanic(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(_ *tea.Model) {
		for i := 0; i < 50; i++ {
			args := [][]string{
				nil,
				{"-remote", "h", "-remote-iface", "eth0"},
				{"-remote", "h"},
				{"-bogus"},
			}[i%4]
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("run panicked on iteration %d (%v): %v", i, args, rec)
					}
				}()
				_ = run(args, devNull(t))
			}()
		}
	})
}

func TestRun_BadConfigFileFailsFast(t *testing.T) {
	t.Parallel()
	stubMu.Lock()
	defer stubMu.Unlock()
	origTea, origPriv := runTeaProgram, checkCapturePrivilegesFn
	defer func() { runTeaProgram, checkCapturePrivilegesFn = origTea, origPriv }()

	checkCapturePrivilegesFn = func() error { return nil }
	called := false
	runTeaProgram = func(tea.Model) error { called = true; return nil }

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	code := run([]string{"-config", path}, devNull(t))
	if code != 1 {
		t.Fatalf("run() = %d, want 1 for a malformed config file", code)
	}
	if called {
		t.Fatal("runTeaProgram was called despite a malformed config file")
	}
}

func TestRun_ConfigFileSuppliesArchiveCapWhenFlagNotSet(t *testing.T) {
	t.Parallel()
	withStubs(t, nil, nil, func(captured *tea.Model) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{"archive_cap_gib": 3.5}`), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		code := run([]string{"-config", path}, devNull(t))
		if code != 0 {
			t.Fatalf("run() = %d, want 0", code)
		}
		if *captured == nil {
			t.Fatal("runTeaProgram was never called")
		}
	})
}

func TestRun_VersionFlagPrintsAndExitsWithoutTea(t *testing.T) {
	t.Parallel()
	stubMu.Lock()
	defer stubMu.Unlock()
	origTea := runTeaProgram
	defer func() { runTeaProgram = origTea }()

	called := false
	runTeaProgram = func(tea.Model) error { called = true; return nil }

	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("CreateTemp error: %v", err)
	}
	defer f.Close()

	code := run([]string{"-version"}, f)
	if code != 0 {
		t.Fatalf("run(-version) = %d, want 0", code)
	}
	if called {
		t.Fatal("runTeaProgram was called despite -version")
	}

	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(out), "tcpview") {
		t.Fatalf("output = %q, want it to mention tcpview", out)
	}
}

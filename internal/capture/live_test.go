package capture

import (
	"strings"
	"testing"
	"time"
)

// Live's actual capture loop needs a real pcap handle opened with
// CAP_NET_RAW/root, which a normal unprivileged test run doesn't have (see
// CLAUDE.md's testing policy carve-out for the OS/network boundary). What's
// unit-testable without privilege is covered here; the full open -> capture
// -> decode -> close lifecycle against a real interface is exercised
// manually per the plan's verification section.

// --- positive ---

func TestOptions_WithDefaults_FillsZeroValues(t *testing.T) {
	t.Parallel()
	o := Options{Iface: "eth0"}.withDefaults()
	if o.SnapLen <= 0 {
		t.Fatalf("SnapLen = %d, want > 0 after defaults applied", o.SnapLen)
	}
	if o.Timeout <= 0 {
		t.Fatalf("Timeout = %v, want > 0 after defaults applied", o.Timeout)
	}
}

func TestOptions_WithDefaults_PreservesExplicitValues(t *testing.T) {
	t.Parallel()
	o := Options{Iface: "eth0", SnapLen: 1500, Timeout: 5 * time.Second}.withDefaults()
	if o.SnapLen != 1500 {
		t.Fatalf("SnapLen = %d, want preserved 1500", o.SnapLen)
	}
	if o.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want preserved 5s", o.Timeout)
	}
}

// --- negative ---

func TestNewLive_EmptyInterfaceErrors(t *testing.T) {
	t.Parallel()
	_, err := NewLive(Options{})
	if err == nil {
		t.Fatal("NewLive with empty Iface returned no error")
	}
}

func TestNewLive_NonexistentInterfaceErrors(t *testing.T) {
	t.Parallel()
	_, err := NewLive(Options{Iface: "definitely-does-not-exist-9999"})
	if err == nil {
		t.Fatal("NewLive with a nonexistent interface returned no error")
	}
	if !strings.Contains(err.Error(), "definitely-does-not-exist-9999") {
		t.Fatalf("error %q does not mention the offending interface name", err.Error())
	}
}

// --- chaos ---

func TestNewLive_RepeatedFailuresDoNotLeak(t *testing.T) {
	t.Parallel()
	// Calling a failing open many times in a row shouldn't panic, hang, or
	// otherwise misbehave even though no handle is ever successfully opened.
	for i := 0; i < 50; i++ {
		if _, err := NewLive(Options{Iface: "definitely-does-not-exist-9999"}); err == nil {
			t.Fatalf("iteration %d: expected error, got none", i)
		}
	}
}

package capture

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// listOrSkip calls the real ListInterfaces, skipping only where libpcap
// itself can't exist: a Windows machine without Npcap, such as GitHub's
// Windows runners (Npcap's free edition can't be installed unattended).
// That missing-Npcap path is asserted separately, by
// TestListInterfaces_WindowsWithoutNpcapSaysHowToFix.
func listOrSkip(t *testing.T) []Interface {
	t.Helper()
	ifaces, err := ListInterfaces()
	if errors.Is(err, ErrNpcapMissing) {
		t.Skipf("no Npcap on this Windows machine: %v", err)
	}
	if err != nil {
		t.Fatalf("ListInterfaces() error: %v", err)
	}
	return ifaces
}

// --- positive ---

// ListInterfaces enumerates via libpcap and, on Linux, does not require
// elevated privileges (only actually opening a handle for capture does) -
// so this runs fine as an ordinary unprivileged test. A loopback interface
// is effectively guaranteed to exist on any machine this runs on.
//
// Identified by address (127.0.0.1), not name: Linux calls it "lo", macOS
// calls it "lo0" - a hardcoded name here would be platform-specific in
// exactly the way this test shouldn't be (caught by a real macOS CI run).
func TestListInterfaces_FindsLoopback(t *testing.T) {
	t.Parallel()
	ifaces := listOrSkip(t)
	found := false
	for _, iface := range ifaces {
		for _, addr := range iface.Addresses {
			if addr == "127.0.0.1" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("ListInterfaces() = %+v, expected to find an interface with address 127.0.0.1", ifaces)
	}
}

// --- negative ---

func TestListInterfaces_NeverReturnsNilAddressesSlice(t *testing.T) {
	t.Parallel()
	ifaces := listOrSkip(t)
	for _, iface := range ifaces {
		if iface.Addresses == nil {
			t.Fatalf("interface %q has nil Addresses slice, want non-nil (possibly empty)", iface.Name)
		}
	}
}

// --- chaos ---

func TestListInterfaces_RepeatedCallsAreStable(t *testing.T) {
	t.Parallel()
	first := listOrSkip(t)
	for i := 0; i < 20; i++ {
		again, err := ListInterfaces()
		if err != nil {
			t.Fatalf("ListInterfaces() error on repeat %d: %v", i, err)
		}
		if len(again) != len(first) {
			t.Fatalf("interface count changed between calls: %d vs %d", len(first), len(again))
		}
	}
}

// --- Windows ---

func TestListInterfaces_WindowsWithoutNpcapSaysHowToFix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("Npcap only exists on Windows")
	}
	if _, err := ListInterfaces(); err != nil && !errors.Is(err, ErrNpcapMissing) {
		t.Fatalf("ListInterfaces() error %v isn't ErrNpcapMissing - a missing Npcap would be reported unhelpfully", err)
	}
}

func TestExplainListError_MissingWpcapOnWindowsPointsAtNpcap(t *testing.T) {
	t.Parallel()
	err := explainListError("windows", fmt.Errorf("couldn't load wpcap.dll"))
	if !errors.Is(err, ErrNpcapMissing) || !strings.Contains(err.Error(), "https://npcap.com") {
		t.Fatalf("explainListError = %v, want ErrNpcapMissing with the download link", err)
	}
	if !strings.Contains(err.Error(), "wpcap.dll") {
		t.Fatalf("explainListError = %v, dropped the underlying error", err)
	}
}

func TestExplainListError_OtherFailuresWrappedAsIs(t *testing.T) {
	t.Parallel()
	cause := fmt.Errorf("permission denied")
	for _, goos := range []string{"linux", "darwin", "windows"} {
		err := explainListError(goos, cause)
		if errors.Is(err, ErrNpcapMissing) || !errors.Is(err, cause) {
			t.Errorf("explainListError(%s) = %v, want the cause wrapped without an Npcap hint", goos, err)
		}
	}
	// A wpcap-looking message off Windows isn't a missing Npcap either.
	if err := explainListError("linux", fmt.Errorf("wpcap.dll")); errors.Is(err, ErrNpcapMissing) {
		t.Fatal("non-Windows error mistaken for a missing Npcap")
	}
}

func TestInterface_DisplayName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		iface Interface
		want  string
	}{
		{Interface{Name: `\Device\NPF_{4A1C2B3D-0000-1111-2222-333344445555}`, Description: "Intel(R) Ethernet Connection I219-V"}, "Intel(R) Ethernet Connection I219-V"},
		{Interface{Name: `\Device\NPF_Loopback`, Description: "Adapter for loopback traffic capture"}, "Adapter for loopback traffic capture"},
		{Interface{Name: `\Device\NPF_{4A1C}`}, `\Device\NPF_{4A1C}`}, // no description: the device path is all there is
		{Interface{Name: "eth0", Description: "Intel"}, "eth0"},
		{Interface{Name: "any", Description: "Pseudo-device that captures on all interfaces"}, "any"},
		{Interface{}, ""},
	}
	for _, c := range cases {
		if got := c.iface.DisplayName(); got != c.want {
			t.Errorf("%+v.DisplayName() = %q, want %q", c.iface, got, c.want)
		}
	}
}

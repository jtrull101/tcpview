package capture

import "testing"

// --- positive ---

// ListInterfaces enumerates via libpcap and, on Linux, does not require
// elevated privileges (only actually opening a handle for capture does) -
// so this runs fine as an ordinary unprivileged test. A loopback interface
// is effectively guaranteed to exist on any machine this runs on.
func TestListInterfaces_FindsLoopback(t *testing.T) {
	t.Parallel()
	ifaces, err := ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces() error: %v", err)
	}
	found := false
	for _, iface := range ifaces {
		if iface.Name == "lo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListInterfaces() = %+v, expected to find loopback (lo)", ifaces)
	}
}

// --- negative ---

func TestListInterfaces_NeverReturnsNilAddressesSlice(t *testing.T) {
	t.Parallel()
	ifaces, err := ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces() error: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Addresses == nil {
			t.Fatalf("interface %q has nil Addresses slice, want non-nil (possibly empty)", iface.Name)
		}
	}
}

// --- chaos ---

func TestListInterfaces_RepeatedCallsAreStable(t *testing.T) {
	t.Parallel()
	first, err := ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces() error: %v", err)
	}
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

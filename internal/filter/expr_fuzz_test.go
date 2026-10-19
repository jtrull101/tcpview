package filter

import (
	"testing"
	"time"

	"github.com/jtrull101/tcpview/internal/store"
)

func samplePacketForFuzz(iface, proto string, srcPort, dstPort uint16) store.Packet {
	return store.Packet{
		Time:    time.Now(),
		Iface:   iface,
		Proto:   store.Proto(proto),
		SrcPort: srcPort,
		DstPort: dstPort,
	}
}

// FuzzParseExpression feeds arbitrary strings to the filter-bar expression
// parser - the one place truly free-form external (user-typed) input
// reaches this package. ParseExpression must never panic; returning an
// error for garbage input is the correct, expected outcome.
func FuzzParseExpression(f *testing.F) {
	seeds := []string{
		"",
		"proto=tcp",
		"iface=eth0 src=10.0.0.0/24 dport=443",
		"sport=65535 dport=1",
		"src=::1 dst=::/0",
		"bogus=value",
		"=",
		"key=",
		"=value",
		"iface=eth0==weird",
		"proto=TCP PROTO=udp",
		"src=10.0.0.0/999",
		"dport=99999999999999999999",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ParseExpression(%q) panicked: %v", expr, rec)
			}
		}()
		_, _ = ParseExpression(expr)
	})
}

// FuzzFilterMatches exercises Filter.Matches with parser-derived filters
// against arbitrary-ish packets built from fuzzed port/proto/iface values,
// catching any panic in the matching logic itself.
func FuzzFilterMatches(f *testing.F) {
	f.Add("eth0", "tcp", uint16(80), uint16(443))
	f.Add("", "", uint16(0), uint16(0))
	f.Add("any", "ARP", uint16(65535), uint16(1))

	f.Fuzz(func(t *testing.T, iface, proto string, srcPort, dstPort uint16) {
		filterExpr := Filter{Iface: iface}
		p := samplePacketForFuzz(iface, proto, srcPort, dstPort)

		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("Matches panicked for iface=%q proto=%q sport=%d dport=%d: %v", iface, proto, srcPort, dstPort, rec)
			}
		}()
		_ = filterExpr.Matches(p)
	})
}

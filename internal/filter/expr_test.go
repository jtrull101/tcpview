package filter

import (
	"math/rand"
	"net/netip"
	"strconv"
	"testing"

	"github.com/jtrull101/tcpview/internal/store"
)

// --- positive ---

func TestParseExpression_EmptyStringIsZeroFilter(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "   ", "\t\n"} {
		f, err := ParseExpression(s)
		if err != nil {
			t.Fatalf("ParseExpression(%q) error: %v", s, err)
		}
		if !f.IsZero() {
			t.Fatalf("ParseExpression(%q) = %+v, want zero Filter", s, f)
		}
	}
}

func TestParseExpression_AllKeys(t *testing.T) {
	t.Parallel()
	f, err := ParseExpression("iface=eth0 src=10.0.0.0/24 dst=192.168.1.1 sport=1234 dport=443 proto=tcp")
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}
	if f.Iface != "eth0" {
		t.Fatalf("Iface = %q, want eth0", f.Iface)
	}
	if !f.SrcIP.Contains(netip.MustParseAddr("10.0.0.200")) {
		t.Fatalf("SrcIP = %v, want it to contain 10.0.0.200", f.SrcIP)
	}
	if f.DstIP != netip.PrefixFrom(netip.MustParseAddr("192.168.1.1"), 32) {
		t.Fatalf("DstIP = %v, want a /32 for 192.168.1.1", f.DstIP)
	}
	if f.SrcPort != 1234 || f.DstPort != 443 {
		t.Fatalf("ports = %d/%d, want 1234/443", f.SrcPort, f.DstPort)
	}
	if f.Proto != store.ProtoTCP {
		t.Fatalf("Proto = %q, want TCP", f.Proto)
	}
}

func TestParseExpression_CaseInsensitiveKeysAndProto(t *testing.T) {
	t.Parallel()
	f, err := ParseExpression("IFACE=eth0 PROTO=udp")
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}
	if f.Iface != "eth0" || f.Proto != store.ProtoUDP {
		t.Fatalf("f = %+v, want Iface=eth0 Proto=UDP", f)
	}
}

func TestParseExpression_ExtraWhitespaceIgnored(t *testing.T) {
	t.Parallel()
	f, err := ParseExpression("  iface=eth0    dport=443  ")
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}
	if f.Iface != "eth0" || f.DstPort != 443 {
		t.Fatalf("f = %+v, want Iface=eth0 DstPort=443", f)
	}
}

// --- negative ---

func TestParseExpression_MissingEqualsErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseExpression("eth0"); err == nil {
		t.Fatal("ParseExpression(\"eth0\") returned no error")
	}
}

func TestParseExpression_EmptyValueErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseExpression("iface="); err == nil {
		t.Fatal("ParseExpression(\"iface=\") returned no error")
	}
}

func TestParseExpression_UnknownKeyErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseExpression("bogus=eth0"); err == nil {
		t.Fatal("ParseExpression with an unknown key returned no error")
	}
}

func TestParseExpression_InvalidIPErrors(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"src=not-an-ip", "dst=10.0.0.0/999"} {
		if _, err := ParseExpression(s); err == nil {
			t.Fatalf("ParseExpression(%q) returned no error", s)
		}
	}
}

func TestParseExpression_InvalidPortErrors(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"sport=abc", "dport=99999", "sport=-1", "dport=0"} {
		if _, err := ParseExpression(s); err == nil {
			t.Fatalf("ParseExpression(%q) returned no error", s)
		}
	}
}

// --- load ---

func TestParseExpression_Load_ManyParsesFast(t *testing.T) {
	t.Parallel()
	const n = 100_000
	for i := 0; i < n; i++ {
		if _, err := ParseExpression("iface=eth0 src=10.0.0.0/24 dport=443 proto=tcp"); err != nil {
			t.Fatalf("iteration %d: ParseExpression error: %v", i, err)
		}
	}
}

// --- stress ---

func TestParseExpression_Stress_ManyTermsSameKeyLastWins(t *testing.T) {
	t.Parallel()
	expr := ""
	for i := 1; i <= 200; i++ {
		expr += "dport=" + strconv.Itoa(i) + " "
	}
	f, err := ParseExpression(expr)
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}
	if f.DstPort != 200 {
		t.Fatalf("DstPort = %d, want 200 (last term should win)", f.DstPort)
	}
}

// --- chaos ---

func TestParseExpression_Chaos_RandomStringsNeverPanic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99))
	charset := "abcdefghij=/.: 0123456789\t\n"
	for i := 0; i < 2000; i++ {
		n := rng.Intn(60)
		b := make([]byte, n)
		for j := range b {
			b[j] = charset[rng.Intn(len(charset))]
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("ParseExpression panicked on %q: %v", string(b), rec)
				}
			}()
			_, _ = ParseExpression(string(b))
		}()
	}
}

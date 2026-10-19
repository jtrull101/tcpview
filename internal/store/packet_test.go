package store

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

// --- positive ---

func TestPacket_Valid_PopulatedPacketIsValid(t *testing.T) {
	t.Parallel()
	p := pkt(time.Now(), "x")
	if !p.Valid() {
		t.Fatal("Valid() = false for a populated packet")
	}
}

func TestPacket_String_IncludesAddressesPortsAndProto(t *testing.T) {
	t.Parallel()
	p := pkt(time.Now(), "x")
	s := p.String()
	for _, want := range []string{"10.0.0.1:1234", "10.0.0.2:443", string(ProtoTCP), "len=60"} {
		if !strings.Contains(s, want) {
			t.Fatalf("String() = %q, missing %q", s, want)
		}
	}
}

func TestPacket_String_OmitsPortWhenZero(t *testing.T) {
	t.Parallel()
	p := pkt(time.Now(), "x")
	p.SrcPort = 0
	p.DstPort = 0
	s := p.String()
	if strings.Contains(s, "10.0.0.1:0") || strings.Contains(s, "10.0.0.2:0") {
		t.Fatalf("String() = %q, should omit :0 ports", s)
	}
}

func TestPacket_String_DefaultsEmptyProtoToOther(t *testing.T) {
	t.Parallel()
	p := pkt(time.Now(), "x")
	p.Proto = ""
	s := p.String()
	if !strings.Contains(s, string(ProtoOther)) {
		t.Fatalf("String() = %q, want it to fall back to %q for empty Proto", s, ProtoOther)
	}
}

// --- negative ---

func TestPacket_Valid_ZeroValueIsInvalid(t *testing.T) {
	t.Parallel()
	var p Packet
	if p.Valid() {
		t.Fatal("Valid() = true for a zero-value Packet")
	}
}

func TestPacket_String_HandlesMissingIPs(t *testing.T) {
	t.Parallel()
	p := Packet{Time: time.Now(), Proto: ProtoARP, Length: 28}
	s := p.String()
	if !strings.Contains(s, "? > ?") {
		t.Fatalf("String() = %q, want '?' placeholders for unset IPs", s)
	}
}

// --- chaos ---

func TestPacket_String_DoesNotPanicOnExtremeValues(t *testing.T) {
	t.Parallel()
	cases := []Packet{
		{}, // zero value
		{Time: time.Now(), SrcIP: netip.Addr{}, DstIP: netip.Addr{}, Length: -1},
		{Time: time.Now(), SrcIP: netip.MustParseAddr("::1"), DstIP: netip.MustParseAddr("::1"), SrcPort: 65535, DstPort: 65535, Length: 1 << 20},
	}
	for i, p := range cases {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("String() panicked on case %d: %v", i, rec)
				}
			}()
			_ = p.String()
		}()
	}
}

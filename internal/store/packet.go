// Package store holds the capture-engine-agnostic packet representation and
// the in-memory/on-disk structures that retain it for live viewing and
// rewind. Nothing in this package imports gopacket/pcap or the TUI, so it is
// fully unit-testable against synthetic Packet values.
package store

import (
	"fmt"
	"net/netip"
	"time"
)

// Proto identifies the protocol of a captured packet at the granularity the
// UI and filters care about.
type Proto string

const (
	ProtoTCP    Proto = "TCP"
	ProtoUDP    Proto = "UDP"
	ProtoICMP   Proto = "ICMP"
	ProtoICMPv6 Proto = "ICMPv6"
	ProtoARP    Proto = "ARP"
	ProtoOther  Proto = "OTHER"
)

// Packet is one observed network packet, decoded down to the fields tcpview
// displays and filters on. SrcIP/DstIP are the zero netip.Addr when not
// applicable (e.g. ARP has no IP-layer addresses).
//
// Raw holds the original link-layer frame bytes, when the capture source
// provided them (both capture.Decode and remote's SSH source populate it).
// It's carried along purely so the on-disk archive (archive.go) can write
// real, standard pcap files that other tools can open - nothing in this
// package's own logic depends on it being present, so a Packet built
// without Raw (e.g. in most tests) is still perfectly valid.
type Packet struct {
	Time    time.Time
	Iface   string
	SrcIP   netip.Addr
	DstIP   netip.Addr
	SrcPort uint16
	DstPort uint16
	Proto   Proto
	Length  int
	Summary string
	Raw     []byte

	// LinkType is the pcap LINKTYPE_* value Raw was captured with (1 =
	// Ethernet, 113 = Linux "cooked" capture as used by the "any" device,
	// 0 = BSD loopback, ...), meaningful only when HasLinkType is set:
	// Packets from archives written before this field existed, or built
	// by hand, don't carry one. Export needs it to label each frame
	// correctly, since one capture session can span several link types.
	LinkType    uint16 `json:",omitempty"`
	HasLinkType bool   `json:",omitempty"`
}

// Valid reports whether p looks like a populated Packet rather than a zero
// value. Used defensively at store/archive boundaries to catch bugs early
// instead of silently retaining garbage.
func (p Packet) Valid() bool {
	return !p.Time.IsZero()
}

// String renders a single-line, tcpdump-ish summary, used as a fallback
// display and in tests/log output.
func (p Packet) String() string {
	src, dst := "?", "?"
	if p.SrcIP.IsValid() {
		src = p.SrcIP.String()
		if p.SrcPort != 0 {
			src = fmt.Sprintf("%s:%d", src, p.SrcPort)
		}
	}
	if p.DstIP.IsValid() {
		dst = p.DstIP.String()
		if p.DstPort != 0 {
			dst = fmt.Sprintf("%s:%d", dst, p.DstPort)
		}
	}
	proto := p.Proto
	if proto == "" {
		proto = ProtoOther
	}
	return fmt.Sprintf("%s %s %s > %s len=%d", p.Time.Format(time.RFC3339Nano), proto, src, dst, p.Length)
}

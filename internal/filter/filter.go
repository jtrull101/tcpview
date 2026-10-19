// Package filter decides which captured packets to display. Filtering
// happens at the display layer (see the architecture notes in the project
// plan) rather than at capture time, so rewind can reveal packets that
// matched a filter the user only applied after the fact. Nothing here
// imports gopacket/pcap or the TUI, so it's fully unit-testable.
package filter

import (
	"fmt"
	"net/netip"

	"github.com/jtrull101/tcpview/internal/store"
)

// Filter narrows which packets match. Every field is optional; the zero
// value of each field means "don't constrain on this dimension" - a zero
// Filter matches every packet. Active dimensions are ANDed together.
type Filter struct {
	Iface   string       // exact match, "" = any
	SrcIP   netip.Prefix // CIDR or single address; zero value = any
	DstIP   netip.Prefix
	SrcPort uint16      // 0 = any
	DstPort uint16      // 0 = any
	Proto   store.Proto // "" = any
}

// Matches reports whether p satisfies every active constraint in f.
func (f Filter) Matches(p store.Packet) bool {
	if f.Iface != "" && f.Iface != p.Iface {
		return false
	}
	if f.SrcIP.IsValid() && !f.SrcIP.Contains(p.SrcIP) {
		return false
	}
	if f.DstIP.IsValid() && !f.DstIP.Contains(p.DstIP) {
		return false
	}
	if f.SrcPort != 0 && f.SrcPort != p.SrcPort {
		return false
	}
	if f.DstPort != 0 && f.DstPort != p.DstPort {
		return false
	}
	if f.Proto != "" && f.Proto != p.Proto {
		return false
	}
	return true
}

// IsZero reports whether f has no active constraints, i.e. matches every
// packet.
func (f Filter) IsZero() bool {
	return f.Iface == "" && !f.SrcIP.IsValid() && !f.DstIP.IsValid() &&
		f.SrcPort == 0 && f.DstPort == 0 && f.Proto == ""
}

// ParseIPOrCIDR parses user input for the SrcIP/DstIP filter fields: either
// a bare address ("10.0.0.1") or a CIDR ("10.0.0.0/24"). A bare address
// becomes a single-address prefix (/32 for IPv4, /128 for IPv6). An empty
// string parses to the zero Prefix (meaning "any"), not an error.
func ParseIPOrCIDR(s string) (netip.Prefix, error) {
	if s == "" {
		return netip.Prefix{}, nil
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p, nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("filter: invalid IP or CIDR %q: %w", s, err)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

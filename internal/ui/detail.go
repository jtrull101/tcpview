package ui

import (
	"fmt"
	"strings"

	"github.com/jtrull101/tcpview/internal/store"
)

// renderDetail renders a human-readable field breakdown of p, followed by
// a hex+ASCII dump of its raw frame bytes if available.
func renderDetail(p store.Packet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Time:     %s\n", p.Time.Format("2006-01-02 15:04:05.000000"))
	fmt.Fprintf(&b, "Iface:    %s\n", p.Iface)
	fmt.Fprintf(&b, "Protocol: %s\n", p.Proto)
	if p.SrcIP.IsValid() {
		b.WriteString("Source:   ")
		b.WriteString(p.SrcIP.String())
		if p.SrcPort != 0 {
			fmt.Fprintf(&b, ":%d", p.SrcPort)
		}
		b.WriteString("\n")
	}
	if p.DstIP.IsValid() {
		b.WriteString("Dest:     ")
		b.WriteString(p.DstIP.String())
		if p.DstPort != 0 {
			fmt.Fprintf(&b, ":%d", p.DstPort)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Length:   %d bytes\n\n", p.Length)

	if len(p.Raw) == 0 {
		b.WriteString("(no raw frame bytes available for this packet)\n")
		return b.String()
	}
	b.WriteString("Hex dump:\n")
	b.WriteString(hexDump(p.Raw))
	return b.String()
}

// hexDump renders data as a classic 16-bytes-per-line offset + hex +
// ASCII dump, e.g.:
//
//	00000000  45 00 00 3c 1c 46 40 00  40 06 b1 e6 c0 a8 00 01  |E..<.F@.@.......|
func hexDump(data []byte) string {
	var b strings.Builder
	for off := 0; off < len(data); off += 16 {
		end := off + 16
		if end > len(data) {
			end = len(data)
		}
		chunk := data[off:end]

		fmt.Fprintf(&b, "%08x  ", off)
		for i := 0; i < 16; i++ {
			if i < len(chunk) {
				fmt.Fprintf(&b, "%02x ", chunk[i])
			} else {
				b.WriteString("   ")
			}
			if i == 7 {
				b.WriteString(" ")
			}
		}
		b.WriteString(" |")
		for _, c := range chunk {
			if c >= 32 && c < 127 {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	return b.String()
}

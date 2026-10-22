// Package export writes tcpview's own Packet values out to a standard pcap
// file that other tools (tcpdump, Wireshark, etc) can open. Kept separate
// from internal/store, which deliberately stays free of any gopacket
// dependency (see store/archive.go's doc comment) - export's whole purpose
// is producing pcap-format output, so that dependency belongs here instead.
package export

import (
	"fmt"
	"os"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	"github.com/jtrull101/tcpview/internal/store"
)

// WritePcap writes pkts to a new pcap file at path, in order, skipping any
// packet with no raw frame bytes (store.Packet.Raw) since those can't be
// reconstructed into a valid frame. Returns how many packets were actually
// written.
//
// Assumes every packet was captured as an Ethernet frame. Traffic captured
// via a non-Ethernet link type (e.g. Linux's "any" pseudo-device) would be
// mislabeled in the resulting file - still byte-for-byte accurate, just
// tagged with the wrong link type header for tools that care.
func WritePcap(path string, pkts []store.Packet) (n int, err error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("export: create %s: %w", path, err)
	}
	defer func() {
		// A Close failure can mean buffered data never actually made it to
		// disk - surface that as the function's error if nothing else
		// already failed, rather than silently reporting success.
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("export: close %s: %w", path, closeErr)
		}
	}()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65536, layers.LinkTypeEthernet); err != nil {
		return 0, fmt.Errorf("export: write header: %w", err)
	}

	for _, p := range pkts {
		if len(p.Raw) == 0 {
			continue
		}
		length := p.Length
		if length < len(p.Raw) {
			length = len(p.Raw)
		}
		ci := gopacket.CaptureInfo{Timestamp: p.Time, CaptureLength: len(p.Raw), Length: length}
		if err := w.WritePacket(ci, p.Raw); err != nil {
			return n, fmt.Errorf("export: write packet %d: %w", n, err)
		}
		n++
	}
	return n, nil
}

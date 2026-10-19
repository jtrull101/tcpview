package capture

import (
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// FuzzDecode feeds arbitrary byte slices through gopacket's Ethernet
// decoder and into Decode - the path real wire traffic (and a remote
// host's tcpdump output) ultimately takes. Malformed/truncated/garbage
// frames must never panic; Decode falling back to ProtoOther is correct.
func FuzzDecode(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add(make([]byte, 14)) // bare Ethernet header, no payload
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x08, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		raw := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("Decode panicked on %d-byte input %x: %v", len(data), data, rec)
			}
		}()
		_, _ = Decode(raw, "eth0")
	})
}

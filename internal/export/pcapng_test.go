package export

import (
	"fmt"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	"github.com/jtrull101/tcpview/internal/store"
)

func exportPacket(t time.Time, raw string) store.Packet {
	return store.Packet{
		Time:    t,
		Iface:   "eth0",
		SrcIP:   netip.MustParseAddr("10.0.0.1"),
		DstIP:   netip.MustParseAddr("10.0.0.2"),
		SrcPort: 1234,
		DstPort: 443,
		Proto:   store.ProtoTCP,
		Length:  len(raw),
		Raw:     []byte(raw),
	}
}

func readBackPcapNG(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	r, err := pcapgo.NewNgReader(f, pcapgo.DefaultNgReaderOptions)
	if err != nil {
		t.Fatalf("pcapgo.NewNgReader: %v", err)
	}

	var out [][]byte
	for {
		data, _, err := r.ReadPacketData()
		if err != nil {
			break
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		out = append(out, cp)
	}
	return out
}

// --- positive ---

func TestWritePcapNG_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.pcapng")
	base := time.Now()
	pkts := []store.Packet{
		exportPacket(base, "frame-one"),
		exportPacket(base.Add(time.Second), "frame-two"),
		exportPacket(base.Add(2*time.Second), "frame-three"),
	}

	res, err := WritePcapNG(path, pkts)
	if err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if n := res.Packets; n != len(pkts) {
		t.Fatalf("WritePcapNG wrote %d packets, want %d", n, len(pkts))
	}

	got := readBackPcapNG(t, path)
	if len(got) != len(pkts) {
		t.Fatalf("read back %d packets, want %d", len(got), len(pkts))
	}
	for i, data := range got {
		if string(data) != string(pkts[i].Raw) {
			t.Fatalf("packet %d = %q, want %q", i, data, pkts[i].Raw)
		}
	}
}

func TestWritePcapNG_CreatesValidPcapFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "valid.pcapng")
	if _, err := WritePcapNG(path, []store.Packet{exportPacket(time.Now(), "x")}); err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := pcapgo.NewNgReader(f, pcapgo.DefaultNgReaderOptions); err != nil {
		t.Fatalf("resulting file is not a valid pcapng file: %v", err)
	}
}

// --- negative ---

func TestWritePcapNG_SkipsPacketsWithoutRawBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.pcapng")
	base := time.Now()
	withRaw := exportPacket(base, "has-raw")
	noRaw := exportPacket(base.Add(time.Second), "")
	noRaw.Raw = nil

	res, err := WritePcapNG(path, []store.Packet{withRaw, noRaw, withRaw})
	if err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if n := res.Packets; n != 2 {
		t.Fatalf("WritePcapNG wrote %d packets, want 2 (one skipped for missing Raw)", n)
	}
}

func TestWritePcapNG_EmptySliceWritesHeaderOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.pcapng")
	res, err := WritePcapNG(path, nil)
	if err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if n := res.Packets; n != 0 {
		t.Fatalf("WritePcapNG wrote %d packets, want 0", n)
	}
	got := readBackPcapNG(t, path)
	if len(got) != 0 {
		t.Fatalf("read back %d packets from an empty export, want 0", len(got))
	}
}

func TestWritePcapNG_UncreatablePathErrors(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	path := filepath.Join(t.TempDir(), "does-not-exist", "out.pcapng")
	if _, err := WritePcapNG(path, nil); err == nil {
		t.Fatal("WritePcapNG into a nonexistent directory returned no error")
	}
}

func TestWritePcapNG_LengthFallsBackToRawLenWhenSmaller(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lenfallback.pcapng")
	p := exportPacket(time.Now(), "longer-than-length-field-says")
	p.Length = 1 // deliberately smaller than len(p.Raw)

	if _, err := WritePcapNG(path, []store.Packet{p}); err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	got := readBackPcapNG(t, path)
	if len(got) != 1 || len(got[0]) != len(p.Raw) {
		t.Fatalf("read back %v, want one packet of length %d", got, len(p.Raw))
	}
}

// --- load ---

func TestWritePcapNG_Load_ManyPacketsFast(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "many.pcapng")
	const n = 20_000
	base := time.Now()
	pkts := make([]store.Packet, n)
	for i := range pkts {
		pkts[i] = exportPacket(base.Add(time.Duration(i)*time.Microsecond), "payload")
	}

	res, err := WritePcapNG(path, pkts)
	if err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if written := res.Packets; written != n {
		t.Fatalf("wrote %d, want %d", written, n)
	}
}

// --- stress ---

func TestWritePcapNG_Stress_LargeRawPayload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "large.pcapng")
	p := exportPacket(time.Now(), "")
	p.Raw = make([]byte, 65000)
	for i := range p.Raw {
		p.Raw[i] = byte(i)
	}
	p.Length = len(p.Raw)

	res, err := WritePcapNG(path, []store.Packet{p})
	if err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if n := res.Packets; n != 1 {
		t.Fatalf("wrote %d packets, want 1", n)
	}
	got := readBackPcapNG(t, path)
	if len(got) != 1 || len(got[0]) != len(p.Raw) {
		t.Fatalf("read back len=%d packets, want 1 packet of length %d", len(got), len(p.Raw))
	}
}

// --- chaos ---

func TestWritePcapNG_Chaos_RandomPacketsRoundTripWithoutPanicking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chaos.pcapng")
	rng := rand.New(rand.NewSource(21))
	base := time.Now()

	pkts := make([]store.Packet, 200)
	for i := range pkts {
		p := exportPacket(base.Add(time.Duration(i)*time.Millisecond), "")
		size := rng.Intn(1500)
		p.Raw = make([]byte, size)
		rng.Read(p.Raw)
		p.Length = size
		if rng.Intn(4) == 0 {
			p.Raw = nil // mix in some unexportable packets
		}
		pkts[i] = p
	}

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("WritePcapNG panicked: %v", rec)
			}
		}()
		if _, err := WritePcapNG(path, pkts); err != nil {
			t.Fatalf("WritePcapNG error: %v", err)
		}
	}()
}

// --- link types ---

// readBackFrames returns every frame in the pcapng file at path along
// with the link type of the interface block it was written under.
type frame struct {
	data     []byte
	linkType layers.LinkType
	iface    string
}

func readBackFrames(t *testing.T, path string) []frame {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	// gopacket's reader (like libpcap) drops frames whose link type
	// differs from the first interface's unless told otherwise; Wireshark
	// reads mixed link types, which is what this option mirrors.
	r, err := pcapgo.NewNgReader(f, pcapgo.NgReaderOptions{WantMixedLinkType: true})
	if err != nil {
		t.Fatalf("NewNgReader: %v", err)
	}
	var out []frame
	for {
		data, ci, err := r.ReadPacketData()
		if err != nil {
			break
		}
		intf, err := r.Interface(ci.InterfaceIndex)
		if err != nil {
			t.Fatalf("interface %d: %v", ci.InterfaceIndex, err)
		}
		out = append(out, frame{data: append([]byte(nil), data...), linkType: intf.LinkType, iface: intf.Name})
	}
	return out
}

func withLinkType(p store.Packet, iface string, lt layers.LinkType) store.Packet {
	p.Iface, p.LinkType, p.HasLinkType = iface, uint16(lt), true
	return p
}

func TestWritePcapNG_EachFrameKeepsItsOwnLinkType(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mixed.pcapng")
	base := time.Now()
	pkts := []store.Packet{
		withLinkType(exportPacket(base, "eth-frame"), "eth0", layers.LinkTypeEthernet),
		withLinkType(exportPacket(base.Add(time.Second), "sll-frame"), "any", layers.LinkTypeLinuxSLL),
		withLinkType(exportPacket(base.Add(2*time.Second), "null-frame"), "lo0", layers.LinkTypeNull),
		withLinkType(exportPacket(base.Add(3*time.Second), "eth-again"), "eth0", layers.LinkTypeEthernet),
	}
	res, err := WritePcapNG(path, pkts)
	if err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if res.LinkTypes != 3 {
		t.Fatalf("Result.LinkTypes = %d, want 3", res.LinkTypes)
	}

	got := readBackFrames(t, path)
	want := []frame{
		{[]byte("eth-frame"), layers.LinkTypeEthernet, "eth0"},
		{[]byte("sll-frame"), layers.LinkTypeLinuxSLL, "any"},
		{[]byte("null-frame"), layers.LinkTypeNull, "lo0"},
		{[]byte("eth-again"), layers.LinkTypeEthernet, "eth0"},
	}
	if len(got) != len(want) {
		t.Fatalf("read back %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i].data) != string(want[i].data) || got[i].linkType != want[i].linkType || got[i].iface != want[i].iface {
			t.Errorf("frame %d = %q/%v/%s, want %q/%v/%s", i, got[i].data, got[i].linkType, got[i].iface, want[i].data, want[i].linkType, want[i].iface)
		}
	}
}

func TestWritePcapNG_AnyDeviceFrameDecodesAsLinuxSLL(t *testing.T) {
	t.Parallel()
	// A real Linux cooked-capture frame, as the "any" device produces:
	// SLL header, then IPv4/UDP to 127.0.0.1:5353.
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: []byte{127, 0, 0, 1}, DstIP: []byte{127, 0, 0, 1}}
	udp := &layers.UDP{SrcPort: 40000, DstPort: 5353}
	_ = udp.SetNetworkLayerForChecksum(ip)
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}, ip, udp, gopacket.Payload("hi")); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	// gopacket can't serialize SLL, so build its 16-byte header by hand:
	// packet type (host), ARPHRD_LOOPBACK, address length, 8 address
	// bytes, then the EtherType of what follows (IPv4).
	sll := []byte{0, 0, 0x03, 0x04, 0, 6, 0, 0, 0, 0, 0, 0, 0, 0, 0x08, 0x00}
	p := withLinkType(exportPacket(time.Now(), ""), "any", layers.LinkTypeLinuxSLL)
	p.Raw = append(sll, buf.Bytes()...)
	p.Length = len(p.Raw)

	path := filepath.Join(t.TempDir(), "any.pcapng")
	if _, err := WritePcapNG(path, []store.Packet{p}); err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	got := readBackFrames(t, path)
	if len(got) != 1 {
		t.Fatalf("read back %d frames, want 1", len(got))
	}
	decoded := gopacket.NewPacket(got[0].data, got[0].linkType, gopacket.Default)
	u, _ := decoded.Layer(layers.LayerTypeUDP).(*layers.UDP)
	if u == nil || u.DstPort != 5353 {
		t.Fatalf("re-decoding the export with its recorded link type gave %v, want the UDP/5353 datagram", decoded)
	}
}

func TestWritePcapNG_PacketsWithoutLinkTypeFallBackToEthernet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.pcapng")
	if _, err := WritePcapNG(path, []store.Packet{exportPacket(time.Now(), "old-archive")}); err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	if got := readBackFrames(t, path); len(got) != 1 || got[0].linkType != layers.LinkTypeEthernet {
		t.Fatalf("frames = %+v, want one Ethernet frame", got)
	}
}

func TestWritePcapNG_Stress_ManyDistinctInterfaces(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "many-ifaces.pcapng")
	base := time.Now()
	var pkts []store.Packet
	for i := 0; i < 300; i++ {
		lt := layers.LinkTypeEthernet
		if i%2 == 1 {
			lt = layers.LinkTypeLinuxSLL
		}
		pkts = append(pkts, withLinkType(exportPacket(base.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("f%d", i)), fmt.Sprintf("veth%d", i%100), lt))
	}
	if _, err := WritePcapNG(path, pkts); err != nil {
		t.Fatalf("WritePcapNG error: %v", err)
	}
	got := readBackFrames(t, path)
	if len(got) != len(pkts) {
		t.Fatalf("read back %d frames, want %d", len(got), len(pkts))
	}
	for i, fr := range got {
		if fr.iface != pkts[i].Iface || uint16(fr.linkType) != pkts[i].LinkType {
			t.Fatalf("frame %d tagged %s/%v, want %s/%v", i, fr.iface, fr.linkType, pkts[i].Iface, layers.LinkType(pkts[i].LinkType))
		}
	}
}

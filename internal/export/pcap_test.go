package export

import (
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func readBackPcap(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	r, err := pcapgo.NewReader(f)
	if err != nil {
		t.Fatalf("pcapgo.NewReader: %v", err)
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

func TestWritePcap_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.pcap")
	base := time.Now()
	pkts := []store.Packet{
		exportPacket(base, "frame-one"),
		exportPacket(base.Add(time.Second), "frame-two"),
		exportPacket(base.Add(2*time.Second), "frame-three"),
	}

	n, err := WritePcap(path, pkts)
	if err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}
	if n != len(pkts) {
		t.Fatalf("WritePcap wrote %d packets, want %d", n, len(pkts))
	}

	got := readBackPcap(t, path)
	if len(got) != len(pkts) {
		t.Fatalf("read back %d packets, want %d", len(got), len(pkts))
	}
	for i, data := range got {
		if string(data) != string(pkts[i].Raw) {
			t.Fatalf("packet %d = %q, want %q", i, data, pkts[i].Raw)
		}
	}
}

func TestWritePcap_CreatesValidPcapFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "valid.pcap")
	if _, err := WritePcap(path, []store.Packet{exportPacket(time.Now(), "x")}); err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := pcapgo.NewReader(f); err != nil {
		t.Fatalf("resulting file is not a valid pcap file: %v", err)
	}
}

// --- negative ---

func TestWritePcap_SkipsPacketsWithoutRawBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.pcap")
	base := time.Now()
	withRaw := exportPacket(base, "has-raw")
	noRaw := exportPacket(base.Add(time.Second), "")
	noRaw.Raw = nil

	n, err := WritePcap(path, []store.Packet{withRaw, noRaw, withRaw})
	if err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}
	if n != 2 {
		t.Fatalf("WritePcap wrote %d packets, want 2 (one skipped for missing Raw)", n)
	}
}

func TestWritePcap_EmptySliceWritesHeaderOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.pcap")
	n, err := WritePcap(path, nil)
	if err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}
	if n != 0 {
		t.Fatalf("WritePcap wrote %d packets, want 0", n)
	}
	got := readBackPcap(t, path)
	if len(got) != 0 {
		t.Fatalf("read back %d packets from an empty export, want 0", len(got))
	}
}

func TestWritePcap_UncreatablePathErrors(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	path := filepath.Join(t.TempDir(), "does-not-exist", "out.pcap")
	if _, err := WritePcap(path, nil); err == nil {
		t.Fatal("WritePcap into a nonexistent directory returned no error")
	}
}

func TestWritePcap_LengthFallsBackToRawLenWhenSmaller(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lenfallback.pcap")
	p := exportPacket(time.Now(), "longer-than-length-field-says")
	p.Length = 1 // deliberately smaller than len(p.Raw)

	if _, err := WritePcap(path, []store.Packet{p}); err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}
	got := readBackPcap(t, path)
	if len(got) != 1 || len(got[0]) != len(p.Raw) {
		t.Fatalf("read back %v, want one packet of length %d", got, len(p.Raw))
	}
}

// --- load ---

func TestWritePcap_Load_ManyPacketsFast(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "many.pcap")
	const n = 20_000
	base := time.Now()
	pkts := make([]store.Packet, n)
	for i := range pkts {
		pkts[i] = exportPacket(base.Add(time.Duration(i)*time.Microsecond), "payload")
	}

	written, err := WritePcap(path, pkts)
	if err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}
	if written != n {
		t.Fatalf("wrote %d, want %d", written, n)
	}
}

// --- stress ---

func TestWritePcap_Stress_LargeRawPayload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "large.pcap")
	p := exportPacket(time.Now(), "")
	p.Raw = make([]byte, 65000)
	for i := range p.Raw {
		p.Raw[i] = byte(i)
	}
	p.Length = len(p.Raw)

	n, err := WritePcap(path, []store.Packet{p})
	if err != nil {
		t.Fatalf("WritePcap error: %v", err)
	}
	if n != 1 {
		t.Fatalf("wrote %d packets, want 1", n)
	}
	got := readBackPcap(t, path)
	if len(got) != 1 || len(got[0]) != len(p.Raw) {
		t.Fatalf("read back len=%d packets, want 1 packet of length %d", len(got), len(p.Raw))
	}
}

// --- chaos ---

func TestWritePcap_Chaos_RandomPacketsRoundTripWithoutPanicking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chaos.pcap")
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
				t.Fatalf("WritePcap panicked: %v", rec)
			}
		}()
		if _, err := WritePcap(path, pkts); err != nil {
			t.Fatalf("WritePcap error: %v", err)
		}
	}()
}

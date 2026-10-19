package ui

import (
	"math/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jtrull101/tcpview/internal/store"
)

func detailPacket() store.Packet {
	return store.Packet{
		Time:    time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC),
		Iface:   "eth0",
		SrcIP:   netip.MustParseAddr("10.0.0.1"),
		DstIP:   netip.MustParseAddr("10.0.0.2"),
		SrcPort: 1234,
		DstPort: 443,
		Proto:   store.ProtoTCP,
		Length:  60,
		Raw:     []byte("Hello, world! This is a raw frame payload over 16 bytes."),
	}
}

// --- positive ---

func TestRenderDetail_IncludesAllFields(t *testing.T) {
	t.Parallel()
	out := renderDetail(detailPacket())
	for _, want := range []string{"eth0", "10.0.0.1", "1234", "10.0.0.2", "443", "TCP", "60 bytes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderDetail() = %q, missing %q", out, want)
		}
	}
}

func TestHexDump_FullLineFormat(t *testing.T) {
	t.Parallel()
	data := []byte("0123456789abcdef") // exactly 16 bytes
	out := hexDump(data)
	if !strings.Contains(out, "00000000") {
		t.Fatalf("hexDump() = %q, missing offset", out)
	}
	if !strings.Contains(out, "|0123456789abcdef|") {
		t.Fatalf("hexDump() = %q, missing ASCII column", out)
	}
	if !strings.Contains(out, "30 31 32 33") { // hex for '0','1','2','3'
		t.Fatalf("hexDump() = %q, missing expected hex bytes", out)
	}
}

func TestHexDump_MultiLineOffsets(t *testing.T) {
	t.Parallel()
	data := make([]byte, 20) // spans two lines (16 + 4)
	out := hexDump(data)
	if !strings.Contains(out, "00000000") || !strings.Contains(out, "00000010") {
		t.Fatalf("hexDump() = %q, want offsets for both lines", out)
	}
}

func TestHexDump_NonPrintableBytesBecomeDots(t *testing.T) {
	t.Parallel()
	data := []byte{0x00, 0x01, 0x1f, 0x7f, 0xff, 'A'}
	out := hexDump(data)
	if !strings.Contains(out, "|.....A|") {
		t.Fatalf("hexDump() = %q, want non-printables rendered as '.'", out)
	}
}

// --- negative ---

func TestRenderDetail_NoRawBytesSaysSo(t *testing.T) {
	t.Parallel()
	p := detailPacket()
	p.Raw = nil
	out := renderDetail(p)
	if !strings.Contains(out, "no raw frame bytes") {
		t.Fatalf("renderDetail() = %q, want a note about missing raw bytes", out)
	}
	if strings.Contains(out, "Hex dump:") {
		t.Fatalf("renderDetail() = %q, should not print a hex dump header with no data", out)
	}
}

func TestRenderDetail_NoIPsOmitsAddressLines(t *testing.T) {
	t.Parallel()
	p := store.Packet{Time: time.Now(), Proto: store.ProtoARP, Length: 28}
	out := renderDetail(p)
	if strings.Contains(out, "Source:") || strings.Contains(out, "Dest:") {
		t.Fatalf("renderDetail() = %q, should omit Source/Dest when no IPs are set", out)
	}
}

func TestHexDump_EmptyDataProducesNoLines(t *testing.T) {
	t.Parallel()
	if out := hexDump(nil); out != "" {
		t.Fatalf("hexDump(nil) = %q, want empty", out)
	}
}

// --- load ---

func TestHexDump_Load_LargePayloadFast(t *testing.T) {
	t.Parallel()
	data := make([]byte, 65000)
	for i := range data {
		data[i] = byte(i)
	}
	out := hexDump(data)
	if len(out) == 0 {
		t.Fatal("hexDump on a large payload produced no output")
	}
}

// --- stress ---

func TestHexDump_Stress_OddLengthsNeverMisalign(t *testing.T) {
	t.Parallel()
	for n := 0; n < 40; n++ {
		data := make([]byte, n)
		out := hexDump(data)
		wantLines := (n + 15) / 16
		gotLines := strings.Count(out, "\n")
		if gotLines != wantLines {
			t.Fatalf("n=%d: hexDump produced %d lines, want %d", n, gotLines, wantLines)
		}
	}
}

// --- chaos ---

func TestHexDump_Chaos_RandomSizesNeverPanic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 200; i++ {
		data := make([]byte, rng.Intn(300))
		rng.Read(data)
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("hexDump panicked at i=%d (len=%d): %v", i, len(data), rec)
				}
			}()
			_ = hexDump(data)
		}()
	}
}

func TestRenderDetail_Chaos_ExtremeValuesNeverPanic(t *testing.T) {
	t.Parallel()
	cases := []store.Packet{
		{},
		{Time: time.Now(), SrcIP: netip.MustParseAddr("::1"), DstIP: netip.MustParseAddr("::1"), SrcPort: 65535, DstPort: 65535, Length: -1},
		{Time: time.Now(), Raw: make([]byte, 100000)},
	}
	for i, p := range cases {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("renderDetail panicked on case %d: %v", i, rec)
				}
			}()
			_ = renderDetail(p)
		}()
	}
}

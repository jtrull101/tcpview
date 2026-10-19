package filter

import (
	"math/rand"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jtrull101/tcpview/internal/store"
)

func samplePacket() store.Packet {
	return store.Packet{
		Time:    time.Now(),
		Iface:   "eth0",
		SrcIP:   netip.MustParseAddr("10.0.0.1"),
		DstIP:   netip.MustParseAddr("192.168.1.1"),
		SrcPort: 51234,
		DstPort: 443,
		Proto:   store.ProtoTCP,
		Length:  60,
	}
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("ParsePrefix(%q): %v", s, err)
	}
	return p
}

// --- positive ---

func TestFilter_ZeroFilterMatchesEverything(t *testing.T) {
	t.Parallel()
	var f Filter
	if !f.IsZero() {
		t.Fatal("zero-value Filter.IsZero() = false")
	}
	if !f.Matches(samplePacket()) {
		t.Fatal("zero-value Filter did not match a packet")
	}
}

func TestFilter_MatchesEachDimensionIndividually(t *testing.T) {
	t.Parallel()
	p := samplePacket()

	cases := []struct {
		name string
		f    Filter
	}{
		{"iface", Filter{Iface: "eth0"}},
		{"srcIP /32", Filter{SrcIP: mustPrefix(t, "10.0.0.1/32")}},
		{"srcIP CIDR", Filter{SrcIP: mustPrefix(t, "10.0.0.0/24")}},
		{"dstIP", Filter{DstIP: mustPrefix(t, "192.168.1.0/24")}},
		{"srcPort", Filter{SrcPort: 51234}},
		{"dstPort", Filter{DstPort: 443}},
		{"proto", Filter{Proto: store.ProtoTCP}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.f.Matches(p) {
				t.Fatalf("Filter %+v did not match packet %+v", tc.f, p)
			}
		})
	}
}

func TestFilter_AllDimensionsCombinedMatch(t *testing.T) {
	t.Parallel()
	f := Filter{
		Iface:   "eth0",
		SrcIP:   mustPrefix(t, "10.0.0.0/8"),
		DstIP:   mustPrefix(t, "192.168.0.0/16"),
		SrcPort: 51234,
		DstPort: 443,
		Proto:   store.ProtoTCP,
	}
	if !f.Matches(samplePacket()) {
		t.Fatal("fully-specified Filter did not match a packet satisfying every dimension")
	}
}

// --- negative ---

func TestFilter_MismatchOnEachDimensionExcludes(t *testing.T) {
	t.Parallel()
	p := samplePacket()

	cases := []struct {
		name string
		f    Filter
	}{
		{"iface", Filter{Iface: "wlan0"}},
		{"srcIP", Filter{SrcIP: mustPrefix(t, "172.16.0.0/12")}},
		{"dstIP", Filter{DstIP: mustPrefix(t, "10.0.0.0/8")}},
		{"srcPort", Filter{SrcPort: 9999}},
		{"dstPort", Filter{DstPort: 80}},
		{"proto", Filter{Proto: store.ProtoUDP}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.f.Matches(p) {
				t.Fatalf("Filter %+v matched a packet it should have excluded: %+v", tc.f, p)
			}
		})
	}
}

func TestFilter_ARPPacketNeverMatchesIPFilter(t *testing.T) {
	t.Parallel()
	arp := store.Packet{Time: time.Now(), Iface: "eth0", Proto: store.ProtoARP}
	f := Filter{SrcIP: mustPrefix(t, "0.0.0.0/0")}
	if f.Matches(arp) {
		t.Fatal("IP filter matched an ARP packet with no IP addresses")
	}
}

func TestFilter_IPv4PrefixNeverMatchesIPv6Packet(t *testing.T) {
	t.Parallel()
	p := samplePacket()
	p.SrcIP = netip.MustParseAddr("::1")
	f := Filter{SrcIP: mustPrefix(t, "10.0.0.0/8")}
	if f.Matches(p) {
		t.Fatal("IPv4 prefix matched an IPv6 address")
	}
}

func TestParseIPOrCIDR_InvalidInputErrors(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"not-an-ip", "10.0.0.999", "10.0.0.0/999", "::g"} {
		if _, err := ParseIPOrCIDR(bad); err == nil {
			t.Fatalf("ParseIPOrCIDR(%q) returned no error", bad)
		}
	}
}

func TestParseIPOrCIDR_EmptyStringIsAny(t *testing.T) {
	t.Parallel()
	p, err := ParseIPOrCIDR("")
	if err != nil {
		t.Fatalf("ParseIPOrCIDR(\"\") error: %v", err)
	}
	if p.IsValid() {
		t.Fatalf("ParseIPOrCIDR(\"\") = %v, want zero/invalid Prefix", p)
	}
}

func TestParseIPOrCIDR_BareAddressAndCIDR(t *testing.T) {
	t.Parallel()
	p, err := ParseIPOrCIDR("10.0.0.5")
	if err != nil {
		t.Fatalf("ParseIPOrCIDR bare address error: %v", err)
	}
	if p.Bits() != 32 || !p.Contains(netip.MustParseAddr("10.0.0.5")) {
		t.Fatalf("bare address parsed as %v, want a /32 containing 10.0.0.5", p)
	}

	p, err = ParseIPOrCIDR("10.0.0.0/24")
	if err != nil {
		t.Fatalf("ParseIPOrCIDR CIDR error: %v", err)
	}
	if !p.Contains(netip.MustParseAddr("10.0.0.200")) {
		t.Fatalf("CIDR %v does not contain 10.0.0.200", p)
	}
}

// --- load ---

func TestFilter_Load_ManyPacketsFast(t *testing.T) {
	t.Parallel()
	f := Filter{Iface: "eth0", Proto: store.ProtoTCP}
	p := samplePacket()
	const n = 500_000
	matched := 0
	for i := 0; i < n; i++ {
		if f.Matches(p) {
			matched++
		}
	}
	if matched != n {
		t.Fatalf("matched %d/%d, want all to match", matched, n)
	}
}

// --- stress ---

func TestFilter_Stress_ExhaustiveDimensionCombinations(t *testing.T) {
	t.Parallel()
	p := samplePacket()
	ifaces := []string{"", "eth0", "wlan0"}
	protos := []store.Proto{"", store.ProtoTCP, store.ProtoUDP}
	ports := []uint16{0, 443, 9999}

	count := 0
	for _, iface := range ifaces {
		for _, proto := range protos {
			for _, srcPort := range ports {
				for _, dstPort := range ports {
					f := Filter{Iface: iface, Proto: proto, SrcPort: srcPort, DstPort: dstPort}
					want := (iface == "" || iface == p.Iface) &&
						(proto == "" || proto == p.Proto) &&
						(srcPort == 0 || srcPort == p.SrcPort) &&
						(dstPort == 0 || dstPort == p.DstPort)
					if got := f.Matches(p); got != want {
						t.Fatalf("Filter %+v: Matches() = %v, want %v", f, got, want)
					}
					count++
				}
			}
		}
	}
	if count != len(ifaces)*len(protos)*len(ports)*len(ports) {
		t.Fatalf("exhaustive combination count = %d, expected full cross product", count)
	}
}

// --- chaos ---

func TestFilter_Chaos_RandomPacketsAndFiltersNeverPanic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	protos := []store.Proto{"", store.ProtoTCP, store.ProtoUDP, store.ProtoICMP, store.ProtoARP, store.ProtoOther}

	randAddr := func() netip.Addr {
		switch rng.Intn(3) {
		case 0:
			return netip.Addr{} // zero/invalid - e.g. ARP
		case 1:
			b := make([]byte, 4)
			rng.Read(b)
			a, _ := netip.AddrFromSlice(b)
			return a
		default:
			b := make([]byte, 16)
			rng.Read(b)
			a, _ := netip.AddrFromSlice(b)
			return a
		}
	}
	randPrefix := func() netip.Prefix {
		if rng.Intn(2) == 0 {
			return netip.Prefix{} // "any"
		}
		addr := randAddr()
		if !addr.IsValid() {
			return netip.Prefix{}
		}
		bits := rng.Intn(addr.BitLen() + 1)
		return netip.PrefixFrom(addr, bits)
	}

	for i := 0; i < 2000; i++ {
		p := store.Packet{
			Time:    time.Now(),
			Iface:   []string{"", "eth0", "wlan0"}[rng.Intn(3)],
			SrcIP:   randAddr(),
			DstIP:   randAddr(),
			SrcPort: uint16(rng.Intn(70000)),
			DstPort: uint16(rng.Intn(70000)),
			Proto:   protos[rng.Intn(len(protos))],
		}
		f := Filter{
			Iface:   []string{"", "eth0", "wlan0"}[rng.Intn(3)],
			SrcIP:   randPrefix(),
			DstIP:   randPrefix(),
			SrcPort: uint16(rng.Intn(70000)),
			DstPort: uint16(rng.Intn(70000)),
			Proto:   protos[rng.Intn(len(protos))],
		}

		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Matches panicked on iteration %d (packet=%+v filter=%+v): %v", i, p, f, rec)
				}
			}()
			_ = f.Matches(p)
		}()
	}
}

func TestFilter_Chaos_ConcurrentMatches(t *testing.T) {
	t.Parallel()
	f := Filter{Iface: "eth0", Proto: store.ProtoTCP}
	p := samplePacket()

	var wg sync.WaitGroup
	failures := make(chan int, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10_000; i++ {
				if !f.Matches(p) {
					failures <- 1
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for range failures {
		t.Fatal("unexpected non-match under concurrent use")
	}
}

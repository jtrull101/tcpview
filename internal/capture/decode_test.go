package capture

import (
	"math/rand"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"github.com/jtrull101/tcpview/internal/store"
)

var (
	macA = net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB = net.HardwareAddr{0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb}
)

func serialize(t *testing.T, layerList ...gopacket.SerializableLayer) []byte {
	t.Helper()
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, layerList...); err != nil {
		t.Fatalf("serialize test packet: %v", err)
	}
	return buf.Bytes()
}

func tcpPacket(t *testing.T) gopacket.Packet {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolTCP,
		SrcIP: net.ParseIP("10.0.0.1").To4(), DstIP: net.ParseIP("10.0.0.2").To4()}
	tcp := &layers.TCP{SrcPort: 1234, DstPort: 443, SYN: true}
	_ = tcp.SetNetworkLayerForChecksum(ip)
	data := serialize(t, eth, ip, tcp, gopacket.Payload([]byte("hello")))
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

func udpPacket(t *testing.T) gopacket.Packet {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP,
		SrcIP: net.ParseIP("10.0.0.1").To4(), DstIP: net.ParseIP("10.0.0.2").To4()}
	udp := &layers.UDP{SrcPort: 53000, DstPort: 53}
	_ = udp.SetNetworkLayerForChecksum(ip)
	data := serialize(t, eth, ip, udp, gopacket.Payload([]byte("q")))
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

func arpPacket(t *testing.T) gopacket.Packet {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeARP}
	arp := &layers.ARP{
		AddrType: layers.LinkTypeEthernet, Protocol: layers.EthernetTypeIPv4,
		HwAddressSize: 6, ProtAddressSize: 4, Operation: layers.ARPRequest,
		SourceHwAddress: macA, SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress: macB, DstProtAddress: net.ParseIP("10.0.0.2").To4(),
	}
	data := serialize(t, eth, arp)
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

func icmpPacket(t *testing.T) gopacket.Packet {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4,
		SrcIP: net.ParseIP("10.0.0.1").To4(), DstIP: net.ParseIP("10.0.0.2").To4()}
	icmp := &layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	data := serialize(t, eth, ip, icmp, gopacket.Payload([]byte("ping")))
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

func tcpIPv6Packet(t *testing.T) gopacket.Packet {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeIPv6}
	ip6 := &layers.IPv6{Version: 6, HopLimit: 64, NextHeader: layers.IPProtocolTCP,
		SrcIP: net.ParseIP("::1"), DstIP: net.ParseIP("::1")}
	tcp := &layers.TCP{SrcPort: 9000, DstPort: 22, ACK: true}
	_ = tcp.SetNetworkLayerForChecksum(ip6)
	data := serialize(t, eth, ip6, tcp)
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

// --- positive ---

func TestDecode_TCP(t *testing.T) {
	t.Parallel()
	p, err := Decode(tcpPacket(t), "eth0")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if p.Proto != store.ProtoTCP {
		t.Fatalf("Proto = %v, want TCP", p.Proto)
	}
	if p.SrcIP != netip.MustParseAddr("10.0.0.1") || p.DstIP != netip.MustParseAddr("10.0.0.2") {
		t.Fatalf("IPs = %v -> %v, want 10.0.0.1 -> 10.0.0.2", p.SrcIP, p.DstIP)
	}
	if p.SrcPort != 1234 || p.DstPort != 443 {
		t.Fatalf("ports = %d -> %d, want 1234 -> 443", p.SrcPort, p.DstPort)
	}
	if p.Iface != "eth0" {
		t.Fatalf("Iface = %q, want eth0", p.Iface)
	}
	if p.Summary == "" {
		t.Fatal("Summary is empty")
	}
	if len(p.Raw) == 0 {
		t.Fatal("Raw is empty, want the original frame bytes preserved")
	}
}

func TestDecode_UDP(t *testing.T) {
	t.Parallel()
	p, err := Decode(udpPacket(t), "eth0")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if p.Proto != store.ProtoUDP {
		t.Fatalf("Proto = %v, want UDP", p.Proto)
	}
	if p.SrcPort != 53000 || p.DstPort != 53 {
		t.Fatalf("ports = %d -> %d, want 53000 -> 53", p.SrcPort, p.DstPort)
	}
}

func TestDecode_ARP_NoPorts(t *testing.T) {
	t.Parallel()
	p, err := Decode(arpPacket(t), "eth0")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if p.Proto != store.ProtoARP {
		t.Fatalf("Proto = %v, want ARP", p.Proto)
	}
	if p.SrcPort != 0 || p.DstPort != 0 {
		t.Fatalf("ports = %d -> %d, want 0 -> 0 (ARP has no ports)", p.SrcPort, p.DstPort)
	}
}

func TestDecode_ICMPv4(t *testing.T) {
	t.Parallel()
	p, err := Decode(icmpPacket(t), "eth0")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if p.Proto != store.ProtoICMP {
		t.Fatalf("Proto = %v, want ICMP", p.Proto)
	}
}

func TestDecode_IPv6TCP(t *testing.T) {
	t.Parallel()
	p, err := Decode(tcpIPv6Packet(t), "eth0")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if p.Proto != store.ProtoTCP {
		t.Fatalf("Proto = %v, want TCP", p.Proto)
	}
	if p.SrcIP != netip.MustParseAddr("::1") || p.DstIP != netip.MustParseAddr("::1") {
		t.Fatalf("IPv6 addrs = %v -> %v, want ::1 -> ::1", p.SrcIP, p.DstIP)
	}
}

func TestDecode_UsesCaptureTimestampWhenPresent(t *testing.T) {
	t.Parallel()
	raw := tcpPacket(t)
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	raw.Metadata().CaptureInfo = gopacket.CaptureInfo{Timestamp: want, Length: len(raw.Data())}

	p, err := Decode(raw, "eth0")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if !p.Time.Equal(want) {
		t.Fatalf("Time = %v, want %v", p.Time, want)
	}
}

// --- negative ---

func TestDecode_NilPacketErrors(t *testing.T) {
	t.Parallel()
	_, err := Decode(nil, "eth0")
	if err == nil {
		t.Fatal("Decode(nil, ...) returned no error")
	}
}

func TestDecode_EmptyDataNoPanicNoCrash(t *testing.T) {
	t.Parallel()
	raw := gopacket.NewPacket(nil, layers.LayerTypeEthernet, gopacket.Default)
	p, err := Decode(raw, "eth0")
	if err != nil {
		t.Fatalf("Decode returned error on empty data: %v", err)
	}
	if p.Proto != store.ProtoOther {
		t.Fatalf("Proto = %v, want OTHER for undecodable data", p.Proto)
	}
}

func TestDecode_TruncatedPacketFallsBackGracefully(t *testing.T) {
	t.Parallel()
	full := serializeTCPBytes(t)
	truncated := full[:20] // cuts off mid-IP-header
	raw := gopacket.NewPacket(truncated, layers.LayerTypeEthernet, gopacket.Default)

	p, err := Decode(raw, "eth0")
	if err != nil {
		t.Fatalf("Decode returned error on truncated data: %v", err)
	}
	// Whatever did decode should be internally consistent; above all, no panic.
	_ = p
}

func serializeTCPBytes(t *testing.T) []byte {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolTCP,
		SrcIP: net.ParseIP("10.0.0.1").To4(), DstIP: net.ParseIP("10.0.0.2").To4()}
	tcp := &layers.TCP{SrcPort: 1234, DstPort: 443, SYN: true}
	_ = tcp.SetNetworkLayerForChecksum(ip)
	return serialize(t, eth, ip, tcp, gopacket.Payload([]byte("hello world")))
}

// --- load ---

func TestDecode_Load_ManyPacketsFast(t *testing.T) {
	t.Parallel()
	raw := tcpPacket(t)
	const n = 50_000
	for i := 0; i < n; i++ {
		if _, err := Decode(raw, "eth0"); err != nil {
			t.Fatalf("Decode failed at iteration %d: %v", i, err)
		}
	}
}

// --- stress ---

func TestDecode_Stress_LargePayload(t *testing.T) {
	t.Parallel()
	eth := &layers.Ethernet{SrcMAC: macA, DstMAC: macB, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP,
		SrcIP: net.ParseIP("10.0.0.1").To4(), DstIP: net.ParseIP("10.0.0.2").To4()}
	udp := &layers.UDP{SrcPort: 1, DstPort: 2}
	_ = udp.SetNetworkLayerForChecksum(ip)
	payload := make([]byte, 65000)
	for i := range payload {
		payload[i] = byte(i)
	}
	data := serialize(t, eth, ip, udp, gopacket.Payload(payload))
	raw := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)

	p, err := Decode(raw, "eth0")
	if err != nil {
		t.Fatalf("Decode returned error on large payload: %v", err)
	}
	if p.Length <= 0 {
		t.Fatalf("Length = %d, want > 0 for a large packet", p.Length)
	}
}

// --- chaos ---

func TestDecode_Chaos_RandomBytesNeverPanics(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		size := rng.Intn(300)
		data := make([]byte, size)
		rng.Read(data)

		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Decode panicked on random input (seed case %d, size %d): %v", i, size, rec)
				}
			}()
			raw := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
			_, _ = Decode(raw, "eth0")
		}()
	}
}

func TestDecode_Chaos_ConcurrentDecodeOfDistinctPackets(t *testing.T) {
	t.Parallel()
	const goroutines = 16
	const perGoroutine = 500

	// Build the packet pool on the test goroutine first - t.Fatalf must only
	// ever be called from the goroutine running the test, never from the
	// worker goroutines below.
	pool := []gopacket.Packet{tcpPacket(t), udpPacket(t), arpPacket(t), icmpPacket(t)}

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				raw := pool[(g+i)%len(pool)]
				if _, err := Decode(raw, "eth0"); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Decode failed: %v", err)
	}
}

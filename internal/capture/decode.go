package capture

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"github.com/jtrull101/tcpview/internal/store"
)

// Decode converts a raw gopacket.Packet into tcpview's local store.Packet
// representation. Deliberately split out from the live-capture read loop so
// it's unit-testable against synthetic packets (built with
// gopacket/layers.SerializeLayers) without a real pcap handle, interface, or
// root privileges.
//
// Decode never fails on malformed or partially-decodable packet data - a
// packet gopacket couldn't fully parse just falls back to ProtoOther with
// whatever layers *did* decode, since one bad packet on the wire shouldn't
// abort a live capture. It only errors on a nil packet, which indicates a
// caller bug rather than a wire anomaly.
func Decode(raw gopacket.Packet, iface string) (store.Packet, error) {
	if raw == nil {
		return store.Packet{}, fmt.Errorf("capture: decode: nil packet")
	}

	ts := time.Now()
	length := len(raw.Data())
	if md := raw.Metadata(); md != nil {
		if !md.Timestamp.IsZero() {
			ts = md.Timestamp
		}
		if md.Length > 0 {
			length = md.Length
		}
	}

	p := store.Packet{
		Time:   ts,
		Iface:  iface,
		Length: length,
		Proto:  store.ProtoOther,
		Raw:    raw.Data(),
	}

	if net := raw.NetworkLayer(); net != nil {
		src, dst := net.NetworkFlow().Endpoints()
		if addr, ok := netip.AddrFromSlice(src.Raw()); ok {
			p.SrcIP = addr
		}
		if addr, ok := netip.AddrFromSlice(dst.Raw()); ok {
			p.DstIP = addr
		}
	}

	switch {
	case raw.Layer(layers.LayerTypeTCP) != nil:
		tcp, _ := raw.Layer(layers.LayerTypeTCP).(*layers.TCP)
		p.Proto = store.ProtoTCP
		if tcp != nil {
			p.SrcPort = uint16(tcp.SrcPort)
			p.DstPort = uint16(tcp.DstPort)
		}
	case raw.Layer(layers.LayerTypeUDP) != nil:
		udp, _ := raw.Layer(layers.LayerTypeUDP).(*layers.UDP)
		p.Proto = store.ProtoUDP
		if udp != nil {
			p.SrcPort = uint16(udp.SrcPort)
			p.DstPort = uint16(udp.DstPort)
		}
	case raw.Layer(layers.LayerTypeICMPv4) != nil:
		p.Proto = store.ProtoICMP
	case raw.Layer(layers.LayerTypeICMPv6) != nil:
		p.Proto = store.ProtoICMPv6
	case raw.Layer(layers.LayerTypeARP) != nil:
		p.Proto = store.ProtoARP
	}

	p.Summary = p.String()
	return p, nil
}

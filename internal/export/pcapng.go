// Package export writes tcpview's own Packet values out to a standard
// pcapng file that other tools (tcpdump, Wireshark, etc) can open. Kept
// separate from internal/store, which deliberately stays free of any
// gopacket dependency (see store/archive.go's doc comment) - export's
// whole purpose is producing pcapng output, so that dependency belongs
// here instead.
package export

import (
	"fmt"
	"os"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	"github.com/jtrull101/tcpview/internal/store"
)

// fallbackLinkType labels packets that don't record their own link type
// (archives from before store.Packet.LinkType existed, hand-built
// Packets). Ethernet is what nearly every real interface uses.
const fallbackLinkType = layers.LinkTypeEthernet

// ifaceKey identifies one pcapng Interface Description Block: a pcapng
// file can hold frames of several link types, but each IDB has exactly
// one, so packets get an IDB per distinct (interface, link type) pair.
type ifaceKey struct {
	name     string
	linkType layers.LinkType
}

func keyOf(p store.Packet) ifaceKey {
	lt := fallbackLinkType
	if p.HasLinkType {
		lt = layers.LinkType(p.LinkType)
	}
	return ifaceKey{name: p.Iface, linkType: lt}
}

// Result describes what WritePcapNG wrote.
type Result struct {
	Packets   int // packets written (those with raw frame bytes)
	LinkTypes int // distinct link types among them
}

// WritePcapNG writes pkts to a new pcapng file at path, in order, skipping
// any packet with no raw frame bytes (store.Packet.Raw) since those can't
// be reconstructed into a valid frame.
//
// Each packet is written under an interface block carrying its own link
// type, so frames from Linux's "any" device (Linux cooked capture, not
// Ethernet) or macOS loopback (BSD null) are labeled correctly - classic
// pcap, with one hardcoded Ethernet header, mislabeled them. A file whose
// packets share one link type (any single-interface export) opens in
// Wireshark and tcpdump alike. One mixing link types (a rewound view
// spanning a tab switch between, say, eth0 and any) is valid pcapng that
// Wireshark reads, but libpcap-based tools such as tcpdump refuse it;
// Result.LinkTypes lets the caller say so.
func WritePcapNG(path string, pkts []store.Packet) (res Result, err error) {
	var keys []ifaceKey
	index := map[ifaceKey]int{}
	linkTypes := map[layers.LinkType]bool{}
	for _, p := range pkts {
		if len(p.Raw) == 0 {
			continue
		}
		k := keyOf(p)
		linkTypes[k.linkType] = true
		if _, seen := index[k]; !seen {
			index[k] = len(keys)
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		// A file needs at least one interface block to be valid.
		keys = []ifaceKey{{name: "tcpview", linkType: fallbackLinkType}}
	}

	f, err := os.Create(path)
	if err != nil {
		return Result{}, fmt.Errorf("export: create %s: %w", path, err)
	}
	defer func() {
		// A Close failure can mean buffered data never actually made it to
		// disk - surface that as the function's error if nothing else
		// already failed, rather than silently reporting success.
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("export: close %s: %w", path, closeErr)
		}
	}()

	opts := pcapgo.DefaultNgWriterOptions
	opts.SectionInfo.Application = "tcpview"
	w, err := pcapgo.NewNgWriterInterface(f, ngInterface(keys[0]), opts)
	if err != nil {
		return Result{}, fmt.Errorf("export: write header: %w", err)
	}
	for _, k := range keys[1:] {
		if _, err := w.AddInterface(ngInterface(k)); err != nil {
			return Result{}, fmt.Errorf("export: write interface %s: %w", k.name, err)
		}
	}

	for _, p := range pkts {
		if len(p.Raw) == 0 {
			continue
		}
		length := p.Length
		if length < len(p.Raw) {
			length = len(p.Raw)
		}
		ci := gopacket.CaptureInfo{
			Timestamp:      p.Time,
			CaptureLength:  len(p.Raw),
			Length:         length,
			InterfaceIndex: index[keyOf(p)],
		}
		if err := w.WritePacket(ci, p.Raw); err != nil {
			return res, fmt.Errorf("export: write packet %d: %w", res.Packets, err)
		}
		res.Packets++
	}
	res.LinkTypes = len(linkTypes)
	if err := w.Flush(); err != nil {
		return res, fmt.Errorf("export: flush %s: %w", path, err)
	}
	return res, nil
}

func ngInterface(k ifaceKey) pcapgo.NgInterface {
	intf := pcapgo.DefaultNgInterface
	intf.Name = k.name
	intf.LinkType = k.linkType
	return intf
}

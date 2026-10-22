package capture

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/gopacket/gopacket/pcap"
)

// Interface describes one local network interface available for capture.
type Interface struct {
	Name        string
	Description string
	Addresses   []string
}

// DisplayName is the name to show people. On Windows, Npcap names
// interfaces by device path ("\Device\NPF_{4A1C...}"), which says nothing
// about which adapter it is; the description ("Intel(R) Ethernet ...")
// does, so that's shown instead. Everywhere else the name is already the
// familiar one (eth0, en0, lo).
func (i Interface) DisplayName() string {
	if strings.HasPrefix(i.Name, `\Device\`) && i.Description != "" {
		return i.Description
	}
	return i.Name
}

// ErrNpcapMissing is returned by ListInterfaces on Windows when Npcap,
// which provides the packet capture driver and wpcap.dll, isn't installed.
// tcpview can't bundle it: Npcap's free license doesn't allow
// redistribution.
var ErrNpcapMissing = errors.New("capture: live capture on Windows needs Npcap - install it from https://npcap.com/#download, then run tcpview again")

// ListInterfaces enumerates local capture-able interfaces via libpcap. This
// requires libpcap to be installed but not elevated privileges (listing
// interfaces, unlike opening one for capture, doesn't need root/CAP_NET_RAW
// on Linux).
func ListInterfaces() ([]Interface, error) {
	devs, err := pcap.FindAllDevs()
	if err != nil {
		return nil, explainListError(runtime.GOOS, err)
	}

	out := make([]Interface, 0, len(devs))
	for _, d := range devs {
		addrs := make([]string, 0, len(d.Addresses))
		for _, a := range d.Addresses {
			if a.IP != nil {
				addrs = append(addrs, a.IP.String())
			}
		}
		out = append(out, Interface{
			Name:        d.Name,
			Description: d.Description,
			Addresses:   addrs,
		})
	}
	return out, nil
}

// explainListError turns a FindAllDevs failure into something actionable.
// gopacket reports a missing Npcap only as "couldn't load wpcap.dll".
func explainListError(goos string, err error) error {
	if goos == "windows" && strings.Contains(err.Error(), "wpcap.dll") {
		return fmt.Errorf("%w (%v)", ErrNpcapMissing, err)
	}
	return fmt.Errorf("capture: list interfaces: %w", err)
}

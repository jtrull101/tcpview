package capture

import (
	"fmt"

	"github.com/gopacket/gopacket/pcap"
)

// Interface describes one local network interface available for capture.
type Interface struct {
	Name        string
	Description string
	Addresses   []string
}

// ListInterfaces enumerates local capture-able interfaces via libpcap. This
// requires libpcap to be installed but not elevated privileges (listing
// interfaces, unlike opening one for capture, doesn't need root/CAP_NET_RAW
// on Linux).
func ListInterfaces() ([]Interface, error) {
	devs, err := pcap.FindAllDevs()
	if err != nil {
		return nil, fmt.Errorf("capture: list interfaces: %w", err)
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

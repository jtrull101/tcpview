package capture

import (
	"fmt"
	"sync"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"

	"github.com/jtrull101/tcpview/internal/store"
)

// Options configures a Live capture.
type Options struct {
	Iface       string
	Promiscuous bool
	SnapLen     int32         // 0 => sensible default
	Timeout     time.Duration // 0 => sensible default
}

func (o Options) withDefaults() Options {
	if o.SnapLen <= 0 {
		o.SnapLen = 262144
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Second
	}
	return o
}

// Live captures packets from a local network interface via libpcap. It
// implements Source. Opening one requires elevated privileges on most
// platforms (root or CAP_NET_RAW+CAP_NET_ADMIN on Linux).
type Live struct {
	iface     string
	packets   chan store.Packet
	errs      chan error
	done      chan struct{}
	closeOnce sync.Once
	closeFn   func()
}

// NewLive opens iface for live capture and starts decoding in the
// background. Callers must call Close when done to release the pcap handle.
func NewLive(opts Options) (*Live, error) {
	if opts.Iface == "" {
		return nil, fmt.Errorf("capture: interface name required")
	}
	opts = opts.withDefaults()

	handle, err := pcap.OpenLive(opts.Iface, opts.SnapLen, opts.Promiscuous, opts.Timeout)
	if err != nil {
		return nil, fmt.Errorf("capture: open %s: %w", opts.Iface, err)
	}

	return newLiveFromSource(handle, handle.LinkType(), opts.Iface, handle.Close), nil
}

// newLiveFromSource builds a Live around any gopacket.PacketDataSource,
// deliberately decoupled from *pcap.Handle specifically (gopacket.PacketSource
// accepts the narrower PacketDataSource interface - just ReadPacketData) so
// the read loop's logic is unit-testable against a fake source instead of a
// real libpcap handle, which needs CAP_NET_RAW/root to open. See
// live_source_test.go.
func newLiveFromSource(src gopacket.PacketDataSource, linkType layers.LinkType, iface string, closeFn func()) *Live {
	l := &Live{
		iface:   iface,
		packets: make(chan store.Packet, 1024),
		errs:    make(chan error, 64),
		done:    make(chan struct{}),
		closeFn: closeFn,
	}
	go l.loop(src, linkType)
	return l
}

// loop reads decoded packets until done fires or the underlying source ends.
//
// Note: gopacket's own internal read loop (driven by NewPacketSource below)
// only stops retrying on a specific allowlist of "unrecoverable" errors
// (io.EOF, io.ErrUnexpectedEOF, a closed-file error, etc - see
// packetsToChannel in gopacket/packet.go); an error type it doesn't
// recognize gets retried in a tight loop indefinitely instead of surfacing.
// In practice real pcap errors (interface removed, etc) tend to match one
// of the recognized patterns, but this is a gopacket characteristic worth
// knowing about, not something this package papers over.
func (l *Live) loop(src gopacket.PacketDataSource, linkType layers.LinkType) {
	defer close(l.packets)
	defer close(l.errs)

	packetSrc := gopacket.NewPacketSource(src, linkType)
	pkts := packetSrc.Packets()
	for {
		select {
		case <-l.done:
			return
		case raw, ok := <-pkts:
			if !ok {
				return
			}
			p, err := Decode(raw, l.iface)
			if err != nil {
				select {
				case l.errs <- err:
				default: // errs is advisory; don't block capture on a slow/absent reader
				}
				continue
			}
			p.LinkType, p.HasLinkType = uint16(linkType), true
			select {
			case l.packets <- p:
			case <-l.done:
				return
			}
		}
	}
}

func (l *Live) Packets() <-chan store.Packet { return l.packets }
func (l *Live) Errors() <-chan error         { return l.errs }

func (l *Live) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
		if l.closeFn != nil {
			l.closeFn()
		}
	})
	return nil
}

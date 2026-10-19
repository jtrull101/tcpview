// Package capture turns live network traffic into a stream of
// store.Packet values, either from a local interface via libpcap or (see
// internal/remote) from a remote host over SSH.
package capture

import "github.com/jtrull101/tcpview/internal/store"

// Source produces a stream of decoded packets. Both local live capture
// (Live, in this package) and remote SSH capture (internal/remote.SSH)
// implement this, so everything downstream (the store-owning goroutine, the
// UI, main) is agnostic to where packets actually come from.
type Source interface {
	// Packets returns a channel of decoded packets. The channel is closed
	// once the source stops, whether from an error or from Close.
	Packets() <-chan store.Packet

	// Errors returns a channel of non-fatal errors encountered while
	// capturing - e.g. a single malformed packet shouldn't kill a live
	// capture, so decode failures are reported here rather than aborting.
	// Closed alongside Packets().
	Errors() <-chan error

	// Close stops capture and releases underlying resources (a pcap handle,
	// an SSH session, etc). Idempotent: calling Close more than once is
	// safe and returns the first error, if any.
	Close() error
}

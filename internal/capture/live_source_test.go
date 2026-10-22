package capture

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// fakePacketDataSource is a gopacket.PacketDataSource double standing in
// for a real *pcap.Handle, so Live's read loop is unit-testable without a
// libpcap handle (which needs CAP_NET_RAW/root to open) - mirrors the fake
// command runner approach used in internal/remote.
type fakePacketDataSource struct {
	mu      sync.Mutex
	packets [][]byte
	idx     int
	// afterErr is returned once packets is exhausted. io.EOF (the zero
	// value) means "clean end of stream", matching a finite pcap file.
	afterErr error
	// blockForever makes ReadPacketData block until blockRelease closes,
	// simulating a live interface with no more traffic right now.
	blockForever bool
	blockRelease chan struct{}
}

func (f *fakePacketDataSource) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	f.mu.Lock()
	if f.idx < len(f.packets) {
		d := f.packets[f.idx]
		f.idx++
		f.mu.Unlock()
		return d, gopacket.CaptureInfo{Timestamp: time.Now(), Length: len(d), CaptureLength: len(d)}, nil
	}
	block, release := f.blockForever, f.blockRelease
	err := f.afterErr
	f.mu.Unlock()

	if block {
		<-release
		return nil, gopacket.CaptureInfo{}, io.EOF
	}
	if err == nil {
		err = io.EOF
	}
	return nil, gopacket.CaptureInfo{}, err
}

func ethTCPFrame(t *testing.T) []byte {
	t.Helper()
	return serializeTCPBytes(t)
}

// --- positive ---

func TestLiveFromSource_DecodesAllPackets(t *testing.T) {
	t.Parallel()
	frame := ethTCPFrame(t)
	src := &fakePacketDataSource{packets: [][]byte{frame, frame, frame}}

	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	defer l.Close()

	got := 0
	deadline := time.After(5 * time.Second)
	for got < 3 {
		select {
		case p, ok := <-l.Packets():
			if !ok {
				t.Fatalf("Packets() closed after %d/3 packets", got)
			}
			if p.Proto == "" {
				t.Fatal("decoded packet has empty Proto")
			}
			if !p.HasLinkType || p.LinkType != uint16(layers.LinkTypeEthernet) {
				t.Fatalf("packet link type = %d (known=%v), want Ethernet recorded for export", p.LinkType, p.HasLinkType)
			}
			got++
		case <-deadline:
			t.Fatalf("timed out after %d/3 packets", got)
		}
	}
}

func TestLiveFromSource_ErrorsChannelIsAccessible(t *testing.T) {
	t.Parallel()
	src := &fakePacketDataSource{}
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	defer l.Close()
	if l.Errors() == nil {
		t.Fatal("Errors() returned a nil channel")
	}
}

func TestLiveFromSource_CallsCloseFn(t *testing.T) {
	t.Parallel()
	src := &fakePacketDataSource{}
	called := make(chan struct{})
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", func() { close(called) })

	if err := l.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("closeFn was not called")
	}
}

// --- negative ---

func TestLiveFromSource_CleanEOFClosesChannelsWithoutError(t *testing.T) {
	t.Parallel()
	src := &fakePacketDataSource{} // empty, immediate EOF
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	defer l.Close()

	select {
	case _, ok := <-l.Packets():
		if ok {
			t.Fatal("expected Packets() to be closed on EOF, got a value instead")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Packets() to close on EOF")
	}
}

func TestLiveFromSource_ReadErrorClosesChannels(t *testing.T) {
	t.Parallel()
	// gopacket's packetsToChannel only stops retrying on a specific
	// allowlist of "unrecoverable" errors (io.ErrUnexpectedEOF among them) -
	// an arbitrary error type gets retried forever instead, which is a real
	// gopacket characteristic worth documenting here rather than a bug in
	// this package. Use a recognized one so this test actually exercises
	// the close-on-error path.
	src := &fakePacketDataSource{afterErr: io.ErrUnexpectedEOF}
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	defer l.Close()

	select {
	case _, ok := <-l.Packets():
		if ok {
			t.Fatal("expected Packets() to be closed on a read error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Packets() to close on a read error")
	}
}

func TestLiveFromSource_NilCloseFnDoesNotPanic(t *testing.T) {
	t.Parallel()
	src := &fakePacketDataSource{}
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	if err := l.Close(); err != nil {
		t.Fatalf("Close() with nil closeFn error: %v", err)
	}
}

// --- load ---

func TestLiveFromSource_Load_ManyPackets(t *testing.T) {
	t.Parallel()
	frame := ethTCPFrame(t)
	const n = 20_000
	frames := make([][]byte, n)
	for i := range frames {
		frames[i] = frame
	}
	src := &fakePacketDataSource{packets: frames}
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	defer l.Close()

	got := 0
	deadline := time.After(30 * time.Second)
	for got < n {
		select {
		case _, ok := <-l.Packets():
			if !ok {
				t.Fatalf("Packets() closed early after %d/%d", got, n)
			}
			got++
		case <-deadline:
			t.Fatalf("timed out after %d/%d packets", got, n)
		}
	}
}

// --- stress ---

func TestLiveFromSource_Stress_CloseReturnsPromptlyEvenIfSourceIsBlocked(t *testing.T) {
	t.Parallel()
	src := &fakePacketDataSource{blockForever: true, blockRelease: make(chan struct{})}
	defer close(src.blockRelease) // avoid leaking the fake source's goroutine past the test

	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)

	// loop()'s own select races <-l.done against the packet channel, so
	// Close() must return promptly even though the underlying source never
	// unblocks on its own - real production additionally wires closeFn to
	// handle.Close() to unstick the OS-level read too, but the caller-facing
	// Close() shouldn't depend on that to avoid hanging.
	closeDone := make(chan struct{})
	go func() {
		l.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return promptly while the underlying source was blocked")
	}
}

// --- chaos ---

func TestLiveFromSource_Chaos_RapidOpenCloseCycles(t *testing.T) {
	t.Parallel()
	frame := ethTCPFrame(t)
	for i := 0; i < 100; i++ {
		src := &fakePacketDataSource{packets: [][]byte{frame}}
		l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
		if err := l.Close(); err != nil {
			t.Fatalf("iteration %d: Close() error: %v", i, err)
		}
		// Closing immediately after opening races the loop goroutine against
		// done being closed - just confirm no panic/hang across many cycles.
	}
}

func TestLiveFromSource_Chaos_GarbageDataDoesNotStallDecoding(t *testing.T) {
	t.Parallel()
	frame := ethTCPFrame(t)
	packets := [][]byte{
		{0x00}, // too short to decode meaningfully
		{},
		frame,
		{0xFF, 0xFF, 0xFF, 0xFF},
		frame,
	}
	src := &fakePacketDataSource{packets: packets}
	l := newLiveFromSource(src, layers.LinkTypeEthernet, "eth0", nil)
	defer l.Close()

	// Garbage frames should never reach Decode as an error (Decode only
	// errors on a literal nil gopacket.Packet - see decode.go) so every
	// entry, garbage or not, should surface as a packet; just confirm we
	// drain the full count without the loop stalling or panicking.
	got := 0
	deadline := time.After(5 * time.Second)
	for got < len(packets) {
		select {
		case _, ok := <-l.Packets():
			if !ok {
				t.Fatalf("Packets() closed early after %d/%d", got, len(packets))
			}
			got++
		case <-deadline:
			t.Fatalf("timed out after %d/%d packets", got, len(packets))
		}
	}
}

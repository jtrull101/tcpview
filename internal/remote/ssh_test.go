package remote

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

// buildPcapFile writes n synthetic TCP packets to a pcap file on disk and
// returns its path, standing in for "a remote machine's tcpdump output"
// without needing a real SSH session or remote host.
func buildPcapFile(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.pcap")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create pcap file: %v", err)
	}
	defer f.Close()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65536, layers.LinkTypeEthernet); err != nil {
		t.Fatalf("write pcap header: %v", err)
	}

	eth := &layers.Ethernet{
		SrcMAC: net.HardwareAddr{0, 1, 2, 3, 4, 5}, DstMAC: net.HardwareAddr{6, 7, 8, 9, 10, 11},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolTCP,
		SrcIP: net.ParseIP("10.1.1.1").To4(), DstIP: net.ParseIP("10.1.1.2").To4()}
	tcp := &layers.TCP{SrcPort: 2222, DstPort: 80, SYN: true}
	_ = tcp.SetNetworkLayerForChecksum(ip)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	for i := 0; i < n; i++ {
		if err := buf.Clear(); err != nil {
			t.Fatalf("clear buffer: %v", err)
		}
		if err := gopacket.SerializeLayers(buf, opts, eth, ip, tcp); err != nil {
			t.Fatalf("serialize packet %d: %v", i, err)
		}
		ci := gopacket.CaptureInfo{Timestamp: time.Now(), CaptureLength: len(buf.Bytes()), Length: len(buf.Bytes())}
		if err := w.WritePacket(ci, buf.Bytes()); err != nil {
			t.Fatalf("write packet %d: %v", i, err)
		}
	}
	return path
}

func catFactory(path string) func() *exec.Cmd {
	return func() *exec.Cmd { return exec.Command("cat", path) }
}

func failFactory() func() *exec.Cmd {
	return func() *exec.Cmd { return exec.Command("false") }
}

// sleepyFactory simulates a remote session that writes nothing further and
// never exits on its own - the only way to end it is to kill the process,
// exactly like a stalled/hung SSH connection.
func sleepyFactory() func() *exec.Cmd {
	return func() *exec.Cmd { return exec.Command("sleep", "300") }
}

func collectPackets(t *testing.T, s *SSH, want int, timeout time.Duration) int {
	t.Helper()
	got := 0
	deadline := time.After(timeout)
	for got < want {
		select {
		case _, ok := <-s.Packets():
			if !ok {
				return got
			}
			got++
		case <-deadline:
			t.Fatalf("timed out after collecting %d/%d packets", got, want)
		}
	}
	return got
}

func drainErrors(s *SSH, stop <-chan struct{}) {
	go func() {
		for {
			select {
			case _, ok := <-s.Errors():
				if !ok {
					return
				}
			case <-stop:
				return
			}
		}
	}()
}

// --- positive ---

func TestSSH_DecodesPacketsFromFakeRemoteCommand(t *testing.T) {
	t.Parallel()
	path := buildPcapFile(t, 5)
	s, err := New(Options{Host: "fake-host", Iface: "eth0", cmdFactory: catFactory(path)})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	n := collectPackets(t, s, 5, 5*time.Second)
	if n != 5 {
		t.Fatalf("collected %d packets, want 5", n)
	}
}

// --- negative ---

func TestSSH_MissingHostErrors(t *testing.T) {
	t.Parallel()
	_, err := New(Options{Iface: "eth0"})
	if err == nil {
		t.Fatal("New() with empty Host returned no error")
	}
}

func TestSSH_MissingIfaceErrors(t *testing.T) {
	t.Parallel()
	_, err := New(Options{Host: "fake-host"})
	if err == nil {
		t.Fatal("New() with empty Iface returned no error")
	}
}

func TestSSH_CommandFailureReportsErrorAndRetries(t *testing.T) {
	t.Parallel()
	s, err := New(Options{
		Host: "fake-host", Iface: "eth0", cmdFactory: failFactory(),
		Backoff: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	select {
	case _, ok := <-s.Errors():
		if !ok {
			t.Fatal("Errors() channel closed before reporting a failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a reported error from a failing command")
	}
}

// --- load ---

func TestSSH_Load_ManyPacketsStreamed(t *testing.T) {
	t.Parallel()
	const n = 5000
	path := buildPcapFile(t, n)
	s, err := New(Options{Host: "fake-host", Iface: "eth0", cmdFactory: catFactory(path)})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	got := collectPackets(t, s, n, 30*time.Second)
	if got != n {
		t.Fatalf("collected %d packets, want %d", got, n)
	}
}

// --- stress ---

func TestSSH_Stress_RapidReconnectDoesNotHang(t *testing.T) {
	t.Parallel()
	s, err := New(Options{
		Host: "fake-host", Iface: "eth0", cmdFactory: failFactory(),
		Backoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stop := make(chan struct{})
	drainErrors(s, stop)
	time.Sleep(200 * time.Millisecond) // let many rapid fail/backoff cycles happen

	closeDone := make(chan struct{})
	go func() {
		s.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return after rapid reconnect churn - possible hang/leak")
	}
	close(stop)
}

// --- chaos ---

func TestSSH_Chaos_CloseDuringBlockedReadUnsticksPromptly(t *testing.T) {
	t.Parallel()
	s, err := New(Options{Host: "fake-host", Iface: "eth0", cmdFactory: sleepyFactory()})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Give runOnce a moment to actually be blocked inside pcapgo.NewReader /
	// ReadPacketData against the sleeping process's stdout.
	time.Sleep(100 * time.Millisecond)

	closeDone := make(chan struct{})
	go func() {
		s.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Close() did not return while a read was blocked on a stalled remote command")
	}
}

func TestSSH_Chaos_ReconnectsAfterMidStreamDrop(t *testing.T) {
	t.Parallel()

	full := buildPcapFile(t, 10)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read full pcap: %v", err)
	}
	truncatedPath := filepath.Join(t.TempDir(), "truncated.pcap")
	// Cut off mid-stream (keep the header but only part of the packets) to
	// simulate a connection that drops partway through.
	cut := len(data) / 2
	if err := os.WriteFile(truncatedPath, data[:cut], 0o644); err != nil {
		t.Fatalf("write truncated pcap: %v", err)
	}

	calls := 0
	factory := func() *exec.Cmd {
		calls++
		if calls == 1 {
			return exec.Command("cat", truncatedPath)
		}
		return exec.Command("cat", full)
	}

	s, err := New(Options{
		Host: "fake-host", Iface: "eth0", cmdFactory: factory,
		Backoff: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	// The first (truncated) session won't yield all 10 packets by itself,
	// but after it ends the loop should reconnect and the second (full)
	// session should let us accumulate at least 10 packets total.
	got := collectPackets(t, s, 10, 10*time.Second)
	if got < 10 {
		t.Fatalf("collected %d packets across reconnect, want at least 10", got)
	}
}

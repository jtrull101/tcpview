// Package remote captures traffic on a remote host over SSH by shelling out
// to the remote's own tcpdump and decoding the piped pcap stream locally.
// This deliberately avoids requiring a cross-compiled tcpview agent on every
// remote box - it only needs existing SSH access and tcpdump, both already
// near-universal on Linux hosts.
package remote

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/pcapgo"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/store"
)

// maxStderrCapture bounds how much of the remote command's stderr gets kept
// for error messages - plenty for a diagnostic like "permission denied" or
// "command not found", not enough for a chatty/hostile remote to grow
// unbounded memory over a long-lived reconnect loop.
const maxStderrCapture = 4096

// boundedBuffer is an io.Writer that keeps only the first maxLen bytes
// written to it, silently discarding the rest - used to capture just
// enough of a subprocess's stderr for a useful error message, without
// letting a long-running or chatty remote process grow it unbounded.
type boundedBuffer struct {
	buf    bytes.Buffer
	maxLen int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.maxLen - b.buf.Len()
	if remaining > 0 {
		if len(p) < remaining {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:remaining])
		}
	}
	return len(p), nil // report success regardless, so the pipe never backs up
}

func (b *boundedBuffer) String() string {
	return strings.TrimSpace(b.buf.String())
}

// Options configures an SSH-based remote capture source.
type Options struct {
	Host        string        // e.g. "user@host", passed straight to the ssh binary
	Iface       string        // remote interface name to capture on
	SSHBinary   string        // default "ssh"
	TcpdumpPath string        // remote tcpdump command, default "tcpdump"; may carry a prefix, e.g. "sudo -n tcpdump"
	Backoff     time.Duration // initial reconnect backoff, default 1s
	MaxBackoff  time.Duration // reconnect backoff cap, default 30s

	// cmdFactory overrides how the remote capture command is built.
	// Unexported: production code always derives it from the fields above;
	// tests substitute a fake command here to exercise reconnect/chaos
	// scenarios without a real remote host, per the testing policy in
	// CLAUDE.md.
	cmdFactory func() *exec.Cmd
}

func (o Options) withDefaults() Options {
	if o.SSHBinary == "" {
		o.SSHBinary = "ssh"
	}
	if o.TcpdumpPath == "" {
		o.TcpdumpPath = "tcpdump"
	}
	if o.Backoff <= 0 {
		o.Backoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	return o
}

// captureScript runs on the remote host under sh, with the interface as
// $1 and the tcpdump command as $2 (deliberately unquoted when run, so a
// prefix like "sudo -n tcpdump" splits into words).
//
// It excludes this SSH session's own connection from the capture: the
// pcap stream flows back over that connection, so on the interface SSH
// uses, capturing it would show tcpview's own traffic - and every
// forwarded packet would generate more packets to forward. sshd sets
// $SSH_CONNECTION to "client_ip client_port server_ip server_port" as
// the remote side sees them (so NAT and jump hosts are already
// accounted for); if it's somehow unset, capture runs unfiltered rather
// than failing.
const captureScript = `set -- "$1" "$2" $SSH_CONNECTION; ` +
	`if [ -n "$4" ]; then exec $2 -i "$1" -U -w - "not (host $3 and port $4)"; ` +
	`else exec $2 -i "$1" -U -w -; fi`

func (o Options) buildCmd() *exec.Cmd {
	if o.cmdFactory != nil {
		return o.cmdFactory()
	}
	return exec.Command(o.SSHBinary, o.captureArgs()...)
}

// captureArgs is the ssh argument list for a capture: the host, then the
// remote command. ssh joins the command's words with spaces and hands the
// result to the remote user's login shell, which may not be POSIX (fish,
// csh), so the script runs under an explicit sh with every value passed
// as a single-quoted positional argument rather than spliced into it.
func (o Options) captureArgs() []string {
	return []string{o.Host, "sh", "-c", shellQuote(captureScript), "tcpview", shellQuote(o.Iface), shellQuote(o.TcpdumpPath)}
}

// shellQuote single-quotes s for a remote shell, the portable way:
// close the quote, emit an escaped ', reopen.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SSH captures packets from a remote host by running tcpdump over an SSH
// session and decoding the piped pcap stream. It implements capture.Source
// and reconnects with exponential backoff if the session ends (dropped
// connection, remote tcpdump exiting, etc) until Close is called.
type SSH struct {
	opts      Options
	packets   chan store.Packet
	errs      chan error
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

var _ capture.Source = (*SSH)(nil)

// New starts capturing from opts.Host/opts.Iface in the background. Callers
// must call Close when done.
func New(opts Options) (*SSH, error) {
	if opts.Host == "" {
		return nil, fmt.Errorf("remote: host required")
	}
	if opts.Iface == "" {
		return nil, fmt.Errorf("remote: interface required")
	}
	opts = opts.withDefaults()

	s := &SSH{
		opts:    opts,
		packets: make(chan store.Packet, 1024),
		errs:    make(chan error, 64),
		done:    make(chan struct{}),
	}
	s.wg.Add(1)
	go s.runLoop()
	return s, nil
}

func (s *SSH) runLoop() {
	defer s.wg.Done()
	defer close(s.packets)
	defer close(s.errs)

	backoff := s.opts.Backoff
	for {
		select {
		case <-s.done:
			return
		default:
		}

		err := s.runOnce()

		select {
		case <-s.done:
			return
		default:
		}
		if err != nil {
			select {
			case s.errs <- fmt.Errorf("remote: session ended: %w", err):
			default: // errs is advisory; don't block reconnect on a slow/absent reader
			}
		}

		select {
		case <-s.done:
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > s.opts.MaxBackoff {
			backoff = s.opts.MaxBackoff
		}
	}
}

// runOnce runs a single ssh+tcpdump session to completion (clean EOF,
// command failure, or Close) and returns the reason it ended (nil for a
// clean EOF).
func (s *SSH) runOnce() error {
	cmd := s.opts.buildCmd()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr := &boundedBuffer{maxLen: maxStderrCapture}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	// If Close() fires while we're blocked reading, kill the subprocess to
	// unstick the read rather than hanging forever. stopWatch is closed on
	// every exit path below so this goroutine never outlives runOnce.
	//
	// Deliberately NOT calling cmd.Wait() concurrently here: per os/exec's
	// docs, Wait closes the StdoutPipe once the process exits, and calling
	// it before all reads from that pipe have completed is a race that
	// intermittently truncates the stream mid-read. Wait is called once,
	// synchronously, strictly after pump() below has finished reading.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-s.done:
			_ = cmd.Process.Kill()
		case <-stopWatch:
		}
	}()

	r, err := pcapgo.NewReader(bufio.NewReader(stdout))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return withStderr(fmt.Errorf("pcap header: %w", err), stderr)
	}

	readErr := s.pump(r)
	waitErr := cmd.Wait()
	if readErr != nil {
		return withStderr(readErr, stderr)
	}
	return withStderr(waitErr, stderr)
}

// withStderr appends the remote command's captured stderr to err, if both
// are non-empty - the raw error (an EOF, an exit status) rarely says why a
// remote capture actually failed; the remote tcpdump/ssh's own stderr
// (permission denied, no such device, host key rejected, etc) usually does.
func withStderr(err error, stderr *boundedBuffer) error {
	if err == nil {
		return nil
	}
	if msg := stderr.String(); msg != "" {
		return fmt.Errorf("%w (remote stderr: %s)", err, msg)
	}
	return err
}

func (s *SSH) pump(r *pcapgo.Reader) error {
	for {
		select {
		case <-s.done:
			return nil
		default:
		}

		data, ci, err := r.ReadPacketData()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read packet: %w", err)
		}

		raw := gopacket.NewPacket(data, r.LinkType(), gopacket.Default)
		raw.Metadata().CaptureInfo = ci

		p, decErr := capture.Decode(raw, s.opts.Iface)
		if decErr != nil {
			select {
			case s.errs <- decErr:
			default:
			}
			continue
		}
		p.LinkType, p.HasLinkType = uint16(r.LinkType()), true
		select {
		case s.packets <- p:
		case <-s.done:
			return nil
		}
	}
}

func (s *SSH) Packets() <-chan store.Packet { return s.packets }
func (s *SSH) Errors() <-chan error         { return s.errs }

func (s *SSH) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
	})
	s.wg.Wait()
	return nil
}

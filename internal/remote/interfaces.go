package remote

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/jtrull101/tcpview/internal/capture"
)

// maxListOutput bounds how much `tcpdump -D` output ListInterfaces will
// read - a real interface list is a few KiB at most, so anything past this
// is a misbehaving remote, not more interfaces worth showing.
const maxListOutput = 1 << 20

func (o Options) buildListCmd() *exec.Cmd {
	if o.cmdFactory != nil {
		return o.cmdFactory()
	}
	return exec.Command(o.SSHBinary, o.Host, o.TcpdumpPath, "-D")
}

// ListInterfaces enumerates the capturable interfaces on opts.Host by
// running the remote's own `tcpdump -D` over SSH - the same "just SSH and
// tcpdump" requirement as capture itself, no agent needed. opts.Iface is
// ignored.
func ListInterfaces(opts Options) ([]capture.Interface, error) {
	if opts.Host == "" {
		return nil, fmt.Errorf("remote: host required")
	}
	opts = opts.withDefaults()

	cmd := opts.buildListCmd()
	stdout := &boundedBuffer{maxLen: maxListOutput}
	stderr := &boundedBuffer{maxLen: maxStderrCapture}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, withStderr(fmt.Errorf("remote: list interfaces on %s: %w", opts.Host, err), stderr)
	}

	ifaces := parseTcpdumpList(stdout.buf.Bytes())
	if len(ifaces) == 0 {
		return nil, withStderr(fmt.Errorf("remote: list interfaces on %s: tcpdump -D reported none", opts.Host), stderr)
	}
	return ifaces, nil
}

// parseTcpdumpList parses `tcpdump -D` output, one interface per line:
//
//	1.eth0 [Up, Running, Connected]
//	2.any (Pseudo-device that captures on all interfaces) [Up, Running]
//	3.nflog (Linux netfilter log (NFLOG) interface) [none]
//
// Older tcpdumps omit the bracketed flags, and some omit descriptions
// entirely, so both are optional. Lines that don't look like an entry are
// skipped rather than failing the whole list.
func parseTcpdumpList(out []byte) []capture.Interface {
	var ifaces []capture.Interface
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		num, rest, ok := strings.Cut(line, ".")
		if !ok || num == "" || strings.Trim(num, "0123456789") != "" {
			continue
		}

		// Trailing flags, e.g. "[Up, Running]".
		if strings.HasSuffix(rest, "]") {
			if i := strings.LastIndex(rest, " ["); i >= 0 {
				rest = rest[:i]
			}
		}

		name, desc, _ := strings.Cut(rest, " ")
		if name == "" {
			continue
		}
		desc = strings.TrimSpace(desc)
		if strings.HasPrefix(desc, "(") && strings.HasSuffix(desc, ")") {
			desc = desc[1 : len(desc)-1]
		}
		ifaces = append(ifaces, capture.Interface{Name: name, Description: desc})
	}
	return ifaces
}

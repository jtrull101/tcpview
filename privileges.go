package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// capNetRaw is the Linux capability bit number for CAP_NET_RAW (see
// capability(7)) - the capability live packet capture needs.
const capNetRaw = 13

// parseCapEff reports whether the given capability bit is set in a
// /proc/self/status-formatted "CapEff:" line read from r. Split out from
// hasNetRawCapability so the actual bitmask logic is unit-testable without
// needing a real /proc filesystem or any particular privilege level.
func parseCapEff(r io.Reader, bit uint) (bool, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false, fmt.Errorf("privileges: unexpected CapEff line %q", line)
		}
		mask, err := strconv.ParseUint(fields[1], 16, 64)
		if err != nil {
			return false, fmt.Errorf("privileges: parse CapEff %q: %w", fields[1], err)
		}
		return mask&(1<<bit) != 0, nil
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, fmt.Errorf("privileges: CapEff line not found")
}

func hasNetRawCapability() (bool, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }() // read-only handle; a close error here is inconsequential
	return parseCapEff(f, capNetRaw)
}

// checkCapturePrivileges returns a clear, actionable error if this process
// almost certainly can't open a live capture handle, so main fails fast
// with useful guidance instead of surfacing a confusing low-level pcap
// error after the user has already picked an interface.
//
// It deliberately only blocks when it's confident capture will fail (root
// check succeeds trivially; the /proc-based capability check is concrete).
// If capability detection itself fails (non-Linux, /proc unavailable), it
// doesn't block - better to let the real pcap.OpenLive call surface the
// actual error than to guess wrong here.
func checkCapturePrivileges() error {
	if os.Geteuid() == 0 {
		return nil
	}
	ok, err := hasNetRawCapability()
	if err != nil {
		return nil
	}
	if !ok {
		return fmt.Errorf(`tcpview: live capture needs elevated privileges.
Run with sudo, or grant the binary capture capability once:
  sudo setcap cap_net_raw,cap_net_admin=eip %s`, os.Args[0])
	}
	return nil
}

package remote

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSSH writes an "ssh" stand-in that behaves like the real client as
// far as the remote command goes: it drops the host argument, joins the
// rest with spaces, and runs that through a shell - with SSH_CONNECTION
// set the way sshd sets it (empty conn leaves it unset). It returns the
// script's path.
func fakeSSH(t *testing.T, conn string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	env := ""
	if conn != "" {
		env = fmt.Sprintf("SSH_CONNECTION=%q ", conn)
	}
	script := "#!/bin/sh\nshift\nexec env " + env + "sh -c \"$*\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	return path
}

// fakeTcpdump writes a "tcpdump" stand-in that records its argv, one per
// line, to the returned args file, then streams pcapPath like tcpdump -w -.
func fakeTcpdump(t *testing.T, pcapPath string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin, argsFile = filepath.Join(dir, "tcpdump"), filepath.Join(dir, "args")
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\"; done > %q\nexec cat %q\n", argsFile, pcapPath)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tcpdump: %v", err)
	}
	return bin, argsFile
}

// runCapture starts a real SSH source (the production command line, no
// cmdFactory) through the fakes and waits for its first packet.
func runCapture(t *testing.T, opts Options) {
	t.Helper()
	s, err := New(opts)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	defer s.Close()
	select {
	case <-s.Packets():
	case err := <-s.Errors():
		t.Fatalf("remote capture failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no packet through the fake ssh/tcpdump")
	}
}

func readArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read tcpdump args: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// --- positive ---

func TestSSH_ExcludesOwnSSHConnectionFromCapture(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	runCapture(t, Options{Host: "h", Iface: "eth0", SSHBinary: fakeSSH(t, "203.0.113.7 51234 198.51.100.1 22"), TcpdumpPath: bin})

	got := readArgs(t, argsFile)
	want := []string{"-i", "eth0", "-U", "-w", "-", "not (host 203.0.113.7 and port 51234)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tcpdump argv = %q, want %q", got, want)
	}
}

func TestSSH_IPv6ClientConnectionExcluded(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	runCapture(t, Options{Host: "h", Iface: "eth0", SSHBinary: fakeSSH(t, "2001:db8::7 40000 2001:db8::1 22"), TcpdumpPath: bin})
	if got := readArgs(t, argsFile); got[len(got)-1] != "not (host 2001:db8::7 and port 40000)" {
		t.Fatalf("filter = %q, want the IPv6 client excluded", got[len(got)-1])
	}
}

func TestSSH_TcpdumpCommandPrefixSplitsIntoWords(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	// "env" stands in for "sudo -n": a prefix command that execs the rest.
	runCapture(t, Options{Host: "h", Iface: "eth0", SSHBinary: fakeSSH(t, "10.0.0.5 5000 10.0.0.1 22"), TcpdumpPath: "env " + bin})
	if got := readArgs(t, argsFile); got[0] != "-i" || got[1] != "eth0" {
		t.Fatalf("tcpdump argv = %q, want the prefix to run tcpdump with the usual flags", got)
	}
}

// --- negative ---

func TestSSH_NoSSHConnectionRunsUnfiltered(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	runCapture(t, Options{Host: "h", Iface: "eth0", SSHBinary: fakeSSH(t, ""), TcpdumpPath: bin})
	if got := readArgs(t, argsFile); strings.Join(got, " ") != "-i eth0 -U -w -" {
		t.Fatalf("tcpdump argv without SSH_CONNECTION = %q, want an unfiltered capture", got)
	}
}

// --- chaos ---

func TestSSH_Chaos_HostileInterfaceNameStaysOneArgument(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "pwned")
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	iface := fmt.Sprintf("eth0'; touch %s; echo '$(touch %s) \"quoted\" *", marker, marker)
	runCapture(t, Options{Host: "h", Iface: iface, SSHBinary: fakeSSH(t, "10.0.0.5 5000 10.0.0.1 22"), TcpdumpPath: bin})

	if got := readArgs(t, argsFile); got[1] != iface {
		t.Fatalf("interface arg = %q, want it passed through verbatim as one argument %q", got[1], iface)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the interface name was executed by the remote shell")
	}
}

func TestShellQuote_RoundTripsThroughSh(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "plain", "it's", "''", `a\b`, "$(x) `y` $z", "new\nline", "'\"'\"'"} {
		out, err := execSh(t, "printf %s "+shellQuote(s))
		if err != nil || out != s {
			t.Errorf("sh printf %s = %q (err %v), want %q", shellQuote(s), out, err, s)
		}
	}
}

func execSh(t *testing.T, script string) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", "-c", script).Output()
	return string(out), err
}

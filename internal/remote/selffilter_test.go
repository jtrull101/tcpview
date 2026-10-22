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

// viaFakeSSH returns a cmdFactory that runs opts' real capture argument
// list (captureArgs, exactly what production hands the ssh binary)
// through a fake ssh client that does what the real one does with it -
// joins the remote command's words and runs them under sh - with
// SSH_CONNECTION set the way sshd sets it (empty conn leaves it unset).
func viaFakeSSH(opts Options, conn string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := helperCmd("ssh", opts.captureArgs()...)
		env := cmd.Env[:0:0]
		for _, kv := range cmd.Env {
			if !strings.HasPrefix(kv, "SSH_CONNECTION=") {
				env = append(env, kv)
			}
		}
		if conn != "" {
			env = append(env, "SSH_CONNECTION="+conn)
		}
		cmd.Env = env
		return cmd
	}
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

// runCapture starts an SSH source whose remote command is the production
// one, run through the fake ssh (see viaFakeSSH) as if sshd had set
// SSH_CONNECTION to conn, and waits for its first packet.
func runCapture(t *testing.T, opts Options, conn string) {
	t.Helper()
	requireSh(t)
	opts.cmdFactory = viaFakeSSH(opts, conn)
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
	runCapture(t, Options{Host: "h", Iface: "eth0", TcpdumpPath: bin}, "203.0.113.7 51234 198.51.100.1 22")

	got := readArgs(t, argsFile)
	want := []string{"-i", "eth0", "-U", "-w", "-", "not (host 203.0.113.7 and port 51234)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tcpdump argv = %q, want %q", got, want)
	}
}

func TestSSH_IPv6ClientConnectionExcluded(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	runCapture(t, Options{Host: "h", Iface: "eth0", TcpdumpPath: bin}, "2001:db8::7 40000 2001:db8::1 22")
	if got := readArgs(t, argsFile); got[len(got)-1] != "not (host 2001:db8::7 and port 40000)" {
		t.Fatalf("filter = %q, want the IPv6 client excluded", got[len(got)-1])
	}
}

func TestSSH_TcpdumpCommandPrefixSplitsIntoWords(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	// "env" stands in for "sudo -n": a prefix command that execs the rest.
	runCapture(t, Options{Host: "h", Iface: "eth0", TcpdumpPath: "env " + bin}, "10.0.0.5 5000 10.0.0.1 22")
	if got := readArgs(t, argsFile); got[0] != "-i" || got[1] != "eth0" {
		t.Fatalf("tcpdump argv = %q, want the prefix to run tcpdump with the usual flags", got)
	}
}

// --- negative ---

func TestSSH_NoSSHConnectionRunsUnfiltered(t *testing.T) {
	t.Parallel()
	bin, argsFile := fakeTcpdump(t, buildPcapFile(t, 1))
	runCapture(t, Options{Host: "h", Iface: "eth0", TcpdumpPath: bin}, "")
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
	runCapture(t, Options{Host: "h", Iface: iface, TcpdumpPath: bin}, "10.0.0.5 5000 10.0.0.1 22")

	if got := readArgs(t, argsFile); got[1] != iface {
		t.Fatalf("interface arg = %q, want it passed through verbatim as one argument %q", got[1], iface)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the interface name was executed by the remote shell")
	}
}

func TestShellQuote_RoundTripsThroughSh(t *testing.T) {
	t.Parallel()
	requireSh(t)
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

package remote

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// listFactory fakes the remote `tcpdump -D` by printing out to stdout (and
// errOut to stderr), then exiting with code.
func listFactory(t *testing.T, out, errOut string, code int) func() *exec.Cmd {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(outPath, []byte(out), 0o600); err != nil {
		t.Fatalf("write fake stdout: %v", err)
	}
	return func() *exec.Cmd {
		return helperCmd("script", outPath, errOut, fmt.Sprint(code))
	}
}

const modernTcpdumpD = `1.eth0 [Up, Running, Connected]
2.any (Pseudo-device that captures on all interfaces) [Up, Running]
3.lo [Up, Running, Loopback]
4.eth0.100 [Up, Running]
5.nflog (Linux netfilter log (NFLOG) interface) [none]
`

// --- positive ---

func TestParseTcpdumpList_ModernFormat(t *testing.T) {
	t.Parallel()
	got := parseTcpdumpList([]byte(modernTcpdumpD))
	want := []struct{ name, desc string }{
		{"eth0", ""},
		{"any", "Pseudo-device that captures on all interfaces"},
		{"lo", ""},
		{"eth0.100", ""},
		{"nflog", "Linux netfilter log (NFLOG) interface"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d interfaces, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Description != w.desc {
			t.Errorf("iface %d = %q/%q, want %q/%q", i, got[i].Name, got[i].Description, w.name, w.desc)
		}
	}
}

func TestParseTcpdumpList_LegacyFormats(t *testing.T) {
	t.Parallel()
	// Old tcpdumps: no flags, sometimes no description, CRLF from some ssh setups.
	got := parseTcpdumpList([]byte("1.eth0\r\n2.wlan0 (Wireless)\r\n"))
	if len(got) != 2 || got[0].Name != "eth0" || got[1].Name != "wlan0" || got[1].Description != "Wireless" {
		t.Fatalf("parsed %+v, want eth0 and wlan0 (Wireless)", got)
	}
}

func TestListInterfaces_ParsesRemoteOutput(t *testing.T) {
	t.Parallel()
	got, err := ListInterfaces(Options{Host: "h", cmdFactory: listFactory(t, modernTcpdumpD, "", 0)})
	if err != nil {
		t.Fatalf("ListInterfaces error: %v", err)
	}
	if len(got) != 5 || got[1].Name != "any" {
		t.Fatalf("ListInterfaces = %+v, want the 5 parsed interfaces", got)
	}
}

func TestBuildCmd_RunsCaptureScriptOverSSH(t *testing.T) {
	t.Parallel()
	o := Options{Host: "user@box", Iface: "eth0"}.withDefaults()
	cmd := o.buildCmd()
	if filepath.Base(cmd.Path) != "ssh" && cmd.Args[0] != "ssh" {
		t.Fatalf("capture runs %q, want ssh", cmd.Path)
	}
	if got, want := strings.Join(cmd.Args[1:], " "), strings.Join(o.captureArgs(), " "); got != want {
		t.Fatalf("ssh args = %q, want captureArgs %q", got, want)
	}
	if cmd.Args[1] != "user@box" || cmd.Args[2] != "sh" {
		t.Fatalf("ssh args = %q, want the host then an explicit sh", cmd.Args[1:])
	}
}

func TestBuildListCmd_RunsTcpdumpDOverSSH(t *testing.T) {
	t.Parallel()
	cmd := Options{Host: "user@box"}.withDefaults().buildListCmd()
	if got := strings.Join(cmd.Args, " "); got != "ssh user@box tcpdump -D" {
		t.Fatalf("list cmd = %q, want ssh user@box tcpdump -D", got)
	}
}

// --- negative ---

func TestListInterfaces_MissingHostErrors(t *testing.T) {
	t.Parallel()
	if _, err := ListInterfaces(Options{}); err == nil {
		t.Fatal("ListInterfaces with no host returned nil error")
	}
}

func TestListInterfaces_CommandFailureIncludesRemoteStderr(t *testing.T) {
	t.Parallel()
	_, err := ListInterfaces(Options{Host: "h", cmdFactory: listFactory(t, "", "bash: tcpdump: command not found", 127)})
	if err == nil || !strings.Contains(err.Error(), "command not found") {
		t.Fatalf("ListInterfaces err = %v, want it to carry the remote stderr", err)
	}
}

func TestListInterfaces_EmptyOutputErrors(t *testing.T) {
	t.Parallel()
	_, err := ListInterfaces(Options{Host: "h", cmdFactory: listFactory(t, "", "", 0)})
	if err == nil || !strings.Contains(err.Error(), "reported none") {
		t.Fatalf("ListInterfaces err = %v, want a no-interfaces error", err)
	}
}

func TestParseTcpdumpList_SkipsGarbageLines(t *testing.T) {
	t.Parallel()
	in := "tcpdump: WARNING: something\n\n.eth0\nx.eth1\n12.\n 3.lo \n1. (desc only)\n"
	got := parseTcpdumpList([]byte(in))
	if len(got) != 1 || got[0].Name != "lo" {
		t.Fatalf("parsed %+v, want only lo", got)
	}
}

// --- load ---

func TestParseTcpdumpList_Load_ManyInterfaces(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 1; i <= 10000; i++ {
		fmt.Fprintf(&b, "%d.veth%d (container %d) [Up, Running]\n", i, i, i)
	}
	start := time.Now()
	got := parseTcpdumpList([]byte(b.String()))
	if len(got) != 10000 || got[9999].Name != "veth10000" || got[9999].Description != "container 10000" {
		t.Fatalf("parsed %d interfaces (last %+v), want 10000", len(got), got[len(got)-1])
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("parsing 10000 interfaces took %v", d)
	}
}

// --- stress ---

func TestListInterfaces_Stress_OversizedOutputStaysBounded(t *testing.T) {
	t.Parallel()
	// Far past maxListOutput: the read is capped, and what fits still parses.
	line := "1.eth0 (" + strings.Repeat("x", 1000) + ")\n"
	out := strings.Repeat(line, 3*maxListOutput/len(line))
	got, err := ListInterfaces(Options{Host: "h", cmdFactory: listFactory(t, out, "", 0)})
	if err != nil {
		t.Fatalf("ListInterfaces error: %v", err)
	}
	if max := maxListOutput/len(line) + 1; len(got) > max {
		t.Fatalf("parsed %d interfaces from capped output, want at most %d", len(got), max)
	}
}

// --- chaos ---

func TestListInterfaces_Chaos_ConcurrentCallsAndDroppedSessions(t *testing.T) {
	t.Parallel()
	good := listFactory(t, modernTcpdumpD, "", 0)
	// A session killed mid-listing: partial output, then ssh's 255.
	dropped := listFactory(t, "1.eth0 [Up]\n2.an", "Connection reset by peer", 255)

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				got, err := ListInterfaces(Options{Host: "h", cmdFactory: good})
				if err != nil || len(got) != 5 {
					errs <- fmt.Errorf("good list: %d ifaces, err %v", len(got), err)
				}
				return
			}
			if got, err := ListInterfaces(Options{Host: "h", cmdFactory: dropped}); err == nil {
				errs <- fmt.Errorf("dropped session returned %+v with nil error", got)
			} else if !strings.Contains(err.Error(), "Connection reset") {
				errs <- fmt.Errorf("dropped session err %v lacks the remote reason", err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

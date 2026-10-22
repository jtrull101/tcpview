package remote

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The remote tests stand in for ssh and the remote tcpdump with child
// processes. Rather than relying on cat, false, sleep or sh existing (they
// don't on Windows), helperCmd re-runs this test binary itself, selecting
// TestHelperProcess, which then behaves as the requested command - Go's
// usual pattern for faking an exec'd program portably.

const helperEnv = "TCPVIEW_REMOTE_TEST_HELPER"

// helperCmd returns a command that acts as mode (see TestHelperProcess)
// with the given arguments.
func helperCmd(mode string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelperProcess$", "--", mode}, args...)...)
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	return cmd
}

// TestHelperProcess isn't a test: it's the fake command helperCmd runs,
// and skips straight away in a normal test run. No t.Parallel: it must
// reach os.Exit with the fake command's own exit status.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("only runs as a helper process")
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "helper: no mode")
		os.Exit(2)
	}
	mode, args := args[1], args[2:]

	switch mode {
	case "cat": // cat FILE: stream a file to stdout, like tcpdump -w -
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, _ = io.Copy(os.Stdout, f)
		os.Exit(0)
	case "fail": // exit 1 without output
		os.Exit(1)
	case "hang": // never exit on its own, like a stalled SSH session
		time.Sleep(time.Hour)
		os.Exit(0)
	case "script": // script STDOUT_FILE STDERR_TEXT CODE
		if b, err := os.ReadFile(args[0]); err == nil {
			_, _ = os.Stdout.Write(b)
		}
		fmt.Fprint(os.Stderr, args[1])
		code, _ := strconv.Atoi(args[2])
		os.Exit(code)
	case "ssh": // ssh HOST ARGS...: run ARGS like the real client does
		// ssh joins everything after the host with spaces and runs it
		// under the remote user's shell; SSH_CONNECTION comes from the
		// fake "sshd" side (the test's environment).
		cmd := exec.Command("sh", "-c", strings.Join(args[1:], " "))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "helper: unknown mode %q\n", mode)
	os.Exit(2)
}

// requireSh skips tests that exercise the remote shell script itself:
// they need a POSIX sh, as the remote host has. Linux and macOS always
// have one, as do GitHub's Windows runners (Git's sh.exe).
func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX sh on this machine to stand in for the remote shell")
	}
}

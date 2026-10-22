// Command tcpview is a TUI network traffic analyzer: pick an interface
// (or a remote host over SSH) and live-tail decoded packets, no tcpdump
// syntax required.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/remote"
	"github.com/jtrull101/tcpview/internal/store"
	"github.com/jtrull101/tcpview/internal/ui"
)

// runTeaProgram is a seam over actually launching the TUI against a real
// terminal, swapped out in tests so flag validation and model wiring are
// testable without a TTY (tea.NewProgram.Run needs a real terminal and
// would hang or misbehave under `go test`).
var runTeaProgram = func(m tea.Model) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// checkCapturePrivilegesFn is a seam over checkCapturePrivileges (see
// privileges.go), swapped out in tests so the rest of runLocal's wiring is
// testable regardless of the privilege level `go test` happens to run
// under.
var checkCapturePrivilegesFn = checkCapturePrivileges

// newRemoteSource is a seam over remote.New, swapped out in tests so
// runRemote's wiring is testable without actually spawning a real ssh
// subprocess and leaving its background reconnect loop running after the
// test returns.
var newRemoteSource = func(opts remote.Options) (capture.Source, error) {
	return remote.New(opts)
}

// listRemoteInterfaces is a seam over remote.ListInterfaces, swapped out
// in tests so runRemote's wiring is testable without a real ssh round
// trip.
var listRemoteInterfaces = remote.ListInterfaces

// newArchiveFn is a seam over store.NewArchive, swapped out in tests so
// archive wiring is testable without touching the real filesystem default
// location.
var newArchiveFn = store.NewArchive

// version is overridden at release-build time via
// `-ldflags "-X main.version=vX.Y.Z"` (see .github/workflows/release.yml).
// Left as "dev" for ordinary `go build`/`go run` invocations.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr *os.File) int {
	fs := flag.NewFlagSet("tcpview", flag.ContinueOnError)
	fs.SetOutput(stderr)
	remoteHost := fs.String("remote", "", "capture on a remote host over SSH, e.g. user@host")
	remoteTcpdump := fs.String("remote-tcpdump", "tcpdump", `remote tcpdump command (with -remote), e.g. "sudo -n tcpdump" when the SSH user isn't root`)
	remoteIface := fs.String("remote-iface", "", "remote interface to start capturing on (with -remote; omit to pick from the remote's interface list)")
	archiveDir := fs.String("archive-dir", defaultArchiveDir(), "directory for the on-disk rewind archive")
	archiveCapGiB := fs.Float64("archive-cap-gib", 1.0, "max total disk space (GiB) the rewind archive may use")
	noArchive := fs.Bool("no-archive", false, "disable the on-disk rewind archive (rewind is then limited to what's still in memory)")
	configPath := fs.String("config", defaultConfigPath(), "path to an optional JSON config file (archive_dir, archive_cap_gib, no_archive) - explicit flags always override it")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stderr, "tcpview %s\n", version)
		return 0
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "tcpview: %v\n", err)
		return 1
	}

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	ac := mergeArchiveConfig(
		archiveConfig{disabled: *noArchive, dir: *archiveDir, capGiB: *archiveCapGiB},
		explicit,
		cfg,
	)
	if !ac.disabled && ac.capGiB <= 0 {
		fmt.Fprintln(stderr, "tcpview: archive cap (-archive-cap-gib, or archive_cap_gib in the config file) must be positive, or use -no-archive to disable it")
		return 2
	}

	if *remoteHost != "" {
		return runRemote(remote.Options{Host: *remoteHost, Iface: *remoteIface, TcpdumpPath: *remoteTcpdump}, ac, stderr)
	}
	return runLocal(ac, stderr)
}

// archiveConfig bundles the CLI-configurable rewind-archive settings so
// runLocal/runRemote share one setup path.
type archiveConfig struct {
	disabled bool
	dir      string
	capGiB   float64
}

func defaultArchiveDir() string {
	if cache, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cache, "tcpview", "archive")
	}
	return filepath.Join(os.TempDir(), "tcpview-archive")
}

// setupArchive opens the on-disk archive per ac, or returns (nil, nil, nil)
// if archiving is disabled. Callers should defer closing the returned
// Archive (if non-nil) once the TUI exits.
func setupArchive(ac archiveConfig) (*store.Archive, *store.ArchiveReader, error) {
	if ac.disabled {
		return nil, nil, nil
	}
	a, err := newArchiveFn(store.ArchiveOptions{
		Dir:           ac.dir,
		MaxTotalBytes: int64(ac.capGiB * (1 << 30)),
	})
	if err != nil {
		return nil, nil, err
	}
	return a, store.NewArchiveReader(ac.dir), nil
}

// runRemote captures on opts.Host; opts.Iface, if set, is the interface
// to start on.
func runRemote(opts remote.Options, ac archiveConfig, stderr *os.File) int {
	host, iface := opts.Host, opts.Iface
	open := func(name string) (capture.Source, error) {
		o := opts
		o.Iface = name
		return newRemoteSource(o)
	}

	// The remote's interface list feeds the picker (no -remote-iface) or
	// tab switching (with one). Only the former can't do without it.
	ifaces, listErr := listRemoteInterfaces(opts)

	var m ui.Model
	if iface == "" {
		if listErr != nil {
			fmt.Fprintf(stderr, "tcpview: %v\n(pass -remote-iface to capture on a known interface without listing them)\n", listErr)
			return 1
		}
		m = ui.NewModel(ifaces, open)
		m.SetRemoteHost(host)
	} else {
		src, err := open(iface)
		if err != nil {
			fmt.Fprintf(stderr, "tcpview: %v\n", err)
			return 1
		}
		m = ui.NewModelWithSource(fmt.Sprintf("%s (%s)", host, iface), src)
		m.SetRemoteHost(host)
		if listErr == nil {
			m.SetInterfaces(ifaces, open, iface)
		} else {
			m.SetStatus(fmt.Sprintf("interface switching unavailable: %v", listErr))
		}
	}

	if a, r, err := setupArchive(ac); err != nil {
		fmt.Fprintf(stderr, "tcpview: archive: %v\n", err)
		return 1
	} else if a != nil {
		defer func() {
			if err := a.Close(); err != nil {
				fmt.Fprintf(stderr, "tcpview: archive: %v\n", err)
			}
		}()
		m.EnableArchive(a, r)
	}

	if err := runTeaProgram(m); err != nil {
		fmt.Fprintf(stderr, "tcpview: %v\n", err)
		return 1
	}
	return 0
}

func runLocal(ac archiveConfig, stderr *os.File) int {
	if err := checkCapturePrivilegesFn(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	ifaces, err := capture.ListInterfaces()
	if err != nil {
		fmt.Fprintf(stderr, "tcpview: %v\n", err)
		return 1
	}

	open := func(name string) (capture.Source, error) {
		return capture.NewLive(capture.Options{Iface: name, Promiscuous: true})
	}

	m := ui.NewModel(ifaces, open)
	if a, r, err := setupArchive(ac); err != nil {
		fmt.Fprintf(stderr, "tcpview: archive: %v\n", err)
		return 1
	} else if a != nil {
		defer func() {
			if err := a.Close(); err != nil {
				fmt.Fprintf(stderr, "tcpview: archive: %v\n", err)
			}
		}()
		m.EnableArchive(a, r)
	}

	if err := runTeaProgram(m); err != nil {
		fmt.Fprintf(stderr, "tcpview: %v\n", err)
		return 1
	}
	return 0
}

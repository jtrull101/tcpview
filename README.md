# tcpview

A terminal UI network traffic analyzer. No more remembering interface names, ports, or `tcpdump` filter syntax up front: pick an interface (or a remote host over SSH) from a list, watch decoded traffic live, filter it interactively, and rewind to see what already passed.

![tcpview live-tailing loopback traffic, filtered to proto=icmp](docs/screenshot.png)

## Features

- **Live capture** on a local interface via libpcap, or on a **remote host over SSH** (`ssh <host> tcpdump -i <iface> -U -w -`, piped and decoded locally - no agent to install on the remote box, just SSH access and `tcpdump`).
- **Interactive filtering** - press `/` and type a simple expression like `proto=tcp dport=443 src=10.0.0.0/24`, applied live against whatever's currently buffered.
- **Rewind** - press `←` to scrub backward through traffic that already scrolled off, `→` to scrub forward, `L` to jump back to live. Recent traffic is served from an in-memory ring; anything older comes from a bounded on-disk archive (see below).
- **Packet detail** - press `enter` on any row for a parsed field breakdown plus a hex+ASCII dump of the raw frame.
- **Export to pcap** - press `e` to write whatever's currently shown (post-filter) to a real pcap file, openable in tcpdump/Wireshark/etc.
- **Help** - press `?` for a full keybinding reference at any time.

## Installing a release binary

Prebuilt binaries for linux/amd64, darwin/amd64, and darwin/arm64 are attached to each [release](https://github.com/jtrull101/tcpview/releases).

**Linux note**: the released binary is built on GitHub's `ubuntu-latest` runner, which links against the legacy `libpcap.so.0.8` soname. Distros that only ship the modern `libpcap.so.1` (e.g. current Arch) will fail to launch it with a `cannot open shared object file` error. Either symlink your way around it:

```sh
sudo ln -s /usr/lib/libpcap.so.1 /usr/lib/libpcap.so.0.8
```

or just build from source (below), which always matches whatever libpcap your system actually has.

## Building

```sh
go build -o tcpview .
# or: make build
```

Requires Go and `libpcap` (already installed on most Linux systems; `sudo pacman -S libpcap` on Arch).

A `Makefile` wraps the common dev commands:

```sh
make build         # go build -> ./tcpview
make run           # build + run (needs CAP_NET_RAW; see `make setcap`)
make setcap        # grant the built binary CAP_NET_RAW/CAP_NET_ADMIN once, so sudo isn't needed per-run
make test          # go test ./...
make test-race     # go test ./... -race
make cover         # race + coverage, prints the per-function/aggregate summary
make cover-check   # same, but fails if aggregate coverage drops below 90%
make fuzz          # short fuzz smoke pass across every Fuzz target (FUZZTIME=10s by default)
make fmt           # gofmt -w .
make lint          # golangci-lint run ./...
make check         # everything CI runs: fmt-check, vet, lint, race tests, coverage gate
make clean         # remove build/coverage artifacts
make help          # list all targets
```

## Running

Live capture needs elevated privileges on Linux (`CAP_NET_RAW`). Either:

```sh
sudo ./tcpview
```

or grant the capability once so you don't need `sudo` every time:

```sh
sudo setcap cap_net_raw,cap_net_admin=eip ./tcpview
./tcpview
```

### Remote capture

```sh
./tcpview -remote user@host -remote-iface eth0
```

Requires SSH access to the host and `tcpdump` installed there (no root needed locally for this mode - the privilege requirement is on the remote end, where `tcpdump` runs).

### Keybindings

| Key | Action |
|---|---|
| `↑`/`↓` or `j`/`k` | move the interface-picker cursor |
| `enter` | select an interface / apply a filter |
| `/` | edit the filter |
| `esc` | cancel filter editing |
| `←` / `→` | rewind backward / forward in time |
| `L` | jump back to live |
| `e` | export the currently shown (post-filter) packets to a pcap file |
| `?` | toggle the full keybinding help screen |
| `q` | quit (editing a filter? just types a literal `q` instead; viewing detail/help? goes back) |
| `ctrl+c` | quit immediately, from anywhere |

### Rewind archive flags

```
-archive-dir string        directory for the on-disk rewind archive (default: a per-user cache dir)
-archive-cap-gib float     max total disk space, in GiB, the archive may use (default 1)
-no-archive                disable the on-disk archive entirely (rewind then limited to what's still in memory)
```

The archive rotates files by age/size and deletes the oldest ones first once the total exceeds the configured cap - it never grows unbounded.

### Config file

Flags above can also live in an optional JSON config file, checked by default at `$XDG_CONFIG_HOME/tcpview/config.json` (or `~/.config/tcpview/config.json`), overridable with `-config path`:

```json
{
  "archive_dir": "/var/lib/tcpview/archive",
  "archive_cap_gib": 2.0,
  "no_archive": false
}
```

Precedence: an explicitly-passed CLI flag always wins over the config file, which always wins over the built-in default.

## Development

See `CLAUDE.md` for the testing policy (every package needs positive/negative/load/stress/chaos tests, >90% aggregate coverage) and architecture notes (why the archive isn't literal pcap format, why remote capture shells out to the remote's own `tcpdump` instead of needing an agent, etc).

```sh
make check   # fmt-check, vet, golangci-lint, race-enabled tests, 90% coverage gate - everything CI runs
```

Every parser-like function (filter expressions, packet decoding, archive file parsing, the `/proc/self/status` capability parser) also has a native Go fuzz test (`make fuzz` for a quick local smoke pass); CI runs a short fuzz pass on every push, and the project has been fuzzed for 5M+ executions locally with zero crashes found.

### CI / releases

- `.github/workflows/ci.yml` runs on every push/PR: build, `gofmt` check, `go vet`, `golangci-lint`, race-enabled tests, a 90%-aggregate-coverage gate, and a fuzz smoke pass.
- `.github/workflows/release.yml` runs on any `vX.Y.Z` tag push: builds native binaries for linux/amd64, darwin/amd64, and darwin/arm64, and publishes them as a GitHub Release.

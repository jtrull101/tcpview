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

## A tour, in screenshots

Everything below is a real screenshot of real traffic captured on an actual machine while writing this - nothing staged or mocked.

### No `tcpdump -D` required

Launching with no arguments lists every capturable interface by name and description - including the real-but-easy-to-forget ones like `any` (capture on everything at once) alongside physical NICs, bridges, and `nflog`/`nfqueue`. Pick one with `↑`/`↓` + `enter`; no prior knowledge of interface names needed.

![Interface picker listing real local interfaces](docs/screenshot-picker.png)

### Filtering is live, not a restart

Press `/` and type an expression - `iface`, `src`, `dst`, `sport`, `dport`, `proto` - while packets keep flowing in the background underneath it:

![Typing a filter expression while traffic keeps streaming](docs/screenshot-filter-edit.png)

Hit `enter` and the view narrows instantly against the *existing* buffer - no re-capture, no dropped history. Here it's narrowed a noisy multi-protocol capture down to exactly the real HTTPS flows on port 443:

![1000 buffered packets narrowed down to 235 real HTTPS flows on dport=443](docs/screenshot-filter-applied.png)

### Rewind: the actually novel part

This is the feature a plain `tcpdump`/Wireshark live-capture session can't give you: scrub backward in time with `←`/`→` to see traffic that already scrolled off-screen, without having had to pre-emptively start a recording. Recent history comes from an in-memory ring; anything older is transparently pulled from a bounded on-disk archive that rotates and prunes itself (see [Rewind archive flags](#rewind-archive-flags) below) - the UI doesn't care which, it just shows you what was happening at that point in time, filters and all:

![REWOUND to a point 25 seconds in the past, showing real historical traffic](docs/screenshot-rewind.png)

### Packet detail, down to the bytes

`enter` on any row in the live or rewound view opens a full breakdown plus a classic hex+ASCII dump of the actual captured frame - no separate tool needed to go one level deeper than the summary table:

![Packet detail view with a real hex dump of a captured TCP/TLS frame](docs/screenshot-detail.png)

### Full keybinding reference, always one keypress away

`?` from anywhere in the live view:

![Full keybinding help screen](docs/screenshot-help.png)

## Installing a release binary

```sh
curl -sSL https://raw.githubusercontent.com/jtrull101/tcpview/main/install.sh | sh
```

Detects your OS/arch, downloads the matching asset from the [latest release](https://github.com/jtrull101/tcpview/releases), and installs it to `~/.local/bin/tcpview` (override with `INSTALL_DIR=/somewhere sh install.sh`; pin a version with `VERSION=v0.1.0 sh install.sh`). Prebuilt binaries cover linux/amd64, darwin/amd64, and darwin/arm64.

**Linux note**: the released binary is built on GitHub's `ubuntu-latest` runner, which links against the legacy `libpcap.so.0.8` soname. Distros that only ship the modern `libpcap.so.1` (e.g. current Arch) will fail to launch it with a `cannot open shared object file` error - the install script detects this and prints the exact fix:

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

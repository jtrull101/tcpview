# tcpview

A terminal UI network traffic analyzer. No more remembering interface names, ports, or `tcpdump` filter syntax up front: pick an interface (or a remote host over SSH) from a list, watch decoded traffic live, filter it interactively, and rewind to see what already passed.

## Features

- **Live capture** on a local interface via libpcap, or on a **remote host over SSH** (`ssh <host> tcpdump -i <iface> -U -w -`, piped and decoded locally - no agent to install on the remote box, just SSH access and `tcpdump`).
- **Interactive filtering** - press `/` and type a simple expression like `proto=tcp dport=443 src=10.0.0.0/24`, applied live against whatever's currently buffered.
- **Rewind** - press `←` to scrub backward through traffic that already scrolled off, `→` to scrub forward, `L` to jump back to live. Recent traffic is served from an in-memory ring; anything older comes from a bounded on-disk archive (see below).
- **Packet detail** - press `enter` on any row for a parsed field breakdown plus a hex+ASCII dump of the raw frame.
- **Export to pcap** - press `e` to write whatever's currently shown (post-filter) to a real pcap file, openable in tcpdump/Wireshark/etc.
- **Help** - press `?` for a full keybinding reference at any time.

## Building

```sh
go build -o tcpview .
```

Requires Go and `libpcap` (already installed on most Linux systems; `sudo pacman -S libpcap` on Arch).

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
go test ./... -race -cover
```

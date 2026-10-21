# tcpview - Claude Code project notes

See `README.md` for what this project is and how to build/run it. This file is operational notes for working in this repo.

## Testing policy

Every non-trivial component (each package under `internal/`) needs all of the following test categories, not just happy-path coverage:

- **Positive** - correct behavior on well-formed input.
- **Negative** - malformed/invalid input, empty input, out-of-range values; confirm the component fails or degrades predictably rather than panicking or corrupting state.
- **Load** - realistic sustained volume (e.g. ring buffer under continuous high packet-rate insertion, archive writer under sustained flush) - confirm throughput holds and memory/disk stay bounded.
- **Stress** - beyond realistic volume, at or past the component's stated limits (e.g. ring buffer at/over capacity, archive at/over its configured disk cap, filter evaluated against a pathological packet count) - confirm graceful degradation (eviction, rotation, backpressure), never unbounded growth or a crash.
- **Chaos** - fault injection and disorder: out-of-order timestamps, truncated/corrupt packet data, a capture source that drops mid-stream, concurrent goroutines hammering the same store from multiple directions, a killed SSH connection mid-remote-capture. Confirm the component recovers or fails cleanly, never silently drops into an inconsistent state.

**Aggregate coverage must stay above 90%** (`go test ./... -cover`), tracked the same way grim-deck tracks gcov coverage in CI once this repo has CI wired up. Packages that talk to the OS/network directly (`internal/capture`, `internal/remote`) should isolate that boundary behind a small interface so the logic around it (decoding, retry, error handling) is still unit-testable without a real NIC/SSH session - the rest of the packages (`store`, `filter`) have no excuse not to hit the full bar since they're pure logic by design (see the architecture's hard rule in the plan).

**Tests must be multi-threaded by default**: every top-level test function calls `t.Parallel()` as its first line unless there's a specific, commented reason it can't (e.g. it manipulates shared global/process state, or it's already internally concurrent in a way `t.Parallel()` would complicate - rare). Subtests under `t.Run` do the same. This is deliberate, not incidental: it forces every package's exported API to be safe under real concurrent use from the moment it's written, rather than discovering data races only when `internal/capture`'s goroutines start hammering shared state later. Always run the suite with `-race` (`go test ./... -race -cover`); a parallel suite that isn't race-checked defeats the purpose.

## Remote capture

`internal/remote` runs capture on a remote host over SSH by shelling out to the remote's own `tcpdump` (`ssh <host> tcpdump -i <iface> -U -w -`) and decoding the piped pcap stream locally with `gopacket/pcapgo.Reader` - same downstream pipeline (`store`, `filter`, `ui`) as local capture, just a different packet source. The remote interface list (for the picker and tab switching) comes the same way, from `ssh <host> tcpdump -D`. This deliberately avoids requiring a cross-compiled `tcpview` agent binary on every remote box; it only needs SSH access and `tcpdump`, both of which are near-universal already. Reconnect/backoff behavior on a dropped SSH session is chaos-tested per the policy above, not just assumed to work.

## Archive disk cap

`internal/store/archive.go`'s rotating pcap writer enforces a configurable maximum total size (GiB, user-configurable) for all archived capture files combined - on rotation, oldest archive files are deleted first to stay under the cap. This must never grow unbounded; it's stress- and load-tested per the policy above (fill past the cap under sustained write load, confirm total on-disk size never exceeds it).

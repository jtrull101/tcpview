#!/bin/sh
# Records the README's demo GIFs by driving a real tcpview session.
#
#   scripts/record-demos.sh [scene ...]    (default: every scene)
#
# How it works: tmux supplies a fixed-size PTY, asciinema records what
# tcpview draws into it, and `tmux send-keys` plays the keystrokes. agg
# turns the resulting asciicast into a GIF. Keeping this a script rather
# than a one-off means the GIFs can be re-recorded when the UI changes,
# which is the whole reason the old screenshots went stale.
#
# Requirements (all installable without root - see the README's
# contributing notes):
#   - asciinema   PYTHONPATH=~/.local/asciinema-pkg python3 -m asciinema
#   - agg         https://github.com/asciinema/agg/releases
#   - tmux
#   - a tcpview binary with CAP_NET_RAW/CAP_NET_ADMIN (see $TCPVIEW)
#
# Capture runs on a real interface, so whatever is on the wire during a
# take ends up in the GIF. gen_traffic below drives predictable HTTPS so
# the view is never empty; it does not and cannot suppress whatever else
# the machine is doing.
set -eu

TCPVIEW=${TCPVIEW:-./tcpview-demo}
IFACE=${IFACE:-eth0}
OUTDIR=${OUTDIR:-docs}
COLS=${COLS:-100}
ROWS=${ROWS:-28}
FONT_SIZE=${FONT_SIZE:-16}
ASCIINEMA=${ASCIINEMA:-"python3 -m asciinema"}
AGG=${AGG:-agg}

# Archive to a scratch dir: the demo must not write into the user's real
# rewind archive, and a fresh one keeps takes reproducible.
ARCHIVE_DIR=${ARCHIVE_DIR:-$(mktemp -d)}

if [ ! -x "$TCPVIEW" ]; then
	echo "record-demos: $TCPVIEW is not executable - build it first" >&2
	exit 1
fi

traffic_pid=""
gen_traffic() {
	# Predictable, boring HTTPS so the table always has something in it.
	while :; do
		for url in https://example.com https://www.google.com https://github.com; do
			curl -s -o /dev/null --max-time 3 "$url" || true
			sleep 0.3
		done
	done &
	traffic_pid=$!
}

stop_traffic() {
	[ -n "$traffic_pid" ] && kill "$traffic_pid" 2>/dev/null || true
	traffic_pid=""
}

cleanup() {
	stop_traffic
	tmux kill-session -t tcpviewrec 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# start_session launches tcpview under asciinema inside a fixed-size tmux
# pane. $1 is the cast file.
start_session() {
	tmux kill-session -t tcpviewrec 2>/dev/null || true
	tmux new-session -d -s tcpviewrec -x "$COLS" -y "$ROWS" \
		"$ASCIINEMA rec --overwrite -c '$TCPVIEW -archive-dir $ARCHIVE_DIR' '$1'"
	sleep 2 # let asciinema start and tcpview draw its first frame
}

# key sends keystrokes to the running session, then waits. "key <keys> <secs>"
key() {
	tmux send-keys -t tcpviewrec "$1"
	sleep "${2:-1}"
}

end_session() {
	tmux send-keys -t tcpviewrec q 2>/dev/null || true
	sleep 1
	tmux kill-session -t tcpviewrec 2>/dev/null || true
	sleep 1
}

# trim_cast drops everything before tcpview's first draw and rebases the
# timestamps, so the GIF opens on the UI instead of on an empty terminal.
# GitHub shows a GIF's first frame as its poster, so a blank lead-in is a
# blank thumbnail.
trim_cast() {
	python3 -I - "$1" <<-'EOF'
		import json, sys
		path = sys.argv[1]
		lines = open(path).read().splitlines()
		header, events = lines[0], []
		for ln in lines[1:]:
		    if not ln.strip():
		        continue
		    try:
		        events.append(json.loads(ln))
		    except ValueError:
		        pass
		start = 0
		for i, (t, k, d) in enumerate(events):
		    if "tcpview" in d:
		        start = i
		        break
		base = events[start][0] if events else 0
		with open(path, "w") as fh:
		    fh.write(header + "\n")
		    for t, k, d in events[start:]:
		        fh.write(json.dumps([round(max(0.0, t - base), 6), k, d]) + "\n")
	EOF
}

render() {
	name=$1
	trim_cast "$OUTDIR/$name.cast"
	"$AGG" --font-size "$FONT_SIZE" --idle-time-limit 1.5 \
		"$OUTDIR/$name.cast" "$OUTDIR/$name.gif"
	# The asciicast is plain text - keeping it makes a recording
	# auditable (what actually went on screen) and re-renderable at a
	# different size without re-capturing.
	[ "${KEEP_CAST:-0}" = "1" ] || rm -f "$OUTDIR/$name.cast"
	echo "record-demos: wrote $OUTDIR/$name.gif"
}

# Each scene opens on the picker, because that is what tcpview does on
# launch; scenes that document the live view select an interface first.
# $1 is the zero-based row to land on (default 0, which is $IFACE).
select_iface() {
	i=0
	while [ "$i" -lt "${1:-0}" ]; do
		key Down 0.4
		i=$((i + 1))
	done
	key Enter 3
}

scene_picker() {
	start_session "$OUTDIR/demo-picker.cast"
	sleep 2
	# Move down the list and back, then select $IFACE (row 0) - landing on
	# a bridge or veth would end the clip on a view with no traffic in it.
	key Down 0.8
	key Down 0.8
	key Up 0.8
	key Up 0.8
	key Enter 5
	end_session
	render demo-picker
}

scene_live() {
	start_session "$OUTDIR/demo.cast"
	select_iface 0
	sleep 6
	end_session
	render demo
}

scene_switch() {
	start_session "$OUTDIR/demo-switch.cast"
	select_iface 0
	sleep 4
	key i 2.5 # reopen the list, cursor already on the current interface
	# Six rows down is "lo" - six interfaces that tab would have opened
	# and torn down one at a time to get here.
	n=0
	while [ "$n" -lt 6 ]; do
		key Down 0.45
		n=$((n + 1))
	done
	key Enter 4.5  # one jump, one capture opened
	key i 2        # and back into the list...
	key Escape 2.5 # ...leaving the running capture untouched
	end_session
	render demo-switch
}

scene_filter() {
	start_session "$OUTDIR/demo-filter.cast"
	select_iface 0
	sleep 4
	key / 1
	tmux send-keys -l -t tcpviewrec "proto=tcp dport=443"
	sleep 2
	key Enter 5
	end_session
	render demo-filter
}

scene_rewind() {
	start_session "$OUTDIR/demo-rewind.cast"
	select_iface 0
	sleep 6
	key Left 2
	key Left 2
	key Right 2
	key L 3
	end_session
	render demo-rewind
}

scene_detail() {
	start_session "$OUTDIR/demo-detail.cast"
	select_iface 0
	sleep 5
	key Up 0.8
	key Up 0.8
	key Enter 5
	key Escape 2
	end_session
	render demo-detail
}

scene_help() {
	start_session "$OUTDIR/demo-help.cast"
	select_iface 0
	sleep 3
	key ? 5
	key Escape 1.5
	end_session
	render demo-help
}

mkdir -p "$OUTDIR"
gen_traffic

scenes=${*:-"picker live switch filter rewind detail help"}
for s in $scenes; do
	echo "record-demos: recording $s"
	"scene_$s"
done

stop_traffic
echo "record-demos: done (archive scratch dir: $ARCHIVE_DIR)"

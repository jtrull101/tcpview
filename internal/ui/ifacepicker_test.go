package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/filter"
	"github.com/jtrull101/tcpview/internal/store"
)

// Tests for reopening the interface picker mid-capture with "i". Tabbing
// is the neighbour-at-a-time path (ifaceswitch_test.go); this is the jump
// -straight-there path, and the thing it must not do is disturb a running
// capture before the user actually chooses.

// pickOn returns a model live-tailing eth0 with the picker reopened.
func pickOn(t *testing.T, n int) (Model, *recordingOpener) {
	t.Helper()
	m, r := liveOn(t, n)
	m = press(t, m, "i")
	if m.state != viewPickInterface || !m.pickerReturn {
		t.Fatalf("setup: state=%v pickerReturn=%v, want the picker reopened", m.state, m.pickerReturn)
	}
	return m, r
}

// --- positive ---

func TestIfacePicker_IReopensListOnCurrentInterface(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 4)
	m = press(t, press(t, m, "tab"), "tab") // capture on eth2
	opensBefore := len(r.names)

	m = press(t, m, "i")
	if m.state != viewPickInterface {
		t.Fatalf("state after i = %v, want the picker", m.state)
	}
	if m.cursor != 2 || m.pickerSaved != 2 {
		t.Fatalf("cursor=%d pickerSaved=%d, want both on eth2 (index 2)", m.cursor, m.pickerSaved)
	}
	// Opening the list is not itself a switch.
	if len(r.names) != opensBefore {
		t.Fatalf("opened %v, want no new source just for showing the list", r.names)
	}
	if m.source == nil {
		t.Fatal("showing the list closed the running capture")
	}
}

func TestIfacePicker_SelectingAnotherInterfaceJumpsStraightThere(t *testing.T) {
	t.Parallel()
	m, r := pickOn(t, 6)
	first := r.last()

	// eth0 -> eth4 in one move, not four tabs.
	for i := 0; i < 4; i++ {
		m = press(t, m, "down")
	}
	m = press(t, m, "enter")

	if m.state != viewLiveTail {
		t.Fatalf("state after selecting = %v, want live tail", m.state)
	}
	if m.label != "eth4" || m.cursor != 4 {
		t.Fatalf("label=%q cursor=%d, want eth4 at index 4", m.label, m.cursor)
	}
	if !first.closed {
		t.Fatal("selecting a new interface didn't close the old source")
	}
	// The whole point: no eth1/eth2/eth3 captures were opened and torn
	// down on the way.
	if got := strings.Join(r.names, ","); got != "eth0,eth4" {
		t.Fatalf("opened %s, want only eth0,eth4 - intermediate interfaces must not be opened", got)
	}
}

func TestIfacePicker_EscKeepsCurrentCaptureRunning(t *testing.T) {
	t.Parallel()
	m, r := pickOn(t, 3)
	live := r.last()

	m = press(t, m, "esc")
	if m.state != viewLiveTail {
		t.Fatalf("state after esc = %v, want live tail", m.state)
	}
	if m.pickerReturn {
		t.Fatal("pickerReturn still set after leaving the picker")
	}
	if live.closed {
		t.Fatal("esc closed the running capture")
	}
	if m.source != capture.Source(live) {
		t.Fatal("esc replaced the source instead of leaving it alone")
	}
	if got := strings.Join(r.names, ","); got != "eth0" {
		t.Fatalf("opened %s, want only the original eth0", got)
	}
}

func TestIfacePicker_EscAfterMovingCursorLeavesPositionIntact(t *testing.T) {
	t.Parallel()
	// Regression: cursor doubles as the picker cursor and the index of the
	// interface being captured. Browsing and cancelling used to leave the
	// live view pointing at whatever was last highlighted, so the header
	// lied and the next tab stepped from the wrong place.
	m, _ := liveOn(t, 5)
	m = press(t, m, "i")
	m = press(t, press(t, m, "down"), "down") // highlight eth2, don't select
	m = press(t, m, "esc")

	if m.cursor != 0 {
		t.Fatalf("cursor after cancelling = %d, want 0 (still on eth0)", m.cursor)
	}
	if out := m.View(); !strings.Contains(out, "eth0 [1/5") {
		t.Fatalf("View() = %q, want the header still on eth0 [1/5", out)
	}
	// And tab must step from eth0, not from where the cursor was parked.
	m = press(t, m, "tab")
	if m.label != "eth1" {
		t.Fatalf("label after tab = %q, want eth1", m.label)
	}
}

func TestIfacePicker_FooterAndHelpDocumentTheKeys(t *testing.T) {
	t.Parallel()
	m, _ := pickOn(t, 3)
	out := m.View()
	if !strings.Contains(out, "esc keep eth0") {
		t.Fatalf("picker View() = %q, want an esc hint naming the current interface", out)
	}
	if !strings.Contains(helpText, "i   ") {
		t.Fatal("help text doesn't document the i key")
	}
}

func TestIfacePicker_StartupPickerHasNoEscHint(t *testing.T) {
	t.Parallel()
	// The first picker has no live view behind it, so it must not offer to
	// go "back" to one.
	m := NewModel(ifaceList(3), (&recordingOpener{}).open)
	if out := m.View(); strings.Contains(out, "esc") {
		t.Fatalf("startup picker View() = %q, want no esc hint", out)
	}
}

// --- negative ---

func TestIfacePicker_ReselectingCurrentInterfaceIsNoop(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 3)
	live := r.last()
	for i := 0; i < 5; i++ {
		m, _ = update(t, m, packetMsg(samplePacket(i)))
	}
	m, _ = update(t, m, tickMsg(time.Now()))
	before := m.ring.Len()

	m = press(t, m, "i")
	m = press(t, m, "enter") // cursor never moved: still eth0

	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want live tail", m.state)
	}
	if live.closed {
		t.Fatal("re-picking the current interface tore down its capture")
	}
	if got := strings.Join(r.names, ","); got != "eth0" {
		t.Fatalf("opened %s, want no reopen", got)
	}
	if m.ring.Len() != before {
		t.Fatalf("ring went from %d to %d packets, want the buffer kept", before, m.ring.Len())
	}
}

func TestIfacePicker_EscAtStartupPickerDoesNothing(t *testing.T) {
	t.Parallel()
	m := NewModel(ifaceList(3), (&recordingOpener{}).open)
	m = press(t, m, "esc")
	if m.state != viewPickInterface {
		t.Fatalf("state = %v, want to stay in the picker with nothing to return to", m.state)
	}
}

func TestIfacePicker_NoopWithoutAnotherInterface(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 1)
	m = press(t, m, "i")
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want live tail - nowhere else to go", m.state)
	}
}

func TestIfacePicker_NoopWithoutAnOpener(t *testing.T) {
	t.Parallel()
	// Remote capture started with -remote-iface and no usable tcpdump -D
	// has no opener, so there's no list to show.
	m := NewModelWithSource("router0", newFakeSource())
	m = press(t, m, "i")
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want live tail", m.state)
	}
}

func TestIfacePicker_IWhileEditingFilterTypesInsteadOfOpening(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 3)
	m = press(t, m, "/")
	m = press(t, m, "i")
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want to stay in the live view while editing", m.state)
	}
	if got := m.filterBar.input.Value(); got != "i" {
		t.Fatalf("filter input = %q, want the typed \"i\"", got)
	}
}

// --- chaos ---

func TestIfacePicker_Chaos_PacketsKeepArrivingWhileListIsOpen(t *testing.T) {
	t.Parallel()
	// The picker is an overlay, not a pause: capture is still running
	// underneath and its packets must not be lost.
	m, _ := pickOn(t, 3)
	for i := 0; i < 20; i++ {
		m, _ = update(t, m, packetMsg(samplePacket(i)))
	}
	if m.ring.Len() != 20 {
		t.Fatalf("ring holds %d packets, want 20 buffered while the list was open", m.ring.Len())
	}
	m = press(t, m, "esc")
	m, _ = update(t, m, tickMsg(time.Now()))
	if len(m.displayed) != 20 {
		t.Fatalf("displayed %d packets after returning, want 20", len(m.displayed))
	}
}

func TestIfacePicker_Chaos_SelectionKeepsFilterAndJumpsToLive(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 4)
	f, err := filter.ParseExpression("proto=tcp")
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}
	m.filterBar.input.SetValue("proto=tcp")
	m.filterBar.apply(f)
	m.timeline.stepBack(time.Now())

	m = press(t, m, "i")
	m = press(t, press(t, m, "down"), "enter")

	if m.label != "eth1" {
		t.Fatalf("label = %q, want eth1", m.label)
	}
	if m.filterBar.text != "proto=tcp" {
		t.Fatalf("filter after switch = %q, want proto=tcp kept", m.filterBar.text)
	}
	if m.timeline.active {
		t.Fatal("switching from the picker left the view rewound, want it back on live")
	}
}

func TestIfacePicker_Chaos_OpenFailureLeavesListUsable(t *testing.T) {
	t.Parallel()
	r := &recordingOpener{failFor: map[string]bool{"eth2": true}}
	m := press(t, NewModel(ifaceList(4), r.open), "enter")

	m = press(t, m, "i")
	m = press(t, press(t, press(t, m, "down"), "down"), "enter")
	if m.err == nil {
		t.Fatal("selecting an interface that fails to open set no error")
	}
	// Staying in the list is the point: the old capture is already closed,
	// so there's nothing to go back to, but another interface must still
	// be choosable rather than leaving the user on a dead view.
	if m.state != viewPickInterface {
		t.Fatalf("state = %v, want to stay in the list after a failed open", m.state)
	}
	if out := m.View(); !strings.Contains(out, "permission denied") {
		t.Fatalf("picker View() = %q, want the open error shown", out)
	}

	m = press(t, press(t, m, "up"), "enter")
	if m.state != viewLiveTail || m.label != "eth1" {
		t.Fatalf("state=%v label=%q, want live tail on eth1 after recovering", m.state, m.label)
	}
	if m.err != nil {
		t.Fatalf("err = %v, want the failed open's error cleared once one succeeds", m.err)
	}
}

func TestIfacePicker_Chaos_ArchivesOldRingOnSelection(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 3)
	dir := t.TempDir()
	a, err := store.NewArchive(store.ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	m.EnableArchive(a, store.NewArchiveReader(dir))

	start := time.Now()
	for i := 0; i < 5; i++ {
		m, _ = update(t, m, packetMsg(samplePacket(i)))
	}
	m = press(t, m, "i")
	m = press(t, press(t, m, "down"), "enter")

	if m.ring.Len() != 0 {
		t.Fatalf("ring holds %d packets after switching, want a fresh view", m.ring.Len())
	}
	got, err := store.NewArchiveReader(dir).Range(start.Add(-time.Second), time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("archive Range error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("archive holds %d packets, want the 5 the previous interface buffered", len(got))
	}
}

// --- load ---

func TestIfacePicker_Load_RepeatedOpenCancelLeavesOneCapture(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 8)
	live := r.last()

	for i := 0; i < 200; i++ {
		m = press(t, m, "i")
		m = press(t, press(t, m, "down"), "down")
		m = press(t, m, "esc")
	}

	if m.state != viewLiveTail || m.cursor != 0 {
		t.Fatalf("state=%v cursor=%d, want live tail still on eth0", m.state, m.cursor)
	}
	if live.closed {
		t.Fatal("browsing and cancelling closed the capture")
	}
	if len(r.names) != 1 {
		t.Fatalf("opened %d sources, want 1 - cancelling must never open anything", len(r.names))
	}
}

// --- stress ---

func TestIfacePicker_Stress_EverySelectionOpensExactlyOneSource(t *testing.T) {
	t.Parallel()
	const n = 10
	m, r := liveOn(t, n)

	// Jump to each interface in turn via the list, always from wherever
	// the previous jump left capture.
	for target := 1; target < n; target++ {
		m = press(t, m, "i")
		for m.cursor < target {
			m = press(t, m, "down")
		}
		for m.cursor > target {
			m = press(t, m, "up")
		}
		m = press(t, m, "enter")
		if m.cursor != target {
			t.Fatalf("cursor = %d after selecting index %d", m.cursor, target)
		}
	}

	// n-1 jumps plus the initial eth0 open, and no extras: a jump must
	// never open the interfaces it passes over.
	if len(r.names) != n {
		t.Fatalf("opened %d sources (%s), want %d", len(r.names), strings.Join(r.names, ","), n)
	}
	for i, fs := range r.opened[:len(r.opened)-1] {
		if !fs.closed {
			t.Fatalf("source %d left open after switching away", i)
		}
	}
}

// --- remote ---

func TestIfacePicker_Remote_ListNamesHostAndEscKeepsCapture(t *testing.T) {
	t.Parallel()
	r := &recordingOpener{}
	m := NewModelWithSource("router0 (eth0)", newFakeSource())
	m.SetRemoteHost("router0")
	m.SetInterfaces(ifaceList(3), r.open, "eth1")

	m = press(t, m, "i")
	if m.cursor != 1 || m.pickerSaved != 1 {
		t.Fatalf("cursor=%d pickerSaved=%d, want the remote's current eth1", m.cursor, m.pickerSaved)
	}
	out := m.View()
	if !strings.Contains(out, "choose an interface on router0") {
		t.Fatalf("View() = %q, want the remote host named", out)
	}
	if !strings.Contains(out, "esc keep router0 (eth1)") {
		t.Fatalf("View() = %q, want the esc hint to use the host-qualified label", out)
	}

	m = press(t, press(t, m, "down"), "enter")
	if m.label != "router0 (eth2)" {
		t.Fatalf("label = %q, want router0 (eth2)", m.label)
	}
}

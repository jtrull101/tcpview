package ui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/filter"
	"github.com/jtrull101/tcpview/internal/store"
)

// recordingOpener hands out a fresh fakeSource per open call and remembers
// every one, so tests can check which got closed. Safe for concurrent use,
// since Bubble Tea runs openSource cmds on their own goroutines.
type recordingOpener struct {
	mu      sync.Mutex
	opened  []*fakeSource
	names   []string
	failFor map[string]bool
}

func (r *recordingOpener) open(name string) (capture.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
	if r.failFor[name] {
		return nil, fmt.Errorf("open %s: permission denied", name)
	}
	fs := newFakeSource()
	r.opened = append(r.opened, fs)
	return fs, nil
}

func (r *recordingOpener) last() *fakeSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opened[len(r.opened)-1]
}

func ifaceList(n int) []capture.Interface {
	ifaces := make([]capture.Interface, n)
	for i := range ifaces {
		ifaces[i] = capture.Interface{Name: fmt.Sprintf("eth%d", i)}
	}
	return ifaces
}

// runCmd executes cmd synchronously, flattening batches, and returns every
// non-nil message produced. Only use it on cmds that can't block (open and
// close, not waitForPacket/waitForSourceErr).
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func update(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	mUpdated, cmd := m.Update(msg)
	return mUpdated.(Model), cmd
}

// press sends key and delivers whatever the open/close cmds it triggers
// produce, i.e. a complete switch with an instantly-opening interface.
func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	m, cmd := update(t, m, keyMsg(key))
	for _, msg := range runCmd(cmd) {
		m, _ = update(t, m, msg)
	}
	return m
}

// liveOn returns a model live-tailing ifaces[0] of an n-interface list.
func liveOn(t *testing.T, n int) (Model, *recordingOpener) {
	t.Helper()
	r := &recordingOpener{}
	m := press(t, NewModel(ifaceList(n), r.open), "enter")
	if m.state != viewLiveTail || m.label != "eth0" {
		t.Fatalf("setup: state=%v label=%q, want live tail on eth0", m.state, m.label)
	}
	return m, r
}

// --- positive ---

func TestIfaceSwitch_TabCyclesForwardAndWraps(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 3)
	first := r.last()

	m = press(t, m, "tab")
	if m.label != "eth1" {
		t.Fatalf("label after tab = %q, want eth1", m.label)
	}
	if !first.closed {
		t.Fatal("tab didn't close the previous interface's source")
	}
	if m.source != r.last() || m.source == capture.Source(first) {
		t.Fatal("model isn't capturing from the newly opened source")
	}

	m = press(t, press(t, m, "tab"), "tab")
	if m.label != "eth0" {
		t.Fatalf("label after wrapping = %q, want eth0", m.label)
	}
	if got := strings.Join(r.names, ","); got != "eth0,eth1,eth2,eth0" {
		t.Fatalf("opened %s, want eth0,eth1,eth2,eth0", got)
	}
}

func TestIfaceSwitch_ShiftTabCyclesBackwardAndWraps(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 3)
	m = press(t, m, "shift+tab")
	if m.label != "eth2" {
		t.Fatalf("label after shift+tab from eth0 = %q, want eth2", m.label)
	}
	m = press(t, m, "shift+tab")
	if m.label != "eth1" {
		t.Fatalf("label after second shift+tab = %q, want eth1", m.label)
	}
}

func TestIfaceSwitch_StartsFreshViewAndArchivesOldRing(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 2)
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
	m, _ = update(t, m, tickMsg(time.Now()))

	m = press(t, m, "tab")
	if m.ring.Len() != 0 || len(m.displayed) != 0 {
		t.Fatalf("after switch ring=%d displayed=%d, want a fresh empty view", m.ring.Len(), len(m.displayed))
	}
	got, err := store.NewArchiveReader(dir).Range(start.Add(-time.Second), time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("archive Range error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("archive holds %d packets after switch, want the 5 from the old ring", len(got))
	}
}

func TestIfaceSwitch_KeepsFilterAndJumpsToLive(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 2)
	f, err := filter.ParseExpression("proto=udp")
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}
	m.filterBar.input.SetValue("proto=udp")
	m.filterBar.apply(f)
	m, _ = update(t, m, packetMsg(samplePacket(1)))
	m, _ = update(t, m, keyMsg("left"))
	if !m.timeline.active {
		t.Fatal("setup: left didn't enter rewind")
	}

	m = press(t, m, "tab")
	if m.timeline.active {
		t.Fatal("switching interface left the view rewound")
	}
	if m.filterBar.text != "proto=udp" {
		t.Fatalf("filter after switch = %q, want proto=udp kept", m.filterBar.text)
	}
}

func TestIfaceSwitch_ViewShowsPositionAndHelpMentionsTab(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 3)
	m = press(t, m, "tab")
	if out := m.View(); !strings.Contains(out, "eth1 [2/3") || !strings.Contains(out, "tab/shift+tab") {
		t.Fatalf("View() = %q, want the interface position and switch hint", out)
	}
	if !strings.Contains(helpText, "tab / shift+tab") {
		t.Fatal("help text doesn't document tab switching")
	}
}

func TestIfaceSwitch_ShowsOpeningStatusUntilOpened(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 2)
	m, cmd := update(t, m, keyMsg("tab"))
	if !strings.Contains(m.View(), "opening eth1...") {
		t.Fatalf("View() mid-switch = %q, want an opening status", m.View())
	}
	for _, msg := range runCmd(cmd) {
		m, _ = update(t, m, msg)
	}
	if m.status != "" {
		t.Fatalf("status after open = %q, want it cleared", m.status)
	}
}

func TestIfaceSwitch_DoesNotStartSecondTickChain(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 2)
	m, cmd := update(t, m, keyMsg("tab"))
	var opened tea.Msg
	for _, msg := range runCmd(cmd) {
		if _, ok := msg.(sourceOpenedMsg); ok {
			opened = msg
		}
	}
	_, cmd = update(t, m, opened)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("post-switch open cmd = %#v, want a batch of just the packet+error readers", batch)
	}
}

func TestModel_TickKeepsRunningOutsideLiveView(t *testing.T) {
	t.Parallel()
	m := NewModelWithSource("eth0", newFakeSource())
	m.state = viewHelp
	if _, cmd := update(t, m, tickMsg(time.Now())); cmd == nil {
		t.Fatal("tick stopped re-arming in the help view, so live tail would stop redrawing after closing it")
	}
}

// --- negative ---

func TestIfaceSwitch_NoopWithoutAnotherInterface(t *testing.T) {
	t.Parallel()
	remote := NewModelWithSource("host (eth0)", newFakeSource())
	if m, cmd := update(t, remote, keyMsg("tab")); cmd != nil || m.label != "host (eth0)" {
		t.Fatal("tab in remote mode tried to switch interface")
	}

	single, r := liveOn(t, 1)
	single = press(t, single, "tab")
	if single.label != "eth0" || len(r.names) != 1 || r.last().closed {
		t.Fatal("tab with a single interface reopened or closed capture")
	}
	if strings.Contains(single.View(), "tab/shift+tab") {
		t.Fatal("switch hint shown with nothing to switch to")
	}
}

func TestIfaceSwitch_OpenFailureShowsErrorAndTabStillWorks(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 3)
	r.failFor = map[string]bool{"eth1": true}

	m = press(t, m, "tab")
	if m.source != nil || m.state != viewLiveTail {
		t.Fatalf("failed open: source=%v state=%v, want no source, still live view", m.source, m.state)
	}
	if !strings.Contains(m.View(), "permission denied") {
		t.Fatalf("View() = %q, want the open error shown", m.View())
	}

	m = press(t, m, "tab")
	if m.label != "eth2" || m.source == nil || m.err != nil {
		t.Fatalf("after skipping past failure: label=%q source=%v err=%v", m.label, m.source, m.err)
	}
}

func TestIfaceSwitch_TabWhileEditingFilterDoesNotSwitch(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 2)
	m, _ = update(t, m, keyMsg("/"))
	m = press(t, m, "tab")
	if m.label != "eth0" || len(r.names) != 1 {
		t.Fatal("tab while editing the filter switched interface")
	}
}

// --- chaos ---

func TestIfaceSwitch_Chaos_LateMessagesFromOldSourceDropped(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 2)
	old := r.last()
	old.packets <- samplePacket(1)
	old.errs <- fmt.Errorf("old iface error")
	pending := []tea.Cmd{waitForPacket(old), waitForSourceErr(old)}

	m = press(t, m, "tab")
	for _, c := range pending {
		var cmd tea.Cmd
		m, cmd = update(t, m, c())
		if cmd != nil {
			t.Fatal("a stale message re-armed a reader")
		}
	}
	// Plus the close the old source's readers see once it's shut down.
	m, _ = update(t, m, waitForPacket(old)())
	if m.ring.Len() != 0 || m.err != nil {
		t.Fatalf("stale messages leaked into the new view: ring=%d err=%v", m.ring.Len(), m.err)
	}
}

func TestIfaceSwitch_Chaos_SupersededOpensAreClosed(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 3)
	m, first := update(t, m, keyMsg("tab"))
	m, second := update(t, m, keyMsg("tab"))
	firstMsgs, secondMsgs := runCmd(first), runCmd(second)

	// Deliver the newer open first, then the stale one.
	for _, msg := range append(secondMsgs, firstMsgs...) {
		m = deliver(t, m, msg)
	}
	if m.label != "eth2" {
		t.Fatalf("label = %q, want eth2", m.label)
	}
	for _, msg := range firstMsgs {
		if om, ok := msg.(sourceOpenedMsg); ok {
			if !om.source.(*fakeSource).closed {
				t.Fatal("superseded open's source was left open")
			}
			if m.source == om.source {
				t.Fatal("superseded open replaced the newer source")
			}
		}
	}
}

// deliver updates m with msg, also running the returned cmd when msg is a
// superseded open (that cmd is just its close). A current open's cmd is
// the blocking channel readers, so it's left unrun.
func deliver(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	om, isOpen := msg.(sourceOpenedMsg)
	stale := isOpen && om.gen != m.openGen
	m, cmd := update(t, m, msg)
	if stale {
		runCmd(cmd)
	}
	return m
}

func TestIfaceSwitch_Chaos_StaleOpenErrorIgnored(t *testing.T) {
	t.Parallel()
	m, _ := liveOn(t, 3)
	m, _ = update(t, m, keyMsg("tab"))
	m = press(t, m, "tab")
	m, _ = update(t, m, openErrMsg{err: fmt.Errorf("late"), gen: m.openGen - 1})
	if m.err != nil {
		t.Fatalf("stale open error surfaced: %v", m.err)
	}
}

// --- load ---

func TestIfaceSwitch_Load_SwitchingUnderTrafficStaysBounded(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 5)
	for round := 0; round < 200; round++ {
		for i := 0; i < 50; i++ {
			m, _ = update(t, m, packetMsg(samplePacket(i)))
		}
		m = press(t, m, "tab")
		if m.ring.Len() > ringCapacity {
			t.Fatalf("ring grew past capacity: %d", m.ring.Len())
		}
	}
	open := 0
	for _, fs := range r.opened {
		if !fs.closed {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("%d sources still open after 200 switches, want exactly 1", open)
	}
	if m.label != "eth0" { // 200 % 5 == 0
		t.Fatalf("label = %q, want eth0", m.label)
	}
}

// --- stress ---

func TestIfaceSwitch_Stress_BurstOfTabsOnlyLastOpenWins(t *testing.T) {
	t.Parallel()
	m, r := liveOn(t, 7)
	const presses = 2000
	var cmds []tea.Cmd
	want := 0
	for i := 0; i < presses; i++ {
		var cmd tea.Cmd
		key, delta := "tab", 1
		if i%3 == 0 {
			key, delta = "shift+tab", -1
		}
		want = (want + delta + 7) % 7
		m, cmd = update(t, m, keyMsg(key))
		cmds = append(cmds, cmd)
	}

	// Run every switch's open (and the first one's close) concurrently,
	// the way Bubble Tea runs cmds on their own goroutines.
	results := make([][]tea.Msg, len(cmds))
	var wg sync.WaitGroup
	for i, c := range cmds {
		wg.Add(1)
		go func(i int, c tea.Cmd) {
			defer wg.Done()
			results[i] = runCmd(c)
		}(i, c)
	}
	wg.Wait()

	for i := len(results) - 1; i >= 0; i-- { // newest first: every older one is stale
		for _, msg := range results[i] {
			m = deliver(t, m, msg)
		}
	}

	open := 0
	for _, fs := range r.opened {
		if !fs.closed {
			open++
			if m.source != capture.Source(fs) {
				t.Fatal("an open source isn't the model's current one")
			}
		}
	}
	if open != 1 {
		t.Fatalf("%d sources left open after a %d-press burst, want 1", open, presses)
	}
	if m.label != fmt.Sprintf("eth%d", want) {
		t.Fatalf("label = %q, want eth%d", m.label, want)
	}
}

// --- remote interface switching ---

// remoteLiveOn mirrors main's -remote -remote-iface path: live from the
// start on current, with the remote's list wired in afterward.
func remoteLiveOn(t *testing.T, ifaces []capture.Interface, current string) (Model, *recordingOpener) {
	t.Helper()
	r := &recordingOpener{}
	m := NewModelWithSource("box ("+current+")", newFakeSource())
	m.SetRemoteHost("box")
	m.SetInterfaces(ifaces, r.open, current)
	return m, r
}

func TestRemoteSwitch_TabCyclesRemoteInterfacesWithHostLabel(t *testing.T) {
	t.Parallel()
	m, r := remoteLiveOn(t, ifaceList(3), "eth1")
	if out := m.View(); !strings.Contains(out, "box (eth1) [2/3") {
		t.Fatalf("View() = %q, want positioned on eth1 with the host label", out)
	}
	m = press(t, m, "tab")
	if m.label != "box (eth2)" || r.names[0] != "eth2" {
		t.Fatalf("after tab: label=%q opened=%v, want box (eth2) opening eth2", m.label, r.names)
	}
	m = press(t, m, "tab")
	if m.label != "box (eth0)" {
		t.Fatalf("label after wrap = %q, want box (eth0)", m.label)
	}
}

func TestRemoteSwitch_PickerNamesHostAndOpensBareIface(t *testing.T) {
	t.Parallel()
	r := &recordingOpener{}
	m := NewModel(ifaceList(2), r.open)
	m.SetRemoteHost("box")
	if out := m.View(); !strings.Contains(out, "choose an interface on box") {
		t.Fatalf("picker = %q, want it to name the remote host", out)
	}
	m = press(t, press(t, m, "down"), "enter")
	if m.label != "box (eth1)" || len(r.names) != 1 || r.names[0] != "eth1" {
		t.Fatalf("label=%q opened=%v, want box (eth1) opening bare eth1", m.label, r.names)
	}
}

func TestRemoteSwitch_UnlistedCurrentIfaceIsPrependedAndReachable(t *testing.T) {
	t.Parallel()
	m, _ := remoteLiveOn(t, ifaceList(2), "tun0")
	if len(m.ifaces) != 3 || m.ifaces[0].Name != "tun0" || m.cursor != 0 {
		t.Fatalf("ifaces=%v cursor=%d, want tun0 prepended and selected", m.ifaces, m.cursor)
	}
	m = press(t, m, "shift+tab")
	m = press(t, m, "tab")
	if m.label != "box (tun0)" {
		t.Fatalf("label = %q, want cycling to reach tun0 again", m.label)
	}
}

func TestRemoteSwitch_SetStatusShownAndClearedBySwitch(t *testing.T) {
	t.Parallel()
	m := NewModelWithSource("box (eth0)", newFakeSource())
	m.SetStatus("interface switching unavailable: boom")
	if !strings.Contains(m.View(), "interface switching unavailable: boom") {
		t.Fatal("SetStatus text not shown in the live view")
	}
	if _, cmd := update(t, m, keyMsg("tab")); cmd != nil {
		t.Fatal("tab switched despite no interface list")
	}
}

func TestRemoteSwitch_Chaos_EmptyListOnlyHasCurrent(t *testing.T) {
	t.Parallel()
	m, r := remoteLiveOn(t, nil, "eth0")
	m = press(t, m, "tab")
	if len(r.names) != 0 || strings.Contains(m.View(), "tab/shift+tab") {
		t.Fatal("an empty remote list (just the current iface) still offered switching")
	}
}

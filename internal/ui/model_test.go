package ui

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/store"
)

// fakeSource is a minimal capture.Source double so the UI's message-driven
// Update logic is testable without libpcap, a real interface, or root.
type fakeSource struct {
	packets chan store.Packet
	errs    chan error
	closed  bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		packets: make(chan store.Packet, 16),
		errs:    make(chan error, 16),
	}
}

func (f *fakeSource) Packets() <-chan store.Packet { return f.packets }
func (f *fakeSource) Errors() <-chan error         { return f.errs }
func (f *fakeSource) Close() error {
	if !f.closed {
		f.closed = true
		close(f.packets)
		close(f.errs)
	}
	return nil
}

var _ capture.Source = (*fakeSource)(nil)

func samplePacket(n int) store.Packet {
	return store.Packet{
		Time:    time.Now(),
		Iface:   "eth0",
		SrcIP:   netip.MustParseAddr("10.0.0.1"),
		DstIP:   netip.MustParseAddr("10.0.0.2"),
		SrcPort: uint16(1000 + n),
		DstPort: 443,
		Proto:   store.ProtoTCP,
		Length:  60,
	}
}

// unwrapFromSource returns the message inside a fromSourceMsg, failing
// the test if it's tagged with a source other than want. Anything that
// isn't a fromSourceMsg (e.g. a tickMsg) is returned as is.
func unwrapFromSource(t *testing.T, msg tea.Msg, want capture.Source) tea.Msg {
	t.Helper()
	fm, ok := msg.(fromSourceMsg)
	if !ok {
		return msg
	}
	if fm.source != want {
		t.Fatalf("fromSourceMsg tagged with %v, want %v", fm.source, want)
	}
	return fm.msg
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "q":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// --- positive ---

func TestModel_PickerNavigation(t *testing.T) {
	t.Parallel()
	ifaces := []capture.Interface{{Name: "eth0"}, {Name: "lo"}, {Name: "wlan0"}}
	m := NewModel(ifaces, nil)

	mUpdated, _ := m.Update(keyMsg("down"))
	m = mUpdated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after one down", m.cursor)
	}

	mUpdated, _ = m.Update(keyMsg("down"))
	m = mUpdated.(Model)
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 after two downs", m.cursor)
	}

	mUpdated, _ = m.Update(keyMsg("up"))
	m = mUpdated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after up", m.cursor)
	}
}

func TestModel_EnterOpensSourceAndSwitchesView(t *testing.T) {
	t.Parallel()
	ifaces := []capture.Interface{{Name: "eth0"}}
	fs := newFakeSource()
	opened := false
	open := func(name string) (capture.Source, error) {
		opened = true
		if name != "eth0" {
			t.Fatalf("open called with %q, want eth0", name)
		}
		return fs, nil
	}
	m := NewModel(ifaces, open)

	mUpdated, cmd := m.Update(keyMsg("enter"))
	m = mUpdated.(Model)
	if cmd == nil {
		t.Fatal("Update(enter) returned nil cmd, want openSource cmd")
	}
	msg := cmd()
	opMsg, ok := msg.(sourceOpenedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want sourceOpenedMsg", msg)
	}
	if !opened {
		t.Fatal("SourceOpener was never invoked")
	}

	mUpdated, _ = m.Update(opMsg)
	m = mUpdated.(Model)
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want viewLiveTail after sourceOpenedMsg", m.state)
	}
}

func TestModel_PacketMsgAddsToRingAndRequestsNext(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, cmd := m.Update(packetMsg(samplePacket(1)))
	m = mUpdated.(Model)
	if m.ring.Len() != 1 {
		t.Fatalf("ring.Len() = %d, want 1", m.ring.Len())
	}
	if cmd == nil {
		t.Fatal("Update(packetMsg) returned nil cmd, want it to re-arm waitForPacket")
	}
}

func TestModel_ViewRendersWithoutPanicking(t *testing.T) {
	t.Parallel()
	ifaces := []capture.Interface{{Name: "eth0", Description: "Ethernet"}}
	m := NewModel(ifaces, nil)
	if out := m.View(); out == "" {
		t.Fatal("picker View() returned empty string")
	}

	fs := newFakeSource()
	live := NewModelWithSource("eth0", fs)
	mUpdated, _ := live.Update(packetMsg(samplePacket(1)))
	live = mUpdated.(Model)
	if out := live.View(); out == "" {
		t.Fatal("live-tail View() returned empty string")
	}
}

func TestModel_Init_LiveTailArmsPacketErrorAndTickCmds(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil cmd for a live-tail model with a source")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init() cmd() = %T, want tea.BatchMsg", msg)
	}
	if len(batch) != 3 {
		t.Fatalf("batch has %d cmds, want 3 (packet, error, tick)", len(batch))
	}

	// Give every sub-cmd something to receive so none of them block forever:
	// waitForPacket and waitForSourceErr are each one blocking channel read.
	fs.packets <- samplePacket(1)
	fs.errs <- fmt.Errorf("probe")

	gotPacket, gotErr, gotTick := false, false, false
	for _, sub := range batch {
		switch unwrapFromSource(t, sub(), fs).(type) {
		case packetMsg:
			gotPacket = true
		case sourceErrMsg:
			gotErr = true
		case tickMsg:
			gotTick = true
		}
	}
	if !gotPacket || !gotErr || !gotTick {
		t.Fatalf("batch sub-cmds produced packet=%v err=%v tick=%v, want all true", gotPacket, gotErr, gotTick)
	}
}

func TestModel_Init_PickerStateReturnsNilCmd(t *testing.T) {
	t.Parallel()
	m := NewModel([]capture.Interface{{Name: "eth0"}}, nil)
	if cmd := m.Init(); cmd != nil {
		t.Fatal("Init() in picker state returned a non-nil cmd")
	}
}

func TestModel_LiveTailArbitraryKeyPassesThroughToTable(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	mUpdated, _ := m.Update(packetMsg(samplePacket(1)))
	m = mUpdated.(Model)
	mUpdated, _ = m.Update(tickMsg(time.Now()))
	m = mUpdated.(Model)

	mUpdated, _ = m.Update(keyMsg("down"))
	m = mUpdated.(Model)
	_ = m // the table's own Update handled this; just confirming no panic/type assertion failure
}

func TestModel_ViewWithErrorSet(t *testing.T) {
	t.Parallel()
	m := NewModel([]capture.Interface{{Name: "eth0"}}, nil)
	mUpdated, _ := m.Update(openErrMsg{err: fmt.Errorf("denied")})
	m = mUpdated.(Model)
	if out := m.viewPicker(); !strings.Contains(out, "denied") {
		t.Fatalf("viewPicker() = %q, want it to mention the error", out)
	}

	fs := newFakeSource()
	live := NewModelWithSource("eth0", fs)
	mUpdated, _ = live.Update(sourceErrMsg{err: fmt.Errorf("boom")})
	live = mUpdated.(Model)
	if out := live.viewLiveTail(); !strings.Contains(out, "boom") {
		t.Fatalf("viewLiveTail() = %q, want it to mention the error", out)
	}
}

func TestEndpoint_ZeroPortOmitted(t *testing.T) {
	t.Parallel()
	if got := endpoint(netip.MustParseAddr("10.0.0.1"), 0); got != "10.0.0.1" {
		t.Fatalf("endpoint with zero port = %q, want bare IP", got)
	}
}

func TestWaitForPacket_ClosedChannelYieldsPacketsClosedMsg(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	fs.Close()
	msg := unwrapFromSource(t, waitForPacket(fs)(), fs)
	if _, ok := msg.(packetsClosedMsg); !ok {
		t.Fatalf("waitForPacket on closed source = %T, want packetsClosedMsg", msg)
	}
}

func TestWaitForSourceErr_ClosedChannelYieldsNil(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	fs.Close()
	if msg := waitForSourceErr(fs)(); msg != nil {
		t.Fatalf("waitForSourceErr on closed source = %v, want nil", msg)
	}
}

func TestWaitForSourceErr_DeliversError(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	fs.errs <- fmt.Errorf("link down")
	msg := unwrapFromSource(t, waitForSourceErr(fs)(), fs)
	se, ok := msg.(sourceErrMsg)
	if !ok || se.err == nil {
		t.Fatalf("waitForSourceErr = %#v, want a populated sourceErrMsg", msg)
	}
}

func TestEndpoint_InvalidIPShowsPlaceholder(t *testing.T) {
	t.Parallel()
	if got := endpoint(netip.Addr{}, 80); got != "?:80" {
		t.Fatalf("endpoint(invalid IP, 80) = %q, want ?:80", got)
	}
	if got := endpoint(netip.Addr{}, 0); got != "?" {
		t.Fatalf("endpoint(invalid IP, 0) = %q, want ?", got)
	}
}

// --- negative ---

func TestModel_PickerCursorClampsAtBounds(t *testing.T) {
	t.Parallel()
	ifaces := []capture.Interface{{Name: "eth0"}, {Name: "lo"}}
	m := NewModel(ifaces, nil)

	mUpdated, _ := m.Update(keyMsg("up")) // already at 0, should stay
	m = mUpdated.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want clamped at 0", m.cursor)
	}

	for i := 0; i < 10; i++ {
		mUpdated, _ = m.Update(keyMsg("down"))
		m = mUpdated.(Model)
	}
	if m.cursor != len(ifaces)-1 {
		t.Fatalf("cursor = %d, want clamped at %d", m.cursor, len(ifaces)-1)
	}
}

func TestModel_EnterWithNoInterfacesIsNoop(t *testing.T) {
	t.Parallel()
	m := NewModel(nil, func(string) (capture.Source, error) {
		t.Fatal("SourceOpener should not be called with an empty interface list")
		return nil, nil
	})
	mUpdated, cmd := m.Update(keyMsg("enter"))
	m = mUpdated.(Model)
	if cmd != nil {
		t.Fatal("Update(enter) with no interfaces returned a non-nil cmd")
	}
	if m.state != viewPickInterface {
		t.Fatalf("state = %v, want still viewPickInterface", m.state)
	}
}

func TestModel_OpenErrSetsErrAndStaysInPicker(t *testing.T) {
	t.Parallel()
	ifaces := []capture.Interface{{Name: "eth0"}}
	wantErr := fmt.Errorf("permission denied")
	m := NewModel(ifaces, func(string) (capture.Source, error) { return nil, wantErr })

	mUpdated, cmd := m.Update(keyMsg("enter"))
	m = mUpdated.(Model)
	msg := cmd()
	mUpdated, _ = m.Update(msg)
	m = mUpdated.(Model)

	if m.state != viewPickInterface {
		t.Fatalf("state = %v, want still viewPickInterface on open error", m.state)
	}
	if m.err == nil {
		t.Fatal("err is nil after an openErrMsg")
	}
}

func TestModel_QuitClosesSource(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("Update(q) returned nil cmd, want tea.Quit")
	}
	if !fs.closed {
		t.Fatal("source was not closed on quit")
	}
}

// --- load ---

func TestModel_Load_ManyPacketsStayWithinRingCapacity(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	const n = ringCapacity * 5
	for i := 0; i < n; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
	}
	if m.ring.Len() != ringCapacity {
		t.Fatalf("ring.Len() = %d, want capped at %d", m.ring.Len(), ringCapacity)
	}
	if !m.dirty {
		t.Fatal("dirty = false after adding packets, want true (pending a tick-driven redraw)")
	}

	// Table rebuilds are throttled to tickMsg (see renderInterval), not
	// rebuilt per packet - force one flush and confirm it catches up.
	mUpdated, _ := m.Update(tickMsg(time.Now()))
	m = mUpdated.(Model)
	if m.dirty {
		t.Fatal("dirty still true after a tickMsg")
	}
	if len(m.table.Rows()) != ringCapacity {
		t.Fatalf("table has %d rows, want %d", len(m.table.Rows()), ringCapacity)
	}
}

// --- stress ---

func TestModel_Stress_RapidViewAndWindowResize(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	for i := 0; i < 200; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
		mUpdated, _ = m.Update(tea.WindowSizeMsg{Width: 40 + i%100, Height: 10 + i%40})
		m = mUpdated.(Model)
		_ = m.View()
	}
}

// --- chaos ---

func TestModel_Chaos_UnexpectedMessageTypesDoNotPanic(t *testing.T) {
	t.Parallel()
	ifaces := []capture.Interface{{Name: "eth0"}}
	fs := newFakeSource()
	m := NewModel(ifaces, func(string) (capture.Source, error) { return fs, nil })

	msgs := []tea.Msg{
		nil,
		packetsClosedMsg{},
		sourceErrMsg{err: fmt.Errorf("boom")},
		openErrMsg{err: fmt.Errorf("boom")},
		tea.WindowSizeMsg{Width: 0, Height: 0},
		tea.WindowSizeMsg{Width: -5, Height: -5},
		keyMsg("ctrl+c"),
	}
	for i, msg := range msgs {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked on message %d (%T): %v", i, msg, rec)
				}
			}()
			mUpdated, cmd := m.Update(msg)
			m = mUpdated.(Model)
			if cmd != nil {
				_ = cmd() // exercise the returned command too (e.g. tea.Quit)
			}
		}()
	}
}

func TestModel_Chaos_InterleavedPacketsAndErrorsOutOfOrder(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	for i := 0; i < 500; i++ {
		var msg tea.Msg
		switch i % 3 {
		case 0:
			msg = packetMsg(samplePacket(i))
		case 1:
			msg = sourceErrMsg{err: fmt.Errorf("transient %d", i)}
		default:
			msg = packetsClosedMsg{}
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked at i=%d on %T: %v", i, msg, rec)
				}
			}()
			mUpdated, _ := m.Update(msg)
			m = mUpdated.(Model)
		}()
	}
}

func TestEndpoint_IPv6WithPortIsBracketed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"2001:db8::1|443":    "[2001:db8::1]:443",
		"2001:db8::1|0":      "2001:db8::1",
		"::ffff:10.0.0.1|80": "[::ffff:10.0.0.1]:80",
		"fe80::1%eth0|546":   "[fe80::1%eth0]:546",
		"10.0.0.1|65535":     "10.0.0.1:65535",
	}
	for in, want := range cases {
		addr, port, _ := strings.Cut(in, "|")
		var p uint16
		fmt.Sscan(port, &p)
		if got := endpoint(netip.MustParseAddr(addr), p); got != want {
			t.Errorf("endpoint(%s, %d) = %q, want %q", addr, p, got, want)
		}
	}
}

func TestAddrWidthFor_GrowsWithTerminalWithinBounds(t *testing.T) {
	t.Parallel()
	cases := []struct{ term, want int }{
		{0, minAddrWidth}, {-5, minAddrWidth}, {80, minAddrWidth},
		{fixedColsWidth + 2*30, 30}, {fixedColsWidth + 2*maxAddrWidth, maxAddrWidth}, {1000, maxAddrWidth},
	}
	for _, c := range cases {
		if got := addrWidthFor(c.term); got != c.want {
			t.Errorf("addrWidthFor(%d) = %d, want %d", c.term, got, c.want)
		}
	}
}

func TestModel_WideTerminalShowsFullIPv6Endpoint(t *testing.T) {
	t.Parallel()
	m := NewModelWithSource("eth0", newFakeSource())
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 140, Height: 30})
	p := samplePacket(1)
	p.SrcIP = netip.MustParseAddr("2001:db8:85a3::8a2e:370:7334")
	p.SrcPort = 51234
	m, _ = update(t, m, packetMsg(p))
	m, _ = update(t, m, tickMsg(time.Now()))
	if out := m.View(); !strings.Contains(out, "[2001:db8:85a3::8a2e:370:7334]:51234") {
		t.Fatalf("View() = %q, want the full bracketed IPv6 endpoint", out)
	}
}

func TestPicker_WindowsAdaptersShownByDescription(t *testing.T) {
	t.Parallel()
	var opened []string
	ifaces := []capture.Interface{
		{Name: `\Device\NPF_{4A1C2B3D-0000-1111-2222-333344445555}`, Description: "Intel(R) Ethernet Connection I219-V"},
		{Name: `\Device\NPF_Loopback`, Description: "Adapter for loopback traffic capture"},
	}
	m := NewModel(ifaces, func(name string) (capture.Source, error) {
		opened = append(opened, name)
		return newFakeSource(), nil
	})
	out := m.View()
	if strings.Contains(out, "NPF_") {
		t.Fatalf("picker shows raw Npcap device paths:\n%s", out)
	}
	if !strings.Contains(out, "> Intel(R) Ethernet Connection I219-V\n") {
		t.Fatalf("picker = %q, want the adapter's description as its name, shown once", out)
	}

	m, cmd := update(t, m, keyMsg("enter"))
	m, _ = update(t, m, cmd())
	if len(opened) != 1 || opened[0] != ifaces[0].Name {
		t.Fatalf("opened %q, want the device path %q - capture needs the real name", opened, ifaces[0].Name)
	}
	if !strings.Contains(m.View(), "live: Intel(R) Ethernet Connection I219-V [1/2") {
		t.Fatalf("live header = %q, want the description", strings.SplitN(m.View(), "\n", 2)[0])
	}
}

func TestPicker_NameColumnFitsLongestName(t *testing.T) {
	t.Parallel()
	m := NewModel([]capture.Interface{
		{Name: "eth0"},
		{Name: "bluetooth-monitor", Description: "Bluetooth Linux Monitor"},
		{Name: "nflog", Description: "Linux netfilter log (NFLOG) interface"},
	}, nil)
	// Each row's second column starts right after the longest name
	// ("bluetooth-monitor", 17 chars) plus the 2-char cursor and a space.
	const descCol = 2 + 17 + 1
	for _, want := range []string{"-", "Bluetooth Linux Monitor", "Linux netfilter"} {
		found := false
		for _, line := range strings.Split(m.View(), "\n") {
			if len(line) > descCol && strings.HasPrefix(line[descCol:], want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no row has %q at column %d:\n%s", want, descCol, m.View())
		}
	}
}

func TestPickerNameWidth_CappedForPathologicalNames(t *testing.T) {
	t.Parallel()
	long := capture.Interface{Name: strings.Repeat("x", 500), Description: "d"}
	if w := pickerNameWidth([]capture.Interface{long}); w != 24 {
		t.Fatalf("pickerNameWidth = %d, want the 24-column cap", w)
	}
	if w := pickerNameWidth(nil); w != 0 {
		t.Fatalf("pickerNameWidth(nil) = %d, want 0", w)
	}
}

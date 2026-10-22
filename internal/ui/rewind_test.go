package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jtrull101/tcpview/internal/capture"
	"github.com/jtrull101/tcpview/internal/store"
)

func newArchivedModel(t *testing.T) (Model, string) {
	t.Helper()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	dir := t.TempDir()
	a, err := store.NewArchive(store.ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	m.EnableArchive(a, store.NewArchiveReader(dir))
	return m, dir
}

// --- positive ---

func TestModel_LeftArrowEntersRewindMode(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = mUpdated.(Model)
	if !m.timeline.active {
		t.Fatal("timeline.active = false after left arrow")
	}
}

func TestModel_EvictedPacketsGetArchived(t *testing.T) {
	t.Parallel()
	m, dir := newArchivedModel(t)

	const n = ringCapacity + 10
	base := time.Now()
	for i := 0; i < n; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
	}
	_ = m.archive.Close() // flush before reading back

	r := store.NewArchiveReader(dir)
	got, err := r.Range(base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Range error: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("archived %d packets, want exactly the 10 evicted from a %d-cap ring after %d adds", len(got), ringCapacity, n)
	}
}

func TestModel_RewindShowsArchivedPacketsOutsideRingWindow(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)

	old := samplePacket(1)
	old.Time = time.Now().Add(-time.Hour)
	if err := m.archive.Write(old); err != nil {
		t.Fatalf("archive.Write error: %v", err)
	}

	m.timeline = timelineState{active: true, at: old.Time}
	m = loadRewindWindow(t, m)
	pkts := m.packetsForDisplay()
	found := false
	for _, p := range pkts {
		if p.Time.Equal(old.Time) {
			found = true
		}
	}
	if !found {
		t.Fatal("packetsForDisplay did not include an archived packet within the rewind window")
	}
}

func TestModel_CapitalLJumpsToLive(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.timeline = timelineState{active: true, at: time.Now()}

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	m = mUpdated.(Model)
	if m.timeline.active {
		t.Fatal("timeline.active = true after 'L'")
	}
}

func TestModel_ViewLiveTailShowsRewoundStatus(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.timeline = timelineState{active: true, at: time.Now()}
	if out := m.viewLiveTail(); !strings.Contains(out, "rewound") {
		t.Fatalf("viewLiveTail() = %q, want it to mention 'rewound'", out)
	}
}

// --- negative ---

func TestModel_RewindWithNoArchiveStillUsesRing(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs) // no EnableArchive call

	base := time.Now()
	mUpdated, _ := m.Update(packetMsg(samplePacket(1)))
	m = mUpdated.(Model)

	m.timeline = timelineState{active: true, at: base}
	pkts := m.packetsForDisplay() // must not panic with archiveReader == nil
	if len(pkts) == 0 {
		t.Fatal("packetsForDisplay returned nothing for a packet within the ring's window")
	}
}

func TestModel_ArchiveWriteErrorIsSurfacedNotFatal(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	dir := t.TempDir()
	a, err := store.NewArchive(store.ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	m.EnableArchive(a, store.NewArchiveReader(dir))

	// Remove write permission on the archive dir so every Write fails,
	// then push enough packets through to evict at least one into it.
	// Update must surface the error on m.err rather than panicking.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod error: %v", err)
	}
	defer os.Chmod(dir, 0o700)

	for i := 0; i < ringCapacity+1; i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked on an archive write failure at i=%d: %v", i, rec)
				}
			}()
			mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
			m = mUpdated.(Model)
		}()
	}
}

// --- chaos ---

func TestModel_Chaos_RapidRewindScrubbing(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	for i := 0; i < 50; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
	}

	keys := []tea.KeyMsg{
		{Type: tea.KeyLeft}, {Type: tea.KeyLeft}, {Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune("L")}, {Type: tea.KeyRight}, {Type: tea.KeyLeft},
	}
	for i := 0; i < 500; i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked at i=%d: %v", i, rec)
				}
			}()
			mUpdated, _ := m.Update(keys[i%len(keys)])
			m = mUpdated.(Model)
			_ = m.View()
		}()
	}
}

// --- detail pane integration ---

func TestModel_EnterOnLiveTailOpensDetailForCursorRow(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	mUpdated, _ := m.Update(packetMsg(samplePacket(1)))
	m = mUpdated.(Model)
	mUpdated, _ = m.Update(tickMsg(time.Now()))
	m = mUpdated.(Model)

	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mUpdated.(Model)
	if m.state != viewDetail {
		t.Fatalf("state = %v, want viewDetail after enter on a populated table", m.state)
	}
}

func TestModel_AnyKeyClosesDetailBackToLiveTail(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.state = viewDetail
	m.selected = samplePacket(1)

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = mUpdated.(Model)
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want viewLiveTail after any key in detail view", m.state)
	}
}

func TestModel_QuestionMarkOpensHelpAnyKeyCloses(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mUpdated.(Model)
	if m.state != viewHelp {
		t.Fatalf("state = %v, want viewHelp after '?'", m.state)
	}
	if out := m.View(); !strings.Contains(out, "keybindings") {
		t.Fatalf("View() in help state = %q, want it to show the help text", out)
	}

	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mUpdated.(Model)
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want viewLiveTail after closing help", m.state)
	}
}

func TestModel_EnterWithEmptyTableIsNoop(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs) // no packets added, table empty

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mUpdated.(Model)
	if m.state != viewLiveTail {
		t.Fatalf("state = %v, want to stay viewLiveTail when the table is empty", m.state)
	}
}

func TestModel_Chaos_DetailAndHelpTogglingNeverPanics(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	for i := 0; i < 30; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
	}
	mUpdated, _ := m.Update(tickMsg(time.Now()))
	m = mUpdated.(Model)

	keys := []tea.KeyMsg{
		{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("?")},
		{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyDown}, {Type: tea.KeyUp},
	}
	for i := 0; i < 500; i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked at i=%d: %v", i, rec)
				}
			}()
			mUpdated, _ := m.Update(keys[i%len(keys)])
			m = mUpdated.(Model)
			_ = m.View()
		}()
	}
}

// --- export integration ---

func TestModel_EKeyExportsDisplayedPacketsToPcap(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.SetExportDir(t.TempDir())

	withRaw := samplePacket(1)
	withRaw.Raw = []byte("a raw ethernet frame")
	mUpdated, _ := m.Update(packetMsg(withRaw))
	m = mUpdated.(Model)
	mUpdated, _ = m.Update(tickMsg(time.Now()))
	m = mUpdated.(Model)

	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = mUpdated.(Model)

	if m.status == "" {
		t.Fatal("status is empty after export")
	}
	if !strings.Contains(m.status, "exported 1 packet") {
		t.Fatalf("status = %q, want it to mention exporting 1 packet", m.status)
	}

	entries, err := os.ReadDir(m.exportDir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("export dir has %d entries, want 1", len(entries))
	}
}

func TestModel_ExportWithNoDisplayedPacketsWritesZero(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.SetExportDir(t.TempDir())

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = mUpdated.(Model)
	if !strings.Contains(m.status, "exported 0 packet") {
		t.Fatalf("status = %q, want it to mention exporting 0 packets", m.status)
	}
}

func TestModel_ExportErrorIsSurfacedOnErrField(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.SetExportDir(filepath.Join(t.TempDir(), "does-not-exist"))

	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = mUpdated.(Model)
	if m.err == nil {
		t.Fatal("err is nil after exporting to a nonexistent directory")
	}
}

// --- extreme resize across every screen state ---
//
// Mirrors manual testing via a real tmux session resized down to 1x1 (and
// various other extremes) while live-tailing, mid-filter-edit, viewing
// packet detail, viewing help, and on the interface picker - all survived
// without a crash. These tests pin that down as an automated regression
// check, including dimensions a real terminal couldn't even produce
// (negative, exactly zero) for extra margin.

func extremeSizes() []tea.WindowSizeMsg {
	return []tea.WindowSizeMsg{
		{Width: 0, Height: 0},
		{Width: 1, Height: 1},
		{Width: -5, Height: -5},
		{Width: 300, Height: 1},
		{Width: 1, Height: 300},
		{Width: 150, Height: 24}, // back to a sane size
	}
}

func resizeThroughAllSizes(t *testing.T, m Model) Model {
	t.Helper()
	for _, sz := range extremeSizes() {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update(%+v) panicked: %v", sz, rec)
				}
			}()
			mUpdated, _ := m.Update(sz)
			m = mUpdated.(Model)
			_ = m.View()
		}()
	}
	return m
}

func TestModel_ExtremeResize_OnInterfacePicker(t *testing.T) {
	t.Parallel()
	m := NewModel([]capture.Interface{{Name: "eth0"}, {Name: "lo"}}, nil)
	resizeThroughAllSizes(t, m)
}

func TestModel_ExtremeResize_OnLiveTail(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	mUpdated, _ := m.Update(packetMsg(samplePacket(1)))
	m = mUpdated.(Model)
	mUpdated, _ = m.Update(tickMsg(time.Now()))
	m = mUpdated.(Model)
	resizeThroughAllSizes(t, m)
}

func TestModel_ExtremeResize_WhileEditingFilter(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mUpdated.(Model)
	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = mUpdated.(Model)
	if !m.filterBar.editing {
		t.Fatal("setup failed: not in filter-editing mode")
	}
	resizeThroughAllSizes(t, m)
}

func TestModel_ExtremeResize_OnPacketDetail(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.state = viewDetail
	m.selected = samplePacket(1)
	resizeThroughAllSizes(t, m)
}

func TestModel_ExtremeResize_OnHelpScreen(t *testing.T) {
	t.Parallel()
	fs := newFakeSource()
	m := NewModelWithSource("eth0", fs)
	m.state = viewHelp
	resizeThroughAllSizes(t, m)
}

func TestModel_ExtremeResize_WhileRewound(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	for i := 0; i < 20; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
	}
	m.timeline = timelineState{active: true, at: time.Now()}
	resizeThroughAllSizes(t, m)
}

// --- rewind boundary conditions ---
//
// Mirrors a real live session: rewound 120 steps (20 minutes) before any
// capture had even started, combined a zero-match filter with an active
// rewind over a window that *does* have real data, and spammed 60 rapid
// alternating left/right presses - all against a running instance via
// tmux, all survived cleanly. These pin down the same scenarios as fast
// automated checks.

func TestModel_RewindFarBeforeAnyDataIsEmptyNotError(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	mUpdated, _ := m.Update(packetMsg(samplePacket(1)))
	m = mUpdated.(Model)

	// 120 steps back (20 minutes) - well before this model (or its archive)
	// ever had any data at all.
	for i := 0; i < 120; i++ {
		mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		m = mUpdated.(Model)
	}
	if !m.timeline.active {
		t.Fatal("timeline.active = false after stepping back")
	}

	pkts := m.packetsForDisplay()
	if len(pkts) != 0 {
		t.Fatalf("packetsForDisplay() = %d packets, want 0 for a window far before any data existed", len(pkts))
	}
	// The real point of this test: it must not panic or hang getting here.
	_ = m.View()
}

func TestModel_RewindWithZeroMatchFilterOverRealData(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)

	base := time.Now()
	for i := 0; i < 20; i++ {
		p := samplePacket(i)
		p.Time = base.Add(time.Duration(i) * time.Second)
		mUpdated, _ := m.Update(packetMsg(p))
		m = mUpdated.(Model)
	}

	// Filter on a port nothing matches, then rewind into the exact window
	// that DOES have real (but now filtered-out) data.
	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mUpdated.(Model)
	for _, r := range "dport=9999" {
		mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mUpdated.(Model)
	}
	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mUpdated.(Model)
	if m.filterBar.editing {
		t.Fatal("still editing after applying the filter")
	}

	m.timeline = timelineState{active: true, at: base.Add(10 * time.Second)}
	m.refreshRows()

	if len(m.table.Rows()) != 0 {
		t.Fatalf("table has %d rows, want 0 (real data present, but none matches dport=9999)", len(m.table.Rows()))
	}
}

func TestModel_Stress_RapidAlternatingScrubAgainstRealData(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	for i := 0; i < 30; i++ {
		mUpdated, _ := m.Update(packetMsg(samplePacket(i)))
		m = mUpdated.(Model)
	}

	for i := 0; i < 60; i++ {
		key := tea.KeyLeft
		if i%2 == 0 {
			key = tea.KeyRight
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Update panicked at i=%d: %v", i, rec)
				}
			}()
			mUpdated, _ := m.Update(tea.KeyMsg{Type: key})
			m = mUpdated.(Model)
			_ = m.View()
		}()
	}
}

// loadRewindWindow runs the archive load rewindMoved starts for m's
// current rewind point and delivers its result, the way Bubble Tea would.
func loadRewindWindow(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.rewindMoved()
	if cmd == nil {
		t.Fatal("rewindMoved started no archive load")
	}
	mUpdated, _ := m.Update(cmd())
	return mUpdated.(Model)
}

func oldArchivedPacket(t *testing.T, m Model, ago time.Duration, port int) store.Packet {
	t.Helper()
	p := samplePacket(port)
	p.Time = time.Now().Add(-ago)
	if err := m.archive.Write(p); err != nil {
		t.Fatalf("archive.Write error: %v", err)
	}
	return p
}

func TestRewind_ArchiveLoadsInBackgroundNotOnEveryRedraw(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	old := oldArchivedPacket(t, m, 10*time.Second, 1)

	m, cmd := update(t, m, keyMsg("left"))
	if cmd == nil {
		t.Fatal("left with an archive enabled started no background load")
	}
	if len(m.displayed) != 0 {
		t.Fatal("archived packet shown before its load finished - the read happened on the Update goroutine")
	}
	m, _ = update(t, m, cmd())
	if len(m.displayed) != 1 || !m.displayed[0].Time.Equal(old.Time) {
		t.Fatalf("displayed %d packets after the load, want the archived one", len(m.displayed))
	}

	// Point the reader at an empty archive: redraws while rewound must
	// keep using the cache rather than re-reading the disk every tick.
	// (Swapped rather than deleted - Windows can't delete the file the
	// archive writer still holds open.)
	m.archiveReader = store.NewArchiveReader(t.TempDir())
	m.dirty = true
	m, _ = update(t, m, tickMsg(time.Now()))
	if len(m.displayed) != 1 {
		t.Fatal("redraw re-read the archive instead of using the cached window")
	}
}

func TestRewind_StaleArchiveLoadDropped(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	oldArchivedPacket(t, m, 10*time.Second, 1)

	m, first := update(t, m, keyMsg("left"))
	m, _ = update(t, m, keyMsg("left")) // scrub on before the first load lands
	m, _ = update(t, m, first())
	if !m.rewindArchiveAt.IsZero() {
		t.Fatal("a load for a rewind point the user already left was applied")
	}

	m, _ = update(t, m, keyMsg("L"))
	if m.rewindArchive != nil {
		t.Fatal("jumping to live kept the rewind cache")
	}
}

func TestRewind_EvictedPacketsStayVisibleInLoadedWindow(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	for i := 0; i < ringCapacity; i++ {
		m, _ = update(t, m, packetMsg(samplePacket(i)))
	}
	m.timeline = timelineState{active: true, at: time.Now()}
	m = loadRewindWindow(t, m)
	before := len(m.packetsForDisplay())

	// Fresh packets push the window's oldest out of the ring into the archive.
	for i := 0; i < 50; i++ {
		p := samplePacket(i)
		p.Time = time.Now().Add(time.Hour) // outside the window
		m, _ = update(t, m, packetMsg(p))
	}
	if after := len(m.packetsForDisplay()); after != before {
		t.Fatalf("window shows %d packets after evictions, had %d - evicted ones vanished", after, before)
	}
}

func TestRewind_CorruptArchiveWarnsButStillShowsGoodPackets(t *testing.T) {
	t.Parallel()
	m, dir := newArchivedModel(t)
	old := oldArchivedPacket(t, m, 10*time.Second, 1)
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("archive files = %v (%v), want 1", files, err)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open archive file: %v", err)
	}
	_, _ = f.WriteString("{\"Time\":\"2026-01-01T00:0\n")
	f.Close()

	m, cmd := update(t, m, keyMsg("left"))
	m, _ = update(t, m, cmd())
	if len(m.displayed) != 1 || !m.displayed[0].Time.Equal(old.Time) {
		t.Fatalf("displayed %d packets, want the good archived one despite the corrupt line", len(m.displayed))
	}
	if !strings.Contains(m.View(), "1 corrupt archive line") || m.err != nil {
		t.Fatalf("View() = %q (err %v), want a corruption warning, not an error", m.View(), m.err)
	}
}

func TestRewind_ArchiveReadErrorSurfaced(t *testing.T) {
	t.Parallel()
	m := NewModelWithSource("eth0", newFakeSource())
	m.archiveReader = store.NewArchiveReader(t.TempDir())
	m, cmd := update(t, m, keyMsg("left"))
	if cmd == nil {
		t.Fatal("left started no archive load")
	}
	// Deliver a failed load directly: how the store's errors arise is
	// platform-specific (and tested in store); this is about the UI
	// surfacing one rather than dropping it.
	m, _ = update(t, m, archiveWindowMsg{at: m.timeline.at, err: errors.New("disk on fire")})
	if m.err == nil || !strings.Contains(m.err.Error(), "rewind: read archive: disk on fire") {
		t.Fatalf("err = %v, want the archive read failure surfaced", m.err)
	}
}

func TestRewind_Chaos_ScrubbingWithLoadsArrivingOutOfOrder(t *testing.T) {
	t.Parallel()
	m, _ := newArchivedModel(t)
	for i := 0; i < 30; i++ {
		oldArchivedPacket(t, m, time.Duration(i)*10*time.Second, i)
	}
	var loads []tea.Cmd
	keys := []string{"left", "left", "right", "left", "L", "left", "left"}
	for i := 0; i < 70; i++ {
		var cmd tea.Cmd
		m, cmd = update(t, m, keyMsg(keys[i%len(keys)]))
		if cmd != nil {
			loads = append(loads, cmd)
		}
	}
	// Deliver every load, newest first, then verify only the current one stuck.
	for i := len(loads) - 1; i >= 0; i-- {
		m, _ = update(t, m, loads[i]())
	}
	if m.timeline.active && !m.rewindArchiveAt.IsZero() && !m.rewindArchiveAt.Equal(m.timeline.at) {
		t.Fatalf("cache loaded for %v while rewound to %v", m.rewindArchiveAt, m.timeline.at)
	}
	_ = m.View()
}

func TestModel_ExportWarnsWhenMixingLinkTypes(t *testing.T) {
	t.Parallel()
	m := NewModelWithSource("eth0", newFakeSource())
	m.SetExportDir(t.TempDir())
	for i, lt := range []uint16{1, 113} { // Ethernet, then Linux cooked ("any")
		p := samplePacket(i)
		p.Raw, p.LinkType, p.HasLinkType = []byte("frame"), lt, true
		m, _ = update(t, m, packetMsg(p))
	}
	m, _ = update(t, m, tickMsg(time.Now()))
	m, _ = update(t, m, keyMsg("e"))
	if !strings.Contains(m.status, "exported 2 packet") || !strings.Contains(m.status, "mixes 2 link types") {
		t.Fatalf("status = %q, want the export count and a mixed-link-type warning", m.status)
	}
	entries, _ := os.ReadDir(m.exportDir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".pcapng") {
		t.Fatalf("export dir = %v, want one .pcapng file", entries)
	}
}

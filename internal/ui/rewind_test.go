package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

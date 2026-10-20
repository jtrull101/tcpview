package store

import (
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func archivePacket(t time.Time, summary string) Packet {
	p := pkt(t, summary)
	p.Raw = []byte("raw-" + summary)
	return p
}

// --- positive ---

func TestArchive_WriteAndReadBackRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now().UTC()
	want := []Packet{
		archivePacket(base, "a"),
		archivePacket(base.Add(time.Second), "b"),
		archivePacket(base.Add(2*time.Second), "c"),
	}
	for _, p := range want {
		if err := a.Write(p); err != nil {
			t.Fatalf("Write error: %v", err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	r := NewArchiveReader(dir)
	got, err := r.Range(base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("Range error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d packets, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p.Summary != want[i].Summary {
			t.Fatalf("packet %d: Summary = %q, want %q", i, p.Summary, want[i].Summary)
		}
		if string(p.Raw) != string(want[i].Raw) {
			t.Fatalf("packet %d: Raw = %q, want %q", i, p.Raw, want[i].Raw)
		}
		if !p.SrcIP.IsValid() || p.SrcIP != want[i].SrcIP {
			t.Fatalf("packet %d: SrcIP = %v, want %v", i, p.SrcIP, want[i].SrcIP)
		}
		if !p.Time.Equal(want[i].Time) {
			t.Fatalf("packet %d: Time = %v, want %v", i, p.Time, want[i].Time)
		}
	}
}

func TestArchive_RotatesOnFileAge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileAge: 10 * time.Millisecond, MaxFileBytes: 1 << 30, MaxTotalBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	if err := a.Write(archivePacket(base, "a")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := a.Write(archivePacket(base.Add(time.Second), "b")); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	files, err := listArchiveFiles(dir)
	if err != nil {
		t.Fatalf("listArchiveFiles error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2 (one rotation due to age)", len(files))
	}
}

// --- default-file-size-vs-total-cap clamping (see withDefaults) ---
//
// Found via a real sustained-load test: setting a total cap smaller than
// the 32 MiB *default* per-file size let a single file balloon well past
// the configured cap before the first rotation ever happened, since the
// in-flight file is exempt from eviction. Only the default should clamp -
// an explicitly-chosen MaxFileBytes (however large) must be left alone,
// since TestArchive_Stress_CurrentFileNeverDeletedMidWrite deliberately
// relies on that to test the exemption itself.

func TestArchiveOptions_DefaultFileSizeClampsToSmallTotalCap(t *testing.T) {
	t.Parallel()
	got := ArchiveOptions{MaxTotalBytes: 1024}.withDefaults()
	if got.MaxFileBytes != 1024 {
		t.Fatalf("MaxFileBytes = %d, want clamped to MaxTotalBytes (1024)", got.MaxFileBytes)
	}
}

func TestArchiveOptions_ExplicitFileSizeIsNeverClamped(t *testing.T) {
	t.Parallel()
	got := ArchiveOptions{MaxFileBytes: 1 << 30, MaxTotalBytes: 1}.withDefaults()
	if got.MaxFileBytes != 1<<30 {
		t.Fatalf("MaxFileBytes = %d, want the explicit 1GiB value left untouched", got.MaxFileBytes)
	}
}

func TestArchiveOptions_DefaultFileSizeUnchangedWhenCapIsLarge(t *testing.T) {
	t.Parallel()
	got := ArchiveOptions{MaxTotalBytes: 10 << 30}.withDefaults() // 10 GiB cap, way above the 32 MiB default
	if got.MaxFileBytes != 32<<20 {
		t.Fatalf("MaxFileBytes = %d, want the unclamped 32 MiB default", got.MaxFileBytes)
	}
}

func TestArchive_Stress_SmallCapNeverOvershootsBeyondOneFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const capBytes = 50 * 1024 // 50 KiB total cap, well under the 32 MiB default file size
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxTotalBytes: capBytes})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	payload := make([]byte, 500)
	for i := 0; i < 2000; i++ {
		p := archivePacket(base.Add(time.Duration(i)*time.Millisecond), "x")
		p.Raw = payload
		if err := a.Write(p); err != nil {
			t.Fatalf("Write %d error: %v", i, err)
		}
	}

	total, err := a.TotalBytes()
	if err != nil {
		t.Fatalf("TotalBytes error: %v", err)
	}
	// With the default file size now clamped to the cap itself, total
	// on-disk size should track close to the cap (plus one record's worth
	// of slack for whatever was mid-write when the cap was last checked),
	// not balloon toward the old unclamped 32 MiB default.
	if total > capBytes+4096 {
		t.Fatalf("TotalBytes() = %d, want close to the %d-byte cap (old bug: could reach the unclamped default file size)", total, capBytes)
	}
}

func TestArchive_RotatesOnFileSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileAge: time.Hour, MaxFileBytes: 200, MaxTotalBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	for i := 0; i < 20; i++ {
		p := archivePacket(base.Add(time.Duration(i)*time.Millisecond), "payload-filler-text")
		if err := a.Write(p); err != nil {
			t.Fatalf("Write %d error: %v", i, err)
		}
	}

	files, err := listArchiveFiles(dir)
	if err != nil {
		t.Fatalf("listArchiveFiles error: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("len(files) = %d, want at least 2 (size-based rotation)", len(files))
	}
}

// --- negative ---

func TestNewArchive_EmptyDirErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewArchive(ArchiveOptions{}); err == nil {
		t.Fatal("NewArchive with empty Dir returned no error")
	}
}

func TestNewArchive_UncreatableDirErrors(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	// A regular file can't have subdirectories created under it, so
	// MkdirAll must fail.
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if _, err := NewArchive(ArchiveOptions{Dir: filepath.Join(parent, "sub")}); err == nil {
		t.Fatal("NewArchive under a file (not a directory) returned no error")
	}
}

func TestArchive_RotateFailureSurfacesFromWrite(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // r-x, no write
		t.Fatalf("Chmod error: %v", err)
	}
	defer os.Chmod(dir, 0o700)

	if err := a.Write(archivePacket(time.Now(), "a")); err == nil {
		t.Fatal("Write into a read-only archive dir returned no error")
	}
}

func TestArchiveReader_MalformedFilenameIsSkippedByParseStart(t *testing.T) {
	t.Parallel()
	if _, ok := parseArchiveFileStart(archivePrefix + "not-a-timestamp" + archiveSuffix); ok {
		t.Fatal("parseArchiveFileStart succeeded on a malformed timestamp")
	}
}

func TestReadArchiveFile_SkipsBlankLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "blank.jsonl")
	content := `{"Time":"2026-01-01T00:00:00Z","Summary":"a"}` + "\n\n" + `{"Time":"2026-01-01T00:00:01Z","Summary":"b"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	pkts, err := readArchiveFile(path)
	if err != nil {
		t.Fatalf("readArchiveFile error: %v", err)
	}
	if len(pkts) != 2 {
		t.Fatalf("len(pkts) = %d, want 2 (blank line skipped)", len(pkts))
	}
}

func TestArchiveReader_NoSuchDirReturnsEmptyNotError(t *testing.T) {
	t.Parallel()
	r := NewArchiveReader(filepath.Join(t.TempDir(), "does-not-exist"))
	got, err := r.Range(time.Time{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Range error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d packets from a nonexistent dir, want 0", len(got))
	}
}

func TestArchiveReader_RangeOutsideWindowReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	base := time.Now()
	_ = a.Write(archivePacket(base, "a"))
	_ = a.Close()

	r := NewArchiveReader(dir)
	got, err := r.Range(base.Add(time.Hour), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Range error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d packets outside the window, want 0", len(got))
	}
}

func TestReadArchiveFile_CorruptLineErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.jsonl")
	if err := os.WriteFile(path, []byte("not valid json\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if _, err := readArchiveFile(path); err == nil {
		t.Fatal("readArchiveFile on a corrupt line returned no error")
	}
}

func TestReadArchiveFile_MissingFileErrors(t *testing.T) {
	t.Parallel()
	if _, err := readArchiveFile(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("readArchiveFile on a missing file returned no error")
	}
}

// TestArchive_SurvivesAbruptTerminationWithoutClose models what actually
// happens on SIGTERM/SIGKILL: Close() (and all other deferred cleanup)
// never runs at all. Each Write already flushes synchronously, so the
// on-disk bytes after a real signal kill are indistinguishable from the
// on-disk bytes right here, before ever calling Close - this is a faithful,
// repeatable proxy for abrupt termination without needing to actually
// fork/signal/kill a real process from within a test.
//
// Verified for real too: sent SIGTERM and SIGKILL to an actual running
// tcpview process mid-capture and checked the resulting archive file by
// hand - every line parsed as valid JSON both times, matching what this
// test asserts.
func TestArchive_SurvivesAbruptTerminationWithoutClose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}

	base := time.Now()
	const n = 500
	for i := 0; i < n; i++ {
		if err := a.Write(archivePacket(base.Add(time.Duration(i)*time.Millisecond), "x")); err != nil {
			t.Fatalf("Write %d error: %v", i, err)
		}
	}
	// Deliberately no a.Close() here - that's the point of this test.

	r := NewArchiveReader(dir)
	got, err := r.Range(base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("Range error on an archive that was never cleanly closed: %v", err)
	}
	if len(got) != n {
		t.Fatalf("read back %d packets, want all %d despite no clean Close", len(got), n)
	}

	_ = a.Close() // tidy up the test's own fd; irrelevant to the assertion above
}

func TestArchive_Close_NoWritesIsNoop(t *testing.T) {
	t.Parallel()
	a, err := NewArchive(ArchiveOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close on a never-written archive error: %v", err)
	}
}

func TestArchive_TotalBytes_PropagatesListError(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("Chmod error: %v", err)
	}
	defer os.Chmod(dir, 0o700)

	if _, err := a.TotalBytes(); err == nil {
		t.Fatal("TotalBytes on an unreadable dir returned no error")
	}
}

func TestArchiveReader_Range_PropagatesListError(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission denial doesn't apply")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("Chmod error: %v", err)
	}
	defer os.Chmod(dir, 0o700)

	r := NewArchiveReader(dir)
	if _, err := r.Range(time.Time{}, time.Now()); err == nil {
		t.Fatal("Range on an unreadable dir returned no error")
	}
}

// --- load ---

func TestArchive_Load_ManyWritesStayFunctional(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	const n = 20_000
	for i := 0; i < n; i++ {
		p := archivePacket(base.Add(time.Duration(i)*time.Microsecond), "x")
		if err := a.Write(p); err != nil {
			t.Fatalf("Write %d error: %v", i, err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	r := NewArchiveReader(dir)
	got, err := r.Range(base.Add(-time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Range error: %v", err)
	}
	if len(got) != n {
		t.Fatalf("got %d packets, want %d", len(got), n)
	}
}

// --- stress ---

func TestArchive_Stress_TotalSizeCapNeverExceeded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const cap_ = 50 * 1024 // 50 KiB
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileBytes: 4 * 1024, MaxTotalBytes: cap_})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	payload := make([]byte, 200)
	for i := 0; i < 2000; i++ {
		p := archivePacket(base.Add(time.Duration(i)*time.Millisecond), "x")
		p.Raw = payload
		if err := a.Write(p); err != nil {
			t.Fatalf("Write %d error: %v", i, err)
		}

		total, err := a.TotalBytes()
		if err != nil {
			t.Fatalf("TotalBytes error at i=%d: %v", i, err)
		}
		// The actively-written file is exempt from eviction, so total can
		// exceed the cap by at most one file's worth while it's still open.
		if total > cap_+8*1024 {
			t.Fatalf("TotalBytes() = %d at i=%d, want <= cap (%d) plus one in-flight file", total, i, cap_)
		}
	}
}

func TestArchive_Stress_CurrentFileNeverDeletedMidWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A tiny total cap combined with writes that never rotate (huge
	// MaxFileBytes) should never delete the one file being actively
	// written to, even though it alone already exceeds the cap.
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileBytes: 1 << 30, MaxTotalBytes: 1})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	for i := 0; i < 50; i++ {
		if err := a.Write(archivePacket(base.Add(time.Duration(i)*time.Millisecond), "x")); err != nil {
			t.Fatalf("Write %d error: %v", i, err)
		}
	}

	files, err := listArchiveFiles(dir)
	if err != nil {
		t.Fatalf("listArchiveFiles error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want exactly 1 (the in-flight file must survive)", len(files))
	}
}

// --- chaos ---

func TestArchive_Chaos_ConcurrentWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileBytes: 8 * 1024, MaxTotalBytes: 256 * 1024})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	base := time.Now()
	const writers = 8
	const perWriter = 500
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				p := archivePacket(base.Add(time.Duration(w*perWriter+i)*time.Microsecond), "c")
				if err := a.Write(p); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Write failed: %v", err)
	}
}

func TestArchive_Chaos_OtherFilesInDirIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Unrelated files (and directories) in the same archive directory
	// shouldn't confuse listing, rotation accounting, or reads.
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tcpview-garbage"+archiveSuffix+".bak"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tcpview-subdir"+archiveSuffix), 0o755); err != nil {
		t.Fatalf("Mkdir error: %v", err)
	}

	a, err := NewArchive(ArchiveOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	base := time.Now()
	if err := a.Write(archivePacket(base, "a")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	r := NewArchiveReader(dir)
	got, err := r.Range(base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("Range error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d packets, want 1 (unrelated files/dirs should be ignored)", len(got))
	}
}

func TestArchive_Chaos_RandomPacketsRoundTripWithoutPanicking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := NewArchive(ArchiveOptions{Dir: dir, MaxFileBytes: 2048, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewArchive error: %v", err)
	}
	defer a.Close()

	rng := rand.New(rand.NewSource(13))
	base := time.Now()
	for i := 0; i < 300; i++ {
		p := archivePacket(base.Add(time.Duration(rng.Intn(1_000_000))*time.Microsecond), "r")
		if rng.Intn(5) == 0 {
			p.Raw = nil // no raw bytes available, e.g. a synthetic/live-only packet
		}
		if rng.Intn(5) == 0 {
			p.SrcIP = p.SrcIP.WithZone("") // exercise an addr with no zone explicitly
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Write panicked on iteration %d: %v", i, rec)
				}
			}()
			if err := a.Write(p); err != nil {
				t.Fatalf("Write error at i=%d: %v", i, err)
			}
		}()
	}
}

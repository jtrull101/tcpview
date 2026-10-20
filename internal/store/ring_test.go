package store

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func pkt(t time.Time, summary string) Packet {
	return Packet{
		Time:    t,
		Iface:   "eth0",
		SrcIP:   netip.MustParseAddr("10.0.0.1"),
		DstIP:   netip.MustParseAddr("10.0.0.2"),
		SrcPort: 1234,
		DstPort: 443,
		Proto:   ProtoTCP,
		Length:  60,
		Summary: summary,
	}
}

// --- positive ---

func TestRing_AddAndSnapshot_Order(t *testing.T) {
	t.Parallel()
	r := NewRing(10, 0)
	base := time.Now()
	for i := 0; i < 5; i++ {
		evicted := r.Add(pkt(base.Add(time.Duration(i)*time.Second), fmt.Sprintf("p%d", i)))
		if len(evicted) != 0 {
			t.Fatalf("unexpected eviction below capacity: %v", evicted)
		}
	}
	snap := r.Snapshot()
	if len(snap) != 5 {
		t.Fatalf("expected 5 packets, got %d", len(snap))
	}
	for i, p := range snap {
		want := fmt.Sprintf("p%d", i)
		if p.Summary != want {
			t.Fatalf("snapshot out of order: index %d got %q want %q", i, p.Summary, want)
		}
	}
}

func TestRing_OldestNewest(t *testing.T) {
	t.Parallel()
	r := NewRing(3, 0)
	base := time.Now()
	r.Add(pkt(base, "a"))
	r.Add(pkt(base.Add(time.Second), "b"))
	r.Add(pkt(base.Add(2*time.Second), "c"))

	oldest, ok := r.Oldest()
	if !ok || oldest.Summary != "a" {
		t.Fatalf("oldest = %+v, ok=%v, want a", oldest, ok)
	}
	newest, ok := r.Newest()
	if !ok || newest.Summary != "c" {
		t.Fatalf("newest = %+v, ok=%v, want c", newest, ok)
	}
}

// --- negative ---

func TestRing_EmptyRing(t *testing.T) {
	t.Parallel()
	r := NewRing(5, 0)
	if r.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", r.Len())
	}
	if snap := r.Snapshot(); len(snap) != 0 {
		t.Fatalf("Snapshot() = %v, want empty", snap)
	}
	if _, ok := r.Oldest(); ok {
		t.Fatal("Oldest() on empty ring returned ok=true")
	}
	if _, ok := r.Newest(); ok {
		t.Fatal("Newest() on empty ring returned ok=true")
	}
}

func TestRing_NewRing_NonPositiveCapacityClampsToOne(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1, -100} {
		r := NewRing(n, 0)
		if r.Cap() != 1 {
			t.Fatalf("NewRing(%d,0).Cap() = %d, want 1", n, r.Cap())
		}
	}
}

func TestRing_Range_NoMatches(t *testing.T) {
	t.Parallel()
	r := NewRing(5, 0)
	base := time.Now()
	r.Add(pkt(base, "a"))
	r.Add(pkt(base.Add(time.Minute), "b"))

	// window entirely before anything inserted
	got := r.Range(base.Add(-time.Hour), base.Add(-time.Minute))
	if len(got) != 0 {
		t.Fatalf("Range outside window = %v, want empty", got)
	}

	// window entirely after everything inserted (exercises the upper-bound branch)
	got = r.Range(base.Add(time.Hour), base.Add(2*time.Hour))
	if len(got) != 0 {
		t.Fatalf("Range after window = %v, want empty", got)
	}
}

func TestRing_Range_InclusiveBounds(t *testing.T) {
	t.Parallel()
	r := NewRing(5, 0)
	base := time.Now()
	r.Add(pkt(base, "a"))
	r.Add(pkt(base.Add(time.Minute), "b"))
	r.Add(pkt(base.Add(2*time.Minute), "c"))

	got := r.Range(base, base.Add(time.Minute))
	if len(got) != 2 {
		t.Fatalf("Range(base, base+1m) = %d packets, want 2 (inclusive both ends)", len(got))
	}
}

// --- load ---

func TestRing_Load_SustainedInsertionStaysBounded(t *testing.T) {
	t.Parallel()
	const cap = 1000
	const total = 200_000
	r := NewRing(cap, 0)
	base := time.Now()

	evictedCount := 0
	for i := 0; i < total; i++ {
		evicted := r.Add(pkt(base.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("p%d", i)))
		evictedCount += len(evicted)
		if r.Len() > cap {
			t.Fatalf("ring exceeded capacity at i=%d: Len()=%d", i, r.Len())
		}
	}
	if r.Len() != cap {
		t.Fatalf("final Len() = %d, want %d", r.Len(), cap)
	}
	if evictedCount != total-cap {
		t.Fatalf("evictedCount = %d, want %d (conservation of packets)", evictedCount, total-cap)
	}
	newest, ok := r.Newest()
	if !ok || newest.Summary != fmt.Sprintf("p%d", total-1) {
		t.Fatalf("newest after sustained load = %+v, want p%d", newest, total-1)
	}
}

// --- stress ---

func TestRing_Stress_ExactCapacityNoEviction(t *testing.T) {
	t.Parallel()
	const cap = 50
	r := NewRing(cap, 0)
	base := time.Now()
	for i := 0; i < cap; i++ {
		if evicted := r.Add(pkt(base.Add(time.Duration(i)*time.Second), "x")); len(evicted) != 0 {
			t.Fatalf("eviction before ring full at i=%d: %v", i, evicted)
		}
	}
	if r.Len() != cap {
		t.Fatalf("Len() = %d, want %d", r.Len(), cap)
	}

	evicted := r.Add(pkt(base.Add(time.Duration(cap)*time.Second), "overflow"))
	if len(evicted) != 1 {
		t.Fatalf("expected exactly 1 eviction on first overflow, got %d", len(evicted))
	}
	if r.Len() != cap {
		t.Fatalf("Len() after overflow = %d, want %d (still bounded)", r.Len(), cap)
	}
}

func TestRing_Stress_MaxAgePrunesBacklogInOneAdd(t *testing.T) {
	t.Parallel()
	r := NewRing(1000, time.Second)
	base := time.Now()

	// Pack in 100 packets all within the same millisecond-ish window, well under capacity.
	for i := 0; i < 100; i++ {
		r.Add(pkt(base.Add(time.Duration(i)*time.Millisecond), "old"))
	}
	if r.Len() != 100 {
		t.Fatalf("Len() before prune = %d, want 100", r.Len())
	}

	// A single packet far enough in the future should prune the entire backlog
	// (all older than maxAge relative to it) in one Add call.
	evicted := r.Add(pkt(base.Add(time.Hour), "new"))
	if len(evicted) != 100 {
		t.Fatalf("expected 100 packets pruned by maxAge in one Add, got %d", len(evicted))
	}
	if r.Len() != 1 {
		t.Fatalf("Len() after prune = %d, want 1", r.Len())
	}
}

// --- chaos ---

func TestRing_Chaos_OutOfOrderTimestampsDoNotPanic(t *testing.T) {
	t.Parallel()
	r := NewRing(100, time.Minute)
	base := time.Now()

	offsets := []time.Duration{0, 5 * time.Second, -10 * time.Second, 3 * time.Hour, -time.Hour, 0, time.Millisecond}
	for i, off := range offsets {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Add panicked on out-of-order timestamp at i=%d: %v", i, rec)
				}
			}()
			r.Add(pkt(base.Add(off), fmt.Sprintf("o%d", i)))
		}()
	}
	// No crash and ring stays internally consistent (Len never exceeds cap).
	if r.Len() > r.Cap() {
		t.Fatalf("Len() = %d exceeds Cap() = %d after out-of-order chaos", r.Len(), r.Cap())
	}
}

func TestRing_Chaos_ConcurrentReadersAndWriters(t *testing.T) {
	t.Parallel()
	const cap = 500
	const writers = 8
	const perWriter = 2000
	r := NewRing(cap, 0)
	base := time.Now()

	var readersWG, writersWG sync.WaitGroup
	stop := make(chan struct{})

	// Concurrent readers hammering Snapshot/Len/Oldest/Newest while writers
	// run. A tiny sleep between iterations (rather than a pure busy-spin)
	// keeps this representative of real usage (the UI polls on a tick, it
	// never spins) and avoids the readers monopolizing CPU on a
	// resource-constrained runner - a pure spin loop here was observed to
	// make this test time out under -race on GitHub Actions' shared CI
	// runners (plenty fast on a many-core dev machine, but Snapshot()
	// allocates a fresh slice every call, and four unthrottled readers doing
	// that back-to-back under race instrumentation starved the writers of
	// CPU/memory bandwidth badly enough to blow the deadline).
	for i := 0; i < 4; i++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = r.Snapshot()
					_ = r.Len()
					_, _ = r.Oldest()
					_, _ = r.Newest()
					time.Sleep(50 * time.Microsecond)
				}
			}
		}()
	}

	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < perWriter; i++ {
				r.Add(pkt(base.Add(time.Duration(w*perWriter+i)*time.Microsecond), "c"))
			}
		}(w)
	}

	// Wait for writers only, then stop readers.
	writerDone := make(chan struct{})
	go func() {
		writersWG.Wait()
		close(writerDone)
	}()
	select {
	case <-writerDone:
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent chaos test timed out - possible deadlock")
	}
	close(stop)
	readersWG.Wait()

	if r.Len() > cap {
		t.Fatalf("Len() = %d exceeds cap %d after concurrent chaos", r.Len(), cap)
	}
}

package store

import (
	"sync"
	"time"
)

// Ring is a thread-safe, bounded in-memory buffer of the most recently
// observed packets. It is bounded by count (capacity) and, if maxAge is
// nonzero, additionally evicts anything older than that relative to the
// newest packet on every Add.
//
// Eviction is synchronous and returned to the caller rather than delivered
// via a callback: a callback invoked while holding the ring's lock risks
// deadlock or stalling every other Add/Snapshot call if it does anything
// slow (like a disk write), so instead the caller decides what to do with
// evicted packets (e.g. hand them to an archive writer) after Add returns.
type Ring struct {
	mu     sync.RWMutex
	buf    []Packet
	cap    int
	start  int
	count  int
	maxAge time.Duration
}

// NewRing creates a Ring holding at most maxLen packets (minimum 1). If
// maxAge is nonzero, packets older than maxAge relative to the most recently
// added packet are evicted even if the ring isn't full.
func NewRing(maxLen int, maxAge time.Duration) *Ring {
	if maxLen <= 0 {
		maxLen = 1
	}
	return &Ring{
		buf:    make([]Packet, maxLen),
		cap:    maxLen,
		maxAge: maxAge,
	}
}

// Add inserts p, evicting the oldest packet(s) if the ring is at capacity or
// maxAge is exceeded, and returns whatever was evicted (oldest first, may be
// empty, may be more than one when maxAge prunes a backlog).
func (r *Ring) Add(p Packet) []Packet {
	r.mu.Lock()
	defer r.mu.Unlock()

	var evicted []Packet

	if r.count == r.cap {
		evicted = append(evicted, r.evictOldestLocked())
	}

	idx := (r.start + r.count) % r.cap
	r.buf[idx] = p
	r.count++

	if r.maxAge > 0 {
		cutoff := p.Time.Add(-r.maxAge)
		for r.count > 0 && r.buf[r.start].Time.Before(cutoff) {
			evicted = append(evicted, r.evictOldestLocked())
		}
	}

	return evicted
}

// evictOldestLocked removes and returns the oldest packet. Caller must hold r.mu.
func (r *Ring) evictOldestLocked() Packet {
	old := r.buf[r.start]
	r.buf[r.start] = Packet{} // drop the reference so GC can reclaim it
	r.start = (r.start + 1) % r.cap
	r.count--
	return old
}

// Len returns the current number of packets held.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Cap returns the ring's fixed capacity.
func (r *Ring) Cap() int {
	return r.cap
}

// Snapshot returns a copy of all currently held packets, oldest first. Safe
// to use concurrently with Add; it never observes a torn/partial state.
func (r *Ring) Snapshot() []Packet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Packet, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.buf[(r.start+i)%r.cap]
	}
	return out
}

// Range returns held packets with Time in [from, to], oldest first.
func (r *Ring) Range(from, to time.Time) []Packet {
	all := r.Snapshot()
	out := make([]Packet, 0, len(all))
	for _, p := range all {
		if !p.Time.Before(from) && !p.Time.After(to) {
			out = append(out, p)
		}
	}
	return out
}

// Oldest returns the oldest held packet, if any.
func (r *Ring) Oldest() (Packet, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return Packet{}, false
	}
	return r.buf[r.start], true
}

// Newest returns the most recently added packet, if any.
func (r *Ring) Newest() (Packet, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return Packet{}, false
	}
	return r.buf[(r.start+r.count-1)%r.cap], true
}

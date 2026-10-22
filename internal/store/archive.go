package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ArchiveOptions configures a rotating, size-capped on-disk archive.
type ArchiveOptions struct {
	Dir string // directory archive files live in; created if missing

	MaxFileAge    time.Duration // rotate once the current file is older than this; 0 => default
	MaxFileBytes  int64         // rotate once the current file exceeds this size; 0 => default
	MaxTotalBytes int64         // hard cap on combined size of all archive files; oldest deleted first; 0 => default
}

func (o ArchiveOptions) withDefaults() ArchiveOptions {
	if o.MaxFileAge <= 0 {
		o.MaxFileAge = 5 * time.Minute
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = 1 << 30 // 1 GiB
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = 32 << 20 // 32 MiB
		// The currently-open file is exempt from the total-size cap (see
		// enforceTotalCapLocked) - it can grow up to MaxFileBytes before
		// rotation ever gives eviction a second file to work with. Found
		// via a real sustained-load test: a small configured cap (e.g. 20
		// MiB) combined with the 32 MiB *default* file size meant the
		// archive visibly sat at ~1.5x the configured cap for the entire
		// time it took to fill that first file. Only clamp the default,
		// not an explicitly-chosen MaxFileBytes (see
		// TestArchive_Stress_CurrentFileNeverDeletedMidWrite, which
		// deliberately sets a huge MaxFileBytes to test the exemption
		// itself and would break if this silently capped it too).
		if o.MaxFileBytes > o.MaxTotalBytes {
			o.MaxFileBytes = o.MaxTotalBytes
		}
	}
	return o
}

const (
	archivePrefix     = "tcpview-"
	archiveSuffix     = ".jsonl"
	archiveTimeLayout = "20060102T150405.000000000"
)

// Archive is a rotating, total-size-capped on-disk store for packets
// evicted from a Ring. Each archive file is a newline-delimited JSON
// stream of Packet values, named by the timestamp of its first packet so
// files sort chronologically by filename alone. Safe for concurrent use.
//
// Deliberately not real pcap format: a Ring eviction only carries
// tcpview's own decoded Packet (and, when available, the original frame in
// Packet.Raw) - encoding the fields we actually render during rewind
// directly, rather than re-deriving them from a pcap file later, sidesteps
// needing to track link-type per packet (a real concern once a user
// switches interfaces, or uses the "any" pseudo-device, mid-session).
type Archive struct {
	mu          sync.Mutex
	opts        ArchiveOptions
	cur         *os.File
	curBuf      *bufio.Writer
	curOpenedAt time.Time
	curBytes    int64
}

// NewArchive prepares (and creates, if needed) the archive directory. No
// file is opened until the first Write.
func NewArchive(opts ArchiveOptions) (*Archive, error) {
	opts = opts.withDefaults()
	if opts.Dir == "" {
		return nil, fmt.Errorf("store: archive directory required")
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create archive dir: %w", err)
	}
	return &Archive{opts: opts}, nil
}

// Write appends p to the current archive file, rotating first if needed
// (first write ever, current file too old, or too large), then enforces
// the total-size cap across the whole archive directory.
func (a *Archive) Write(p Packet) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cur == nil || time.Since(a.curOpenedAt) >= a.opts.MaxFileAge || a.curBytes >= a.opts.MaxFileBytes {
		if err := a.rotateLocked(p.Time); err != nil {
			return err
		}
	}

	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("store: marshal archived packet: %w", err)
	}
	n, err := a.curBuf.Write(data)
	if err == nil {
		err = a.curBuf.WriteByte('\n')
	}
	if err != nil {
		return fmt.Errorf("store: write archive: %w", err)
	}
	a.curBytes += int64(n) + 1

	if err := a.curBuf.Flush(); err != nil {
		return fmt.Errorf("store: flush archive: %w", err)
	}

	return a.enforceTotalCapLocked()
}

func (a *Archive) rotateLocked(startTime time.Time) error {
	if a.cur != nil {
		_ = a.curBuf.Flush()
		_ = a.cur.Close()
	}

	name := archivePrefix + startTime.UTC().Format(archiveTimeLayout) + archiveSuffix
	f, err := os.OpenFile(filepath.Join(a.opts.Dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("store: rotate archive: %w", err)
	}
	a.cur = f
	a.curBuf = bufio.NewWriter(f)
	a.curOpenedAt = time.Now()
	a.curBytes = 0
	return nil
}

// enforceTotalCapLocked deletes the oldest archive files (by filename,
// which sorts chronologically) until the combined on-disk size is at or
// under opts.MaxTotalBytes. The file currently being written is never
// deleted, even if that means briefly exceeding the cap - that's safer
// than truncating or corrupting an in-flight write. Caller must hold a.mu.
func (a *Archive) enforceTotalCapLocked() error {
	files, err := a.listFilesLocked()
	if err != nil {
		return err
	}

	var total int64
	for _, f := range files {
		total += f.size
	}

	curPath := ""
	if a.cur != nil {
		curPath = a.cur.Name()
	}

	for _, f := range files {
		if total <= a.opts.MaxTotalBytes {
			break
		}
		if f.path == curPath {
			continue
		}
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("store: evict archive file %s: %w", f.path, err)
		}
		total -= f.size
	}
	return nil
}

type archiveFileInfo struct {
	path string
	name string
	size int64
}

func (a *Archive) listFilesLocked() ([]archiveFileInfo, error) {
	return listArchiveFiles(a.opts.Dir)
}

func listArchiveFiles(dir string) ([]archiveFileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list archive dir: %w", err)
	}

	out := make([]archiveFileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), archivePrefix) || !strings.HasSuffix(e.Name(), archiveSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // file vanished between ReadDir and Info - fine to skip
		}
		out = append(out, archiveFileInfo{path: filepath.Join(dir, e.Name()), name: e.Name(), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// TotalBytes returns the combined size of all archive files currently on
// disk.
func (a *Archive) TotalBytes() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	files, err := a.listFilesLocked()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	return total, nil
}

// Close flushes and closes the current archive file, if any. Safe to call
// even if nothing has been written yet.
func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil {
		return nil
	}
	flushErr := a.curBuf.Flush()
	closeErr := a.cur.Close()
	a.cur = nil
	if flushErr != nil {
		return fmt.Errorf("store: flush archive on close: %w", flushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("store: close archive: %w", closeErr)
	}
	return nil
}

// ArchiveReader reads archived packets back in chronological order, for
// rewind queries once data has aged out of the in-memory Ring.
type ArchiveReader struct {
	dir string
}

func NewArchiveReader(dir string) *ArchiveReader {
	return &ArchiveReader{dir: dir}
}

// CorruptLinesError is returned by Range alongside the packets it could
// read when some archive lines didn't decode (a write cut off by a crash,
// disk corruption, a manual edit). Those lines are skipped rather than
// failing the whole query, so one bad line never hides the rest of the
// archive from rewind.
type CorruptLinesError struct {
	Lines int // how many lines were skipped
}

func (e *CorruptLinesError) Error() string {
	return fmt.Sprintf("store: skipped %d corrupt archive line(s)", e.Lines)
}

// Range returns archived packets with Time in [from, to], oldest first.
// Files are named by their first packet's timestamp and read in that
// order, so only files overlapping the window are read: a file is skipped
// when the next one already starts before `from` (everything in it is
// older still), and reading stops once a file starts after `to`. Without
// that, every rewind query re-decoded the whole archive from its oldest
// file - over a second per query on a ~80 MiB archive.
//
// If some lines were corrupt, Range still returns every packet it could
// decode, together with a *CorruptLinesError. Any other error means the
// query itself failed and the packets are nil.
func (r *ArchiveReader) Range(from, to time.Time) ([]Packet, error) {
	files, err := listArchiveFiles(r.dir)
	if err != nil {
		return nil, err
	}

	var out []Packet
	corrupt := 0
	for i, f := range files {
		start, ok := parseArchiveFileStart(f.name)
		if ok && start.After(to) {
			break
		}
		if i+1 < len(files) {
			if next, ok := parseArchiveFileStart(files[i+1].name); ok && next.Before(from) {
				continue
			}
		}
		pkts, skipped, err := readArchiveFile(f.path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // evicted by the writer between listing and reading
			}
			return nil, err
		}
		corrupt += skipped
		for _, p := range pkts {
			if !p.Time.Before(from) && !p.Time.After(to) {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	if corrupt > 0 {
		return out, &CorruptLinesError{Lines: corrupt}
	}
	return out, nil
}

func parseArchiveFileStart(name string) (time.Time, bool) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, archivePrefix), archiveSuffix)
	t, err := time.ParseInLocation(archiveTimeLayout, trimmed, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// readArchiveFile decodes every line of the archive file at path,
// skipping (and counting) lines that aren't a valid Packet.
func readArchiveFile(path string) (pkts []Packet, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("store: open archive file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // read-only handle; a close error here is inconsequential

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16<<20) // allow for large payload packets
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var p Packet
		if err := json.Unmarshal(line, &p); err != nil {
			skipped++
			continue
		}
		pkts = append(pkts, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: scan archive file %s: %w", path, err)
	}
	return pkts, skipped, nil
}

package store

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzReadArchiveFile feeds arbitrary file contents through the archive
// reader's line-by-line JSON decoder. A malformed line must produce an
// error, never a panic - archive files are read back long after being
// written, so any corruption (partial write, disk error, manual edit)
// needs to fail cleanly rather than crash the rewind path.
func FuzzReadArchiveFile(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("{}\n"))
	f.Add([]byte(`{"Time":"2026-01-01T00:00:00Z","Summary":"a"}` + "\n"))
	f.Add([]byte("not json\n"))
	f.Add([]byte(`{"Time":"2026-01-01T00:00:00Z"` + "\n")) // truncated mid-object
	f.Add([]byte("\x00\x01\x02\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "fuzz.jsonl")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("readArchiveFile panicked on %q: %v", data, rec)
			}
		}()
		_, _ = readArchiveFile(path)
	})
}

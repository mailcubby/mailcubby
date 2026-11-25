package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// SPEC 11: app.db and its -wal/-shm files are owner-only (0600).
func TestOpenDBFileModes(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}

	tests := []struct {
		name     string
		existing os.FileMode // 0 = no file before Open
	}{
		{name: "new database is created 0600"},
		{name: "existing 0644 database is tightened to 0600", existing: 0o644},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), DBFilename)

			if tc.existing != 0 {
				st, err := Open(ctx, dbPath)
				if err != nil {
					t.Fatalf("Open (seed): %v", err)
				}
				if err := st.Close(); err != nil {
					t.Fatalf("Close (seed): %v", err)
				}
				if err := os.Chmod(dbPath, tc.existing); err != nil {
					t.Fatalf("Chmod: %v", err)
				}
			}

			st, err := Open(ctx, dbPath)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			// Check while the store is open: Close may checkpoint and remove the WAL.
			for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
				fi, err := os.Stat(p)
				if err != nil {
					t.Fatalf("stat %s: %v", filepath.Base(p), err)
				}
				if got := fi.Mode().Perm(); got != 0o600 {
					t.Errorf("%s mode = %o, want 600", filepath.Base(p), got)
				}
			}
		})
	}
}

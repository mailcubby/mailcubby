package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func makeTestULID(t *testing.T, ts time.Time) ulid.ULID {
	t.Helper()
	id, err := ulid.New(ulid.Timestamp(ts.UTC()), rand.Reader)
	if err != nil {
		t.Fatalf("ulid.New: %v", err)
	}
	return id
}

func TestSpoolPlaceOpenRemove(t *testing.T) {
	t.Parallel()

	// FR-006: raw bytes round-trip identically via zstd streaming.
	st, err := NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	payload := bytes.Repeat([]byte("Subject: Test\r\n\r\nHello Mailcubby!\r\n"), 200)
	wantHash := sha256.Sum256(payload)
	wantHashHex := hex.EncodeToString(wantHash[:])

	sp, err := st.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = sp.Cleanup() }()

	if err := sp.CopyFrom(context.Background(), bytes.NewReader(payload)); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if err := sp.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	if sp.Size() != int64(len(payload)) {
		t.Fatalf("Size = %d, want %d", sp.Size(), len(payload))
	}
	if sp.SHA256() != wantHashHex {
		t.Fatalf("SHA256 = %s, want %s", sp.SHA256(), wantHashHex)
	}

	// Reopen finalized spool before placing.
	sr, err := sp.Open()
	if err != nil {
		t.Fatalf("sp.Open: %v", err)
	}
	gotSpoolBytes, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("ReadAll spool: %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("sr.Close: %v", err)
	}
	if !bytes.Equal(gotSpoolBytes, payload) {
		t.Fatalf("spool bytes mismatch")
	}

	// Place into two projects (FR-011 multi-target hard-link).
	ts := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)
	id1 := makeTestULID(t, ts)
	id2 := makeTestULID(t, ts)

	rel1, err := st.Place(sp, 1, id1)
	if err != nil {
		t.Fatalf("Place 1: %v", err)
	}
	rel2, err := st.Place(sp, 2, id2)
	if err != nil {
		t.Fatalf("Place 2: %v", err)
	}

	// Removing the spool must leave placed blobs intact.
	if err := sp.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	for _, rel := range []string{rel1, rel2} {
		rc, err := st.Open(rel)
		if err != nil {
			t.Fatalf("Open(%s): %v", rel, err)
		}
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll(%s): %v", rel, err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("Close(%s): %v", rel, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("blob %s content mismatch", rel)
		}
	}

	// Walk discovers both blobs.
	var seen []Entry
	if err := st.Walk(context.Background(), func(e Entry) error {
		seen = append(seen, e)
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("Walk found %d entries, want 2", len(seen))
	}

	// Remove one blob; second remove is idempotent (SPEC 11).
	if err := st.Remove(rel1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := st.Remove(rel1); err != nil {
		t.Fatalf("Remove missing should succeed, got: %v", err)
	}
	if _, err := st.Open(rel1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open removed blob: got %v, want os.ErrNotExist", err)
	}

	// RemoveProject removes project 2's directory.
	if err := st.RemoveProject(2); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if _, err := st.Open(rel2); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open after RemoveProject: got %v, want os.ErrNotExist", err)
	}
}

func TestCleanTmp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewStore(dir, 3)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	stale := filepath.Join(dir, "tmp", "stale.tmp")
	if err := os.WriteFile(stale, []byte("orphan"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := st.CleanTmp(); err != nil {
		t.Fatalf("CleanTmp: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale tmp file removed, stat err = %v", err)
	}
}

func TestListProjectDirsCleanTmpOlderThanAndTotalDiskBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewStore(dir, 3)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	ts := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	sp, err := st.Create(t.Context())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = sp.Cleanup() }()
	if err := sp.CopyFrom(t.Context(), bytes.NewReader([]byte("Subject: Test\r\n\r\nhello world\r\n"))); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if err := sp.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	if _, err := st.Place(sp, 7, makeTestULID(t, ts)); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if _, err := st.Place(sp, 12, makeTestULID(t, ts)); err != nil {
		t.Fatalf("Place: %v", err)
	}

	dirs, err := st.ListProjectDirs()
	if err != nil {
		t.Fatalf("ListProjectDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("ListProjectDirs = %v, want 2 dirs", dirs)
	}

	totalBytes, err := st.TotalDiskBytes(t.Context())
	if err != nil {
		t.Fatalf("TotalDiskBytes: %v", err)
	}
	if totalBytes <= 0 {
		t.Fatalf("TotalDiskBytes = %d, want > 0", totalBytes)
	}

	oldFile := filepath.Join(st.TmpDir(), "old.tmp")
	newFile := filepath.Join(st.TmpDir(), "new.tmp")
	if err := os.WriteFile(oldFile, []byte("a"), 0o600); err != nil {
		t.Fatalf("WriteFile(old): %v", err)
	}
	if err := os.WriteFile(newFile, []byte("b"), 0o600); err != nil {
		t.Fatalf("WriteFile(new): %v", err)
	}
	if err := os.Chtimes(oldFile, ts.Add(-2*time.Hour), ts.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Chtimes(old): %v", err)
	}
	if err := os.Chtimes(newFile, ts.Add(-10*time.Minute), ts.Add(-10*time.Minute)); err != nil {
		t.Fatalf("Chtimes(new): %v", err)
	}

	removed, err := st.CleanTmpOlderThan(ts.Add(-time.Hour))
	if err != nil {
		t.Fatalf("CleanTmpOlderThan: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old tmp removed, err = %v", err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("expected new tmp kept, err = %v", err)
	}
}

func TestValidateRelPath(t *testing.T) {
	t.Parallel()

	validID := makeTestULID(t, time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)).String()

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "valid", path: "42/2026/11/" + validID + ".eml.zst", wantErr: false},
		{name: "empty", path: "", wantErr: true},
		{name: "leading slash", path: "/42/2026/11/" + validID + ".eml.zst", wantErr: true},
		{name: "traversal", path: "../42/2026/11/" + validID + ".eml.zst", wantErr: true},
		{name: "backslash", path: `42\2026\11\` + validID + ".eml.zst", wantErr: true},
		{name: "zero project", path: "0/2026/11/" + validID + ".eml.zst", wantErr: true},
		{name: "negative project", path: "-1/2026/11/" + validID + ".eml.zst", wantErr: true},
		{name: "leading zero project", path: "01/2026/11/" + validID + ".eml.zst", wantErr: true},
		{name: "bad month", path: "1/2026/13/" + validID + ".eml.zst", wantErr: true},
		{name: "bad year", path: "1/26/11/" + validID + ".eml.zst", wantErr: true},
		{name: "wrong ext", path: "1/2026/11/" + validID + ".eml", wantErr: true},
		{name: "bad ulid", path: "1/2026/11/not-a-ulid.eml.zst", wantErr: true},
		{name: "extra segment", path: "1/2026/11/sub/" + validID + ".eml.zst", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ValidateRelPath(tc.path)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateRelPath(%q) = nil, want error", tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateRelPath(%q) = %v, want nil", tc.path, err)
			}
		})
	}
}

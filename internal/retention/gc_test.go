package retention_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/retention"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func placeOrphanBlob(t *testing.T, blobs *blob.Store, projectID int64, ts time.Time) string {
	t.Helper()
	sp, err := blobs.Create(t.Context())
	if err != nil {
		t.Fatalf("blobs.Create: %v", err)
	}
	defer func() { _ = sp.Cleanup() }()
	if err := sp.CopyFrom(t.Context(), bytes.NewReader([]byte("Subject: orphan\r\n\r\nbody\r\n"))); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if err := sp.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	id, err := ulid.New(ulid.Timestamp(ts.UTC()), rand.Reader)
	if err != nil {
		t.Fatalf("ulid.New: %v", err)
	}
	rel, err := blobs.Place(sp, projectID, id)
	if err != nil {
		t.Fatalf("blobs.Place: %v", err)
	}
	fullPath := filepath.Join(blobs.BlobsDir(), filepath.FromSlash(rel))
	if err := os.Chtimes(fullPath, ts, ts); err != nil {
		t.Fatalf("Chtimes(%s): %v", fullPath, err)
	}
	return rel
}

// FR-098: orphan blob older than 1 h is removed while younger orphan is kept (fake clock);
// deleted project blob dir and stale tmp files older than 1 h are also removed.
func TestOrphanGC(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}

	start := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)

	projKeep, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "keep", Name: "Keep", Color: "blue", Settings: store.DefaultProjectSettings(), Now: start,
	})
	if err != nil {
		t.Fatalf("CreateProject(keep): %v", err)
	}
	projDelete, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "deleted", Name: "Deleted", Color: "red", Settings: store.DefaultProjectSettings(), Now: start,
	})
	if err != nil {
		t.Fatalf("CreateProject(deleted): %v", err)
	}

	// Real DB-backed message whose blob is older than 1 h -> MUST stay.
	ing := ingest.New(st, blobs, clk, nil, nil)
	realRef := ingestSimple(t, ing, projKeep, "Real", "content")
	realFull := filepath.Join(blobs.BlobsDir(), filepath.FromSlash(realRef.BlobPath))
	oldTime := start.Add(-2 * time.Hour)
	if err := os.Chtimes(realFull, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(real): %v", err)
	}

	// Message in projDelete, then delete projDelete from DB (leaving blobs/<id>/ behind).
	delRef := ingestSimple(t, ing, projDelete, "Gone", "content")
	if err := st.DeleteProject(t.Context(), projDelete.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	// Two orphan blobs in projKeep: one 61m old (remove), one 30m old (keep).
	oldOrphan := placeOrphanBlob(t, blobs, projKeep.ID, start.Add(-61*time.Minute))
	youngOrphan := placeOrphanBlob(t, blobs, projKeep.ID, start.Add(-30*time.Minute))

	// Two tmp files: one 61m old (remove), one 30m old (keep).
	oldTmp := filepath.Join(blobs.TmpDir(), "old.tmp")
	youngTmp := filepath.Join(blobs.TmpDir(), "young.tmp")
	if err := os.WriteFile(oldTmp, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(oldTmp): %v", err)
	}
	if err := os.Chtimes(oldTmp, start.Add(-61*time.Minute), start.Add(-61*time.Minute)); err != nil {
		t.Fatalf("Chtimes(oldTmp): %v", err)
	}
	if err := os.WriteFile(youngTmp, []byte("young"), 0o600); err != nil {
		t.Fatalf("WriteFile(youngTmp): %v", err)
	}
	if err := os.Chtimes(youngTmp, start.Add(-30*time.Minute), start.Add(-30*time.Minute)); err != nil {
		t.Fatalf("Chtimes(youngTmp): %v", err)
	}

	gc := retention.NewOrphanGC(st, blobs, clk, nil)
	stats, err := gc.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce #1: %v", err)
	}
	if stats.RemovedProjectDirs != 1 {
		t.Fatalf("RemovedProjectDirs = %d, want 1", stats.RemovedProjectDirs)
	}
	if stats.RemovedOrphanBlobs != 1 {
		t.Fatalf("RemovedOrphanBlobs = %d, want 1", stats.RemovedOrphanBlobs)
	}
	if stats.RemovedTmpEntries != 1 {
		t.Fatalf("RemovedTmpEntries = %d, want 1", stats.RemovedTmpEntries)
	}

	// Deleted project blob gone.
	if _, err := blobs.Open(delRef.BlobPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deleted project blob removed, err = %v", err)
	}
	// Old orphan gone; young orphan and real message kept.
	if _, err := blobs.Open(oldOrphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old orphan removed, err = %v", err)
	}
	if rc, err := blobs.Open(youngOrphan); err != nil {
		t.Fatalf("expected young orphan kept, err = %v", err)
	} else {
		_ = rc.Close()
	}
	if rc, err := blobs.Open(realRef.BlobPath); err != nil {
		t.Fatalf("expected real message blob kept, err = %v", err)
	} else {
		_ = rc.Close()
	}
	// Old tmp gone; young tmp kept.
	if _, err := os.Stat(oldTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old tmp removed, err = %v", err)
	}
	if _, err := os.Stat(youngTmp); err != nil {
		t.Fatalf("expected young tmp kept, err = %v", err)
	}

	// Advance fake clock 35 minutes -> young orphan & young tmp are now 65m old (>1h).
	clk.Advance(35 * time.Minute)
	stats2, err := gc.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce #2: %v", err)
	}
	if stats2.RemovedOrphanBlobs != 1 {
		t.Fatalf("RunOnce #2 RemovedOrphanBlobs = %d, want 1", stats2.RemovedOrphanBlobs)
	}
	if stats2.RemovedTmpEntries != 1 {
		t.Fatalf("RunOnce #2 RemovedTmpEntries = %d, want 1", stats2.RemovedTmpEntries)
	}
	if _, err := blobs.Open(youngOrphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected aged young orphan removed, err = %v", err)
	}
	if _, err := os.Stat(youngTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected aged young tmp removed, err = %v", err)
	}
	if rc, err := blobs.Open(realRef.BlobPath); err != nil {
		t.Fatalf("expected real message blob still intact, err = %v", err)
	} else {
		_ = rc.Close()
	}
}

// FR-098: incremental vacuum reclaims pages when freelist exceeds 10% of total pages.
func TestOrphanGCIncrementalVacuum(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC))

	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "vac", Name: "Vac", Color: "slate", Settings: store.DefaultProjectSettings(), Now: clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Insert enough large rows to grow SQLite pages, then delete all of them.
	bigSnippet := strings.Repeat("vacuum-payload-", 200)
	if err := st.WithTx(t.Context(), func(tx *store.Tx) error {
		for i := range 200 {
			id := fmt.Sprintf("01TESTVACUUM%014d", i)
			if _, err := tx.InsertMessage(t.Context(), store.InsertMessageParams{
				ID: id, ProjectID: proj.ID, ReceivedAt: clk.Now(),
				Subject: bigSnippet, Snippet: bigSnippet,
				EnvelopeFrom: "a@b.test", EnvelopeTo: []string{"c@d.test"},
				SizeBytes: 3000, BlobPath: fmt.Sprintf("%d/2026/11/%s.eml.zst", proj.ID, id),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed vacuum rows: %v", err)
	}

	if _, _, _, err := st.DeleteAllMessages(t.Context(), proj.ID, clk.Now()); err != nil {
		t.Fatalf("DeleteAllMessages: %v", err)
	}

	gc := retention.NewOrphanGC(st, blobs, clk, nil)
	stats, err := gc.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Vacuum.ReclaimedPages == 0 {
		t.Fatalf("expected ReclaimedPages > 0 after deleting 200 large rows, got %+v", stats.Vacuum)
	}
	if stats.Vacuum.PageCount > 0 && stats.Vacuum.FreelistCount*10 > stats.Vacuum.PageCount {
		t.Fatalf("expected freelist <= 10%% of pages after vacuum, got %+v", stats.Vacuum)
	}
}

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestMessagesAndAttachmentsCRUDAndIsolation(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)

	p1, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "proj-a",
		Name:     "Project A",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	p2, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "proj-b",
		Name:     "Project B",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}

	msgID := "01JXXXXXXXXXXXXXXXXXXXXXX1"
	msgIDHdr := "<msg-1@example.test>"
	cid := "logo@example.test"

	var inserted store.Message
	err = st.WithTx(ctx, func(tx *store.Tx) error {
		var txErr error
		inserted, txErr = tx.InsertMessage(ctx, store.InsertMessageParams{
			ID:              msgID,
			ProjectID:       p1.ID,
			ReceivedAt:      now,
			Subject:         "Hello",
			FromName:        "Alice",
			FromAddr:        "alice@example.test",
			To:              []store.Address{{Name: "Bob", Address: "bob@example.test"}},
			EnvelopeFrom:    "alice@example.test",
			EnvelopeTo:      []string{"bob@example.test"},
			MessageIDHeader: &msgIDHdr,
			DateHeader:      &now,
			Snippet:         "Hi Bob",
			HasHTML:         true,
			HasText:         true,
			AttachmentCount: 1,
			SizeBytes:       512,
			BlobPath:        "1/2026/11/" + msgID + ".eml.zst",
			ParseWarnings:   []string{"minor warning"},
		})
		if txErr != nil {
			return txErr
		}
		_, txErr = tx.InsertAttachment(ctx, store.InsertAttachmentParams{
			MessagePK:   inserted.PK,
			ProjectID:   p1.ID,
			PartPath:    "1.2",
			Filename:    "logo.png",
			ContentType: "image/png",
			SizeBytes:   128,
			ContentID:   &cid,
			Disposition: "inline",
			SHA256:      "deadbeef",
		})
		return txErr
	})
	if err != nil {
		t.Fatalf("WithTx insert: %v", err)
	}

	got, err := st.GetMessageByID(ctx, p1.ID, msgID)
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	if got.Subject != "Hello" || got.FromAddr != "alice@example.test" || len(got.To) != 1 || len(got.ParseWarnings) != 1 {
		t.Fatalf("unexpected message: %+v", got)
	}

	// Tenant isolation: looking up p1's message under p2 must fail with ErrNotFound.
	if _, err := st.GetMessageByID(ctx, p2.ID, msgID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant GetMessageByID: got %v, want ErrNotFound", err)
	}

	atts, err := st.ListAttachmentsByMessagePK(ctx, p1.ID, inserted.PK)
	if err != nil {
		t.Fatalf("ListAttachmentsByMessagePK: %v", err)
	}
	if len(atts) != 1 || atts[0].Filename != "logo.png" || atts[0].ContentID == nil || *atts[0].ContentID != cid {
		t.Fatalf("unexpected attachments: %+v", atts)
	}

	// Cross-tenant attachment lookup must return empty slice.
	crossAtts, err := st.ListAttachmentsByMessagePK(ctx, p2.ID, inserted.PK)
	if err != nil {
		t.Fatalf("cross-tenant ListAttachmentsByMessagePK: %v", err)
	}
	if len(crossAtts) != 0 {
		t.Fatalf("expected 0 cross-tenant attachments, got %d", len(crossAtts))
	}

	gotAtt, err := st.GetAttachmentByID(ctx, p1.ID, inserted.PK, atts[0].ID)
	if err != nil {
		t.Fatalf("GetAttachmentByID: %v", err)
	}
	if gotAtt.ID != atts[0].ID || gotAtt.Filename != "logo.png" {
		t.Fatalf("unexpected GetAttachmentByID result: %+v", gotAtt)
	}
	if _, err := st.GetAttachmentByID(ctx, p2.ID, inserted.PK, atts[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant GetAttachmentByID: got %v, want ErrNotFound", err)
	}
	if _, err := st.GetAttachmentByID(ctx, p1.ID, inserted.PK+999, atts[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong-message GetAttachmentByID: got %v, want ErrNotFound", err)
	}

	if err := st.MigrationsApplied(ctx); err != nil {
		t.Fatalf("MigrationsApplied: %v", err)
	}
}

func TestListMessagesPageAndSparkline(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 14, 30, 0, 0, time.UTC)

	p, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "spark",
		Name:     "Spark",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	entries := []struct {
		id         string
		receivedAt time.Time
		subject    string
	}{
		{"01JAAAAAAAAAAAAAAAAAAAAAA1", now.Add(-25 * time.Hour), "older than 24h"},
		{"01JAAAAAAAAAAAAAAAAAAAAAA2", now.Add(-23 * time.Hour), "oldest bucket"},
		{"01JAAAAAAAAAAAAAAAAAAAAAA3", now.Add(-2 * time.Hour), "two hours ago"},
		{"01JAAAAAAAAAAAAAAAAAAAAAA4", now.Add(-15 * time.Minute), "current hour 1"},
		{"01JAAAAAAAAAAAAAAAAAAAAAA5", now.Add(-5 * time.Minute), "current hour 2"},
	}

	for _, e := range entries {
		_, err := st.InsertMessage(ctx, store.InsertMessageParams{
			ID:           e.id,
			ProjectID:    p.ID,
			ReceivedAt:   e.receivedAt,
			Subject:      e.subject,
			EnvelopeFrom: "a@example.test",
			EnvelopeTo:   []string{"b@example.test"},
			SizeBytes:    100,
			BlobPath:     "1/2026/11/" + e.id + ".eml.zst",
		})
		if err != nil {
			t.Fatalf("InsertMessage %s: %v", e.id, err)
		}
	}

	// Cursor pagination (newest first).
	page1, err := st.ListMessagesPage(ctx, p.ID, "", 2)
	if err != nil {
		t.Fatalf("ListMessagesPage page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != entries[4].id || page1[1].ID != entries[3].id {
		t.Fatalf("unexpected page1: %+v", page1)
	}

	page2, err := st.ListMessagesPage(ctx, p.ID, page1[1].ID, 2)
	if err != nil {
		t.Fatalf("ListMessagesPage page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != entries[2].id || page2[1].ID != entries[1].id {
		t.Fatalf("unexpected page2: %+v", page2)
	}

	page3, err := st.ListMessagesPage(ctx, p.ID, page2[1].ID, 2)
	if err != nil {
		t.Fatalf("ListMessagesPage page3: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != entries[0].id {
		t.Fatalf("unexpected page3: %+v", page3)
	}

	// Sparkline (FR-035): 24 buckets, oldest first, ending at current hour.
	spark, err := st.ProjectSparkline(ctx, p.ID, now)
	if err != nil {
		t.Fatalf("ProjectSparkline: %v", err)
	}
	if spark[0] != 1 {
		t.Errorf("spark[0] (23h ago) = %d, want 1", spark[0])
	}
	if spark[21] != 1 {
		t.Errorf("spark[21] (2h ago) = %d, want 1", spark[21])
	}
	if spark[23] != 2 {
		t.Errorf("spark[23] (current hour) = %d, want 2", spark[23])
	}
	var sum int64
	for _, v := range spark {
		sum += v
	}
	if sum != 4 {
		t.Errorf("sparkline sum = %d, want 4 (message 25h ago must be excluded)", sum)
	}
}

func TestEnsureProjectConcurrent(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)

	const workers = 8
	var wg sync.WaitGroup
	ids := make([]int64, workers)
	createdCount := make([]bool, workers)
	errs := make([]error, workers)

	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p, created, err := st.EnsureProject(ctx, store.CreateProjectParams{
				Slug:     "shop-staging",
				Name:     "Shop Staging",
				Color:    "slate",
				Settings: store.DefaultProjectSettings(),
				Now:      now,
			})
			ids[idx] = p.ID
			createdCount[idx] = created
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	createdTotal := 0
	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("worker %d failed: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("worker %d got project id %d, want %d", i, ids[i], ids[0])
		}
		if createdCount[i] {
			createdTotal++
		}
	}
	if createdTotal != 1 {
		t.Fatalf("expected exactly 1 creator, got %d", createdTotal)
	}
}

func TestMessageMutationsAndTenantIsolation(t *testing.T) {
	t.Parallel()

	// FR-034, FR-075, FR-076, FR-078: mutations maintain counters and never cross project boundaries.
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)

	pA, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "alpha",
		Name:     "Alpha",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject alpha: %v", err)
	}
	pB, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "beta",
		Name:     "Beta",
		Color:    "green",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject beta: %v", err)
	}

	insertMsg := func(projectID int64, id string, size int64, at time.Time) {
		t.Helper()
		err := st.WithTx(ctx, func(tx *store.Tx) error {
			if _, txErr := tx.InsertMessage(ctx, store.InsertMessageParams{
				ID:           id,
				ProjectID:    projectID,
				ReceivedAt:   at,
				Subject:      "Subj " + id,
				EnvelopeFrom: "a@example.test",
				EnvelopeTo:   []string{"b@example.test"},
				SizeBytes:    size,
				BlobPath:     fmt.Sprintf("%d/2026/11/%s.eml.zst", projectID, id),
			}); txErr != nil {
				return txErr
			}
			return tx.UpdateProjectCounters(ctx, projectID, store.ProjectCounterDelta{
				MessageDelta:  1,
				UnreadDelta:   1,
				SizeDelta:     size,
				LastMessageAt: &at,
				Now:           at,
			})
		})
		if err != nil {
			t.Fatalf("insertMsg %s: %v", id, err)
		}
	}

	idA1 := "01JBBBBBBBBBBBBBBBBBBBBBB1"
	idA2 := "01JBBBBBBBBBBBBBBBBBBBBBB2"
	idA3 := "01JBBBBBBBBBBBBBBBBBBBBBB3"
	idB1 := "01JBBBBBBBBBBBBBBBBBBBBBB4"

	insertMsg(pA.ID, idA1, 100, now)
	insertMsg(pA.ID, idA2, 250, now.Add(time.Minute))
	insertMsg(pA.ID, idA3, 400, now.Add(2*time.Minute))
	insertMsg(pB.ID, idB1, 999, now.Add(3*time.Minute))

	// GetLatestMessageAfterID & GetLatestMessageSinceTime (FR-078).
	latest, err := st.GetLatestMessageAfterID(ctx, pA.ID, idA1)
	if err != nil || latest.ID != idA3 {
		t.Fatalf("GetLatestMessageAfterID = (%q, %v), want %q", latest.ID, err, idA3)
	}
	if _, err := st.GetLatestMessageAfterID(ctx, pA.ID, idA3); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetLatestMessageAfterID after newest = %v, want ErrNotFound", err)
	}
	latestSince, err := st.GetLatestMessageSinceTime(ctx, pA.ID, now.Add(90*time.Second))
	if err != nil || latestSince.ID != idA3 {
		t.Fatalf("GetLatestMessageSinceTime = (%q, %v), want %q", latestSince.ID, err, idA3)
	}

	// Single SetMessageSeen: idempotent and isolated.
	msg, proj, changed, err := st.SetMessageSeen(ctx, pA.ID, idA1, true, now.Add(4*time.Minute))
	if err != nil || !changed || !msg.Seen || proj.UnreadCount != 2 {
		t.Fatalf("SetMessageSeen true = (seen=%v, changed=%v, unread=%d, err=%v)", msg.Seen, changed, proj.UnreadCount, err)
	}
	// Repeating seen=true is a no-op.
	_, proj, changed, err = st.SetMessageSeen(ctx, pA.ID, idA1, true, now.Add(4*time.Minute))
	if err != nil || changed || proj.UnreadCount != 2 {
		t.Fatalf("SetMessageSeen idempotent = (changed=%v, unread=%d, err=%v)", changed, proj.UnreadCount, err)
	}
	// Cross-tenant SetMessageSeen must return ErrNotFound.
	if _, _, _, err := st.SetMessageSeen(ctx, pA.ID, idB1, true, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant SetMessageSeen = %v, want ErrNotFound", err)
	}

	// BulkSetMessagesSeen with duplicate + cross-tenant IDs.
	changedIDs, proj, err := st.BulkSetMessagesSeen(ctx, pA.ID, []string{idA1, idA2, idA2, idB1, "01JNONEXISTENT000000000000"}, true, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("BulkSetMessagesSeen: %v", err)
	}
	if len(changedIDs) != 1 || changedIDs[0] != idA2 || proj.UnreadCount != 1 {
		t.Fatalf("BulkSetMessagesSeen = (changed=%v, unread=%d), want [%s] and unread=1", changedIDs, proj.UnreadCount, idA2)
	}

	// DeleteMessage removes newest message and recalculates last_message_at.
	del, proj, err := st.DeleteMessage(ctx, pA.ID, idA3, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if del.ID != idA3 || !del.WasUnread || proj.MessageCount != 2 || proj.UnreadCount != 0 || proj.SizeBytes != 350 {
		t.Fatalf("DeleteMessage = (del=%+v, proj=%+v)", del, proj)
	}
	if proj.LastMessageAt == nil || !proj.LastMessageAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("last_message_at after deleting newest = %v, want %v", proj.LastMessageAt, now.Add(time.Minute))
	}

	// Cross-tenant DeleteMessage must fail and leave pB intact.
	if _, _, err := st.DeleteMessage(ctx, pA.ID, idB1, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant DeleteMessage = %v, want ErrNotFound", err)
	}

	// BulkDeleteMessages with cross-tenant ID must only delete pA's message.
	deleted, proj, err := st.BulkDeleteMessages(ctx, pA.ID, []string{idA1, idA1, idB1}, now.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("BulkDeleteMessages: %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != idA1 || proj.MessageCount != 1 || proj.SizeBytes != 250 {
		t.Fatalf("BulkDeleteMessages = (deleted=%+v, proj=%+v)", deleted, proj)
	}

	// DeleteAllMessages clears remaining pA messages and resets counters + last_message_at.
	affected, blobs, proj, err := st.DeleteAllMessages(ctx, pA.ID, now.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("DeleteAllMessages: %v", err)
	}
	if affected != 1 || len(blobs) != 1 || proj.MessageCount != 0 || proj.UnreadCount != 0 || proj.SizeBytes != 0 || proj.LastMessageAt != nil {
		t.Fatalf("DeleteAllMessages = (affected=%d, blobs=%v, proj=%+v)", affected, blobs, proj)
	}

	// Project B must remain completely untouched.
	gotB, err := st.GetProjectByID(ctx, pB.ID)
	if err != nil {
		t.Fatalf("GetProjectByID B: %v", err)
	}
	if gotB.MessageCount != 1 || gotB.UnreadCount != 1 || gotB.SizeBytes != 999 {
		t.Fatalf("project B counters mutated: %+v", gotB)
	}
}

func TestProjectCountersPropertyFR034(t *testing.T) {
	t.Parallel()

	// FR-034: property test performing random inserts, deletes, bulk ops, and seen toggles,
	// asserting maintained counters always equal SQL COUNT(*)/SUM() queries.
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), store.DBFilename)
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verifyDB: %v", err)
	}
	t.Cleanup(func() { _ = verifyDB.Close() })

	baseTime := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	projects := make([]store.Project, 2)
	for i := range projects {
		p, err := st.CreateProject(ctx, store.CreateProjectParams{
			Slug:     fmt.Sprintf("prop-%d", i),
			Name:     fmt.Sprintf("Prop %d", i),
			Color:    "slate",
			Settings: store.DefaultProjectSettings(),
			Now:      baseTime,
		})
		if err != nil {
			t.Fatalf("CreateProject %d: %v", i, err)
		}
		projects[i] = p
	}

	verifyCounters := func(step int) {
		t.Helper()
		for _, p := range projects {
			got, err := st.GetProjectByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("step %d: GetProjectByID %d: %v", step, p.ID, err)
			}
			var (
				wantCount  int64
				wantUnread int64
				wantSize   int64
				wantLast   sql.NullInt64
			)
			row := verifyDB.QueryRowContext(ctx, `
				SELECT
					COUNT(*),
					COALESCE(SUM(CASE WHEN seen = 0 THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(size_bytes), 0),
					MAX(received_at)
				FROM messages
				WHERE project_id = ?`, p.ID)
			if err := row.Scan(&wantCount, &wantUnread, &wantSize, &wantLast); err != nil {
				t.Fatalf("step %d: scan aggregate for project %d: %v", step, p.ID, err)
			}
			if got.MessageCount != wantCount || got.UnreadCount != wantUnread || got.SizeBytes != wantSize {
				t.Fatalf("step %d project %d mismatch: got (count=%d unread=%d size=%d), want (count=%d unread=%d size=%d)",
					step, p.ID, got.MessageCount, got.UnreadCount, got.SizeBytes, wantCount, wantUnread, wantSize)
			}
			if wantLast.Valid {
				if got.LastMessageAt == nil || got.LastMessageAt.UTC().UnixMilli() != wantLast.Int64 {
					t.Fatalf("step %d project %d last_message_at = %v, want %d", step, p.ID, got.LastMessageAt, wantLast.Int64)
				}
			} else if got.LastMessageAt != nil {
				t.Fatalf("step %d project %d last_message_at = %v, want nil", step, p.ID, got.LastMessageAt)
			}
		}
	}

	//nolint:gosec // Deterministic PRNG for reproducible FR-034 property test.
	rng := rand.New(rand.NewPCG(34034, 998877))
	liveIDs := make([][]string, len(projects))
	seq := 0

	for step := range 250 {
		pIdx := rng.IntN(len(projects))
		pID := projects[pIdx].ID
		opTime := baseTime.Add(time.Duration(step) * time.Second)
		op := rng.IntN(100)

		switch {
		case op < 42 || len(liveIDs[pIdx]) == 0:
			// Insert message with random size.
			seq++
			id := fmt.Sprintf("01JPROP%019d", seq)
			size := int64(100 + rng.IntN(50000))
			if err := st.WithTx(ctx, func(tx *store.Tx) error {
				if _, txErr := tx.InsertMessage(ctx, store.InsertMessageParams{
					ID:           id,
					ProjectID:    pID,
					ReceivedAt:   opTime,
					Subject:      "prop",
					EnvelopeFrom: "a@example.test",
					EnvelopeTo:   []string{"b@example.test"},
					SizeBytes:    size,
					BlobPath:     fmt.Sprintf("%d/2026/11/%s.eml.zst", pID, id),
				}); txErr != nil {
					return txErr
				}
				return tx.UpdateProjectCounters(ctx, pID, store.ProjectCounterDelta{
					MessageDelta:  1,
					UnreadDelta:   1,
					SizeDelta:     size,
					LastMessageAt: &opTime,
					Now:           opTime,
				})
			}); err != nil {
				t.Fatalf("step %d: insert: %v", step, err)
			}
			liveIDs[pIdx] = append(liveIDs[pIdx], id)

		case op < 62:
			// Single seen toggle.
			id := liveIDs[pIdx][rng.IntN(len(liveIDs[pIdx]))]
			seen := rng.IntN(2) == 0
			if _, _, _, err := st.SetMessageSeen(ctx, pID, id, seen, opTime); err != nil {
				t.Fatalf("step %d: SetMessageSeen: %v", step, err)
			}

		case op < 76:
			// Single delete.
			idx := rng.IntN(len(liveIDs[pIdx]))
			id := liveIDs[pIdx][idx]
			if _, _, err := st.DeleteMessage(ctx, pID, id, opTime); err != nil {
				t.Fatalf("step %d: DeleteMessage: %v", step, err)
			}
			liveIDs[pIdx] = append(liveIDs[pIdx][:idx], liveIDs[pIdx][idx+1:]...)

		case op < 88:
			// Bulk seen/unseen including duplicates and a cross-project ID.
			var batch []string
			n := 1 + rng.IntN(min(4, len(liveIDs[pIdx])))
			for range n {
				batch = append(batch, liveIDs[pIdx][rng.IntN(len(liveIDs[pIdx]))])
			}
			otherIdx := 1 - pIdx
			if len(liveIDs[otherIdx]) > 0 {
				batch = append(batch, liveIDs[otherIdx][0])
			}
			batch = append(batch, "01JMISSING0000000000000000")
			seen := rng.IntN(2) == 0
			if _, _, err := st.BulkSetMessagesSeen(ctx, pID, batch, seen, opTime); err != nil {
				t.Fatalf("step %d: BulkSetMessagesSeen: %v", step, err)
			}

		case op < 97:
			// Bulk delete including duplicates and cross-project ID.
			var batch []string
			toRemove := make(map[string]struct{})
			n := 1 + rng.IntN(min(3, len(liveIDs[pIdx])))
			for range n {
				id := liveIDs[pIdx][rng.IntN(len(liveIDs[pIdx]))]
				batch = append(batch, id)
				toRemove[id] = struct{}{}
			}
			otherIdx := 1 - pIdx
			if len(liveIDs[otherIdx]) > 0 {
				batch = append(batch, liveIDs[otherIdx][0])
			}
			if _, _, err := st.BulkDeleteMessages(ctx, pID, batch, opTime); err != nil {
				t.Fatalf("step %d: BulkDeleteMessages: %v", step, err)
			}
			kept := liveIDs[pIdx][:0]
			for _, id := range liveIDs[pIdx] {
				if _, removed := toRemove[id]; !removed {
					kept = append(kept, id)
				}
			}
			liveIDs[pIdx] = kept

		default:
			// Delete all in this project.
			if _, _, _, err := st.DeleteAllMessages(ctx, pID, opTime); err != nil {
				t.Fatalf("step %d: DeleteAllMessages: %v", step, err)
			}
			liveIDs[pIdx] = nil
		}

		verifyCounters(step)
	}
}

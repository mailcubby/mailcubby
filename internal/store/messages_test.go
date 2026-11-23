package store_test

import (
	"context"
	"errors"
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

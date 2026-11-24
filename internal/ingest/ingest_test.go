package ingest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

type failingStore struct {
	err error
}

func (f failingStore) WithTx(context.Context, func(tx *store.Tx) error) error {
	return f.err
}

func TestIngestEndToEndAndMultiTargetAndEvent(t *testing.T) {
	t.Parallel()

	// FR-006, FR-008, FR-011, FR-012, FR-086
	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	fixedTime := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	bus := events.NewBus()
	defer bus.Close()

	ctx := context.Background()
	p1, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "shop",
		Name:     "Shop",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      fixedTime,
	})
	if err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	p2, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "default",
		Name:     "default",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      fixedTime,
	})
	if err != nil {
		t.Fatalf("CreateProject p2: %v", err)
	}

	sub, unsub := bus.Subscribe(events.AllFilter())
	defer unsub()

	rawMsg := []byte("From: Orders <orders@shop.test>\r\n" +
		"To: Buyer <buyer@example.test>\r\n" +
		"Subject: Receipt\r\n" +
		"X-Tags: invoice, Staging\r\n" +
		"\r\n" +
		"Thanks for your purchase!\r\n")
	wantHash := sha256.Sum256(rawMsg)
	wantHashHex := hex.EncodeToString(wantHash[:])

	ing := ingest.New(st, blobs, clk, bus, nil)
	env := ingest.Envelope{
		MailFrom: "orders@shop.test",
		RcptTo:   []string{"buyer@example.test", "secret-bcc@example.test"},
		RemoteIP: "127.0.0.1",
		HELO:     "app.local",
	}
	// Duplicate p1 target to verify deduplication (FR-011) and plus-tag merging.
	targets := []ingest.Target{
		{ProjectID: p1.ID, ProjectSlug: p1.Slug, PlusTags: []string{"urgent"}, PlusAddressTags: true},
		{ProjectID: p1.ID, ProjectSlug: p1.Slug, PlusTags: []string{"urgent", "vip"}, PlusAddressTags: true},
		{ProjectID: p2.ID, ProjectSlug: p2.Slug, PlusAddressTags: false, PlusTags: []string{"ignored"}},
	}

	refs, err := ing.Ingest(ctx, env, targets, bytes.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2 (deduplicated)", len(refs))
	}

	// Verify p1 tags include X-Tags + plus tags.
	wantP1Tags := []string{"invoice", "Staging", "urgent", "vip"}
	if len(refs[0].Tags) != len(wantP1Tags) {
		t.Fatalf("p1 tags = %v, want %v", refs[0].Tags, wantP1Tags)
	}
	for i, w := range wantP1Tags {
		if refs[0].Tags[i] != w {
			t.Fatalf("p1 tags[%d] = %q, want %q", i, refs[0].Tags[i], w)
		}
	}
	// Verify p2 ignores plus tags when PlusAddressTags is false.
	if len(refs[1].Tags) != 2 || refs[1].Tags[0] != "invoice" || refs[1].Tags[1] != "Staging" {
		t.Fatalf("p2 tags = %v, want [invoice Staging]", refs[1].Tags)
	}

	// Verify stored raw bytes match SHA-256 identically (FR-006) and Bcc computed.
	for _, ref := range refs {
		if ref.SHA256 != wantHashHex {
			t.Fatalf("ref SHA256 = %s, want %s", ref.SHA256, wantHashHex)
		}
		rc, err := blobs.Open(ref.BlobPath)
		if err != nil {
			t.Fatalf("blobs.Open: %v", err)
		}
		gotBytes, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("ReadAll blob: %v", err)
		}
		if !bytes.Equal(gotBytes, rawMsg) {
			t.Fatalf("stored blob bytes differ from input")
		}

		msg, err := st.GetMessageByID(ctx, ref.ProjectID, ref.ID.String())
		if err != nil {
			t.Fatalf("GetMessageByID: %v", err)
		}
		if len(msg.Bcc) != 1 || msg.Bcc[0].Address != "secret-bcc@example.test" {
			t.Fatalf("Bcc = %+v, want [secret-bcc@example.test]", msg.Bcc)
		}
	}

	// Verify project counters updated.
	updatedP1, err := st.GetProjectByID(ctx, p1.ID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if updatedP1.MessageCount != 1 || updatedP1.UnreadCount != 1 || updatedP1.SizeBytes != int64(len(rawMsg)) {
		t.Fatalf("unexpected p1 counters: %+v", updatedP1)
	}

	// Verify two message.created events published.
	for i := range 2 {
		select {
		case ev := <-sub.Events():
			if ev.Type != events.TypeMessageCreated {
				t.Fatalf("event %d type = %q, want message.created", i, ev.Type)
			}
		default:
			t.Fatalf("expected event %d on bus", i)
		}
	}
}

func TestIngestDBFailureDeletesBlob(t *testing.T) {
	t.Parallel()

	// FR-010: if DB transaction fails, placed blob is deleted and no event is published.
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	bus := events.NewBus()
	defer bus.Close()
	sub, unsub := bus.Subscribe(events.AllFilter())
	defer unsub()

	dbErr := errors.New("simulated sqlite failure")
	ing := ingest.New(failingStore{err: dbErr}, blobs, clock.NewFake(time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)), bus, nil)

	_, err = ing.Ingest(
		context.Background(),
		ingest.Envelope{MailFrom: "a@example.test", RcptTo: []string{"b@example.test"}},
		[]ingest.Target{{ProjectID: 1, ProjectSlug: "default"}},
		bytes.NewReader([]byte("Subject: Fail\r\n\r\nBody\r\n")),
	)
	if !errors.Is(err, dbErr) {
		t.Fatalf("Ingest error = %v, want wrapped %v", err, dbErr)
	}

	// Ensure zero blobs remain on disk.
	var remaining int
	if err := blobs.Walk(context.Background(), func(blob.Entry) error {
		remaining++
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected 0 blobs after DB failure, found %d", remaining)
	}

	// Ensure no event was published.
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event published after DB failure: %+v", ev)
	default:
	}
}

func TestSendTestSampleEmail(t *testing.T) {
	t.Parallel()

	// FR-077: built-in sample message has HTML, text, inline PNG, and PDF attachment.
	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	now := time.Date(2026, 11, 23, 15, 30, 0, 0, time.UTC)
	clk := clock.NewFake(now)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "default",
		Name:     "default",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, nil, nil)
	ref, err := ing.SendTest(ctx, proj.ID, proj.Slug)
	if err != nil {
		t.Fatalf("SendTest: %v", err)
	}

	msg, err := st.GetMessageByID(ctx, proj.ID, ref.ID.String())
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	if !msg.HasHTML || !msg.HasText || msg.TextGenerated {
		t.Fatalf("expected HasHTML=true HasText=true TextGenerated=false, got %+v", msg)
	}
	if msg.DateHeader == nil || !msg.DateHeader.Equal(now) {
		t.Fatalf("DateHeader = %v, want %v", msg.DateHeader, now)
	}
	if msg.MessageIDHeader == nil || *msg.MessageIDHeader == "" {
		t.Fatalf("expected generated Message-ID header")
	}

	atts, err := st.ListAttachmentsByMessagePK(ctx, proj.ID, msg.PK)
	if err != nil {
		t.Fatalf("ListAttachmentsByMessagePK: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("expected 2 attachments (inline PNG + PDF), got %d: %+v", len(atts), atts)
	}

	var hasPNG, hasPDF bool
	for _, a := range atts {
		if a.ContentType == "image/png" && a.Disposition == "inline" && a.ContentID != nil && *a.ContentID == "logo@mailcubby.test" {
			hasPNG = true
		}
		if a.ContentType == "application/pdf" && a.Disposition == "attachment" && a.Filename == "sample.pdf" {
			hasPDF = true
		}
	}
	if !hasPNG || !hasPDF {
		t.Fatalf("expected inline PNG and PDF attachment, got %+v", atts)
	}
}

func TestIngestAutoTagCapAndParseWarning(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tags-cap", Name: "Tags Cap", Color: "slate", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	rawTags := make([]string, 75)
	for i := range rawTags {
		rawTags[i] = fmt.Sprintf("tag-%03d", i)
	}
	rawMsg := "From: a@example.test\r\nTo: b@example.test\r\nSubject: Many Tags\r\nX-Tags: " +
		strings.Join(rawTags, ", ") + "\r\n\r\nBody\r\n"

	ing := ingest.New(st, blobs, clock.NewFake(now), nil, nil)
	refs, err := ing.Ingest(
		t.Context(),
		ingest.Envelope{MailFrom: "a@example.test", RcptTo: []string{"b@example.test"}},
		[]ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug, PlusTags: []string{"extra-plus"}, PlusAddressTags: true}},
		strings.NewReader(rawMsg),
	)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(refs[0].Tags) != ingest.MaxAutoTagsPerMessage {
		t.Fatalf("len(tags) = %d, want %d", len(refs[0].Tags), ingest.MaxAutoTagsPerMessage)
	}
	msg, err := st.GetMessageByID(t.Context(), proj.ID, refs[0].ID.String())
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	foundWarn := false
	for _, w := range msg.ParseWarnings {
		if strings.Contains(w, "auto-tags truncated") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected auto-tags truncation parse warning, got %v", msg.ParseWarnings)
	}
}

func TestIngestMultiProjectRollbackLeavesNoPartialCommitOrBlobs(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	p1, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "p1", Name: "P1", Color: "slate", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	bus := events.NewBus()
	defer bus.Close()
	sub, unsub := bus.Subscribe(events.AllFilter())
	defer unsub()

	ing := ingest.New(st, blobs, clock.NewFake(now), bus, nil)
	// Second target references nonexistent project ID 999999 -> FK violation mid-tx.
	_, err = ing.Ingest(
		t.Context(),
		ingest.Envelope{MailFrom: "a@example.test", RcptTo: []string{"p1@inbox.test", "missing@inbox.test"}},
		[]ingest.Target{
			{ProjectID: p1.ID, ProjectSlug: p1.Slug},
			{ProjectID: 999999, ProjectSlug: "missing"},
		},
		strings.NewReader("Subject: Multi Fail\r\n\r\nBody\r\n"),
	)
	if err == nil {
		t.Fatalf("expected error for nonexistent second project, got nil")
	}

	// First project must have 0 messages and counters unchanged.
	msgs, err := st.ListMessages(t.Context(), p1.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages in p1 after multi-project rollback, got %d", len(msgs))
	}
	p1After, err := st.GetProjectByID(t.Context(), p1.ID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if p1After.MessageCount != 0 {
		t.Fatalf("p1.MessageCount = %d, want 0", p1After.MessageCount)
	}

	// Zero blobs left on disk.
	var remaining int
	if err := blobs.Walk(t.Context(), func(blob.Entry) error {
		remaining++
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected 0 blobs after multi-project rollback, found %d", remaining)
	}

	// Zero events published.
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event published after rollback: %+v", ev)
	default:
	}
}

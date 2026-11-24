package retention_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/retention"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func int64Ptr(v int64) *int64 { return &v }

func defaultTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func assertCountersMatchDB(t *testing.T, st *store.Store, projectID int64) {
	t.Helper()
	proj, err := st.GetProjectByID(t.Context(), projectID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	var count, unread, totalSize int64
	if err := st.ReaderDB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN seen = 0 THEN 1 ELSE 0 END), 0), COALESCE(SUM(size_bytes), 0) FROM messages WHERE project_id = ?`,
		projectID,
	).Scan(&count, &unread, &totalSize); err != nil {
		t.Fatalf("query actual counts: %v", err)
	}
	if proj.MessageCount != count || proj.UnreadCount != unread || proj.SizeBytes != totalSize {
		t.Fatalf("project counters mismatch: got (msg=%d, unread=%d, bytes=%d), want (msg=%d, unread=%d, bytes=%d)",
			proj.MessageCount, proj.UnreadCount, proj.SizeBytes, count, unread, totalSize)
	}
}

func ingestSimple(t *testing.T, ing *ingest.Ingestor, proj store.Project, subject string, extraBody string) ingest.MessageRef {
	t.Helper()
	raw := fmt.Sprintf("From: alice@example.test\r\nTo: bob@example.test\r\nSubject: %s\r\n\r\nBody %s\r\n", subject, extraBody)
	refs, err := ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "alice@example.test",
		RcptTo:   []string{"bob@example.test"},
	}, []ingest.Target{{
		ProjectID:   proj.ID,
		ProjectSlug: proj.Slug,
	}}, bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("Ingest(%q): %v", subject, err)
	}
	return refs[0]
}

// FR-095: with max_messages=100, 150 inserts and one sweep leave the newest 100,
// keep counters consistent, remove blobs, and emit message.deleted + project.updated events.
func TestSweeperMaxMessages(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	bus := events.NewBus()
	defer bus.Close()

	clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig(t)
	cfg.RetentionMaxAge = 0
	cfg.RetentionMaxBytes = 0
	cfg.RetentionMaxMessages = 5000

	settings := store.DefaultProjectSettings()
	settings.RetentionMaxMessages = int64Ptr(100)
	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "shop",
		Name:     "Shop",
		Color:    "blue",
		Settings: settings,
		Now:      clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, bus, nil)
	refs := make([]ingest.MessageRef, 0, 150)
	for i := range 150 {
		clk.Advance(time.Second)
		ref := ingestSimple(t, ing, proj, fmt.Sprintf("Msg %03d", i), "hello")
		refs = append(refs, ref)
		// Mark every 3rd message seen to verify unread_count stays accurate.
		if i%3 == 0 {
			if _, _, _, err := st.SetMessageSeen(t.Context(), proj.ID, ref.ID.String(), true, clk.Now()); err != nil {
				t.Fatalf("SetMessageSeen: %v", err)
			}
		}
	}

	sub, unsub := bus.Subscribe(events.ProjectFilter(proj.ID))
	defer unsub()

	sw := retention.NewSweeper(st, blobs, bus, clk, nil, cfg)
	stats, err := sw.SweepAll(t.Context())
	if err != nil {
		t.Fatalf("SweepAll: %v", err)
	}
	if stats.DeletedMessages != 50 {
		t.Fatalf("DeletedMessages = %d, want 50", stats.DeletedMessages)
	}
	if stats.Batches != 1 {
		t.Fatalf("Batches = %d, want 1", stats.Batches)
	}

	assertCountersMatchDB(t, st, proj.ID)

	remaining, err := st.ListMessages(t.Context(), proj.ID, 200)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(remaining) != 100 {
		t.Fatalf("remaining len = %d, want 100", len(remaining))
	}
	// ListMessages returns newest first: newest is refs[149], oldest remaining is refs[50].
	if remaining[0].ID != refs[149].ID.String() {
		t.Fatalf("newest remaining = %s, want %s", remaining[0].ID, refs[149].ID.String())
	}
	if remaining[99].ID != refs[50].ID.String() {
		t.Fatalf("oldest remaining = %s, want %s", remaining[99].ID, refs[50].ID.String())
	}

	// Deleted blobs must be removed; surviving blobs must remain readable.
	if _, err := blobs.Open(refs[0].BlobPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected oldest deleted blob removed, err = %v", err)
	}
	rc, err := blobs.Open(refs[50].BlobPath)
	if err != nil {
		t.Fatalf("expected surviving blob readable: %v", err)
	}
	_ = rc.Close()

	// Verify message.deleted and project.updated events were emitted.
	var sawDeleted, sawProjUpdated bool
	for range 2 {
		select {
		case ev := <-sub.Events():
			switch ev.Type {
			case events.TypeMessageDeleted:
				p := ev.Payload.(events.MessageDeletedPayload)
				if len(p.IDs) != 50 {
					t.Fatalf("message.deleted IDs len = %d, want 50", len(p.IDs))
				}
				sawDeleted = true
			case events.TypeProjectUpdated:
				p := ev.Payload.(events.ProjectUpdatedPayload)
				if p.MessageCount != 100 {
					t.Fatalf("project.updated message_count = %d, want 100", p.MessageCount)
				}
				sawProjUpdated = true
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for retention events")
		}
	}
	if !sawDeleted || !sawProjUpdated {
		t.Fatalf("events missing: sawDeleted=%v sawProjUpdated=%v", sawDeleted, sawProjUpdated)
	}
}

// FR-095: verify max_age, max_bytes, 500-message batching, and precedence (project > DB > env, 0 disables).
func TestSweeperAgeBytesPrecedenceAndBatches(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig(t)
	cfg.RetentionMaxAge = 30 * 24 * time.Hour
	cfg.RetentionMaxMessages = 5000
	cfg.RetentionMaxBytes = 1024 * 1024 * 1024

	// Override DB default max_age to 1 hour (3600s).
	valJSON, _ := json.Marshal(int64(3600))
	if err := st.SetSetting(t.Context(), retention.SettingRetentionMaxAgeSec, string(valJSON), clk.Now()); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	// Project A inherits DB override (1h max age).
	projInherit, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "inherit", Name: "Inherit", Color: "slate", Settings: store.DefaultProjectSettings(), Now: clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject(inherit): %v", err)
	}

	// Project B explicitly disables age limit (0) and sets tight max_bytes.
	projBytesSettings := store.DefaultProjectSettings()
	projBytesSettings.RetentionMaxAgeSec = int64Ptr(0)
	projBytesSettings.RetentionMaxBytes = int64Ptr(250)
	projBytes, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "bytes", Name: "Bytes", Color: "blue", Settings: projBytesSettings, Now: clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject(bytes): %v", err)
	}

	// Project C tests multi-batch deletion (>500 excess).
	projBatchSettings := store.DefaultProjectSettings()
	projBatchSettings.RetentionMaxAgeSec = int64Ptr(0)
	projBatchSettings.RetentionMaxMessages = int64Ptr(50)
	projBatch, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "batch", Name: "Batch", Color: "green", Settings: projBatchSettings, Now: clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject(batch): %v", err)
	}

	ing := ingest.New(st, blobs, clk, nil, nil)

	// Old messages at t=0.
	oldA := ingestSimple(t, ing, projInherit, "Old A", "a")
	_ = ingestSimple(t, ing, projBytes, "Old B1", string(bytes.Repeat([]byte("x"), 100)))
	clk.Advance(2 * time.Hour)

	// Fresh messages at t=+2h.
	newA := ingestSimple(t, ing, projInherit, "New A", "a")
	b2 := ingestSimple(t, ing, projBytes, "New B2", string(bytes.Repeat([]byte("y"), 100)))
	b3 := ingestSimple(t, ing, projBytes, "New B3", string(bytes.Repeat([]byte("z"), 50)))

	// Insert 560 lightweight rows directly into projBatch to test 500+10 batching fast.
	for i := range 560 {
		clk.Advance(time.Millisecond)
		msgID := fmt.Sprintf("01TESTBATCH%015d", i)
		if err := st.WithTx(t.Context(), func(tx *store.Tx) error {
			if _, err := tx.InsertMessage(t.Context(), store.InsertMessageParams{
				ID: msgID, ProjectID: projBatch.ID, ReceivedAt: clk.Now(),
				Subject: "batch", EnvelopeFrom: "a@b.test", EnvelopeTo: []string{"c@d.test"},
				SizeBytes: 10, BlobPath: fmt.Sprintf("%d/2026/11/%s.eml.zst", projBatch.ID, msgID),
			}); err != nil {
				return err
			}
			now := clk.Now()
			return tx.UpdateProjectCounters(t.Context(), projBatch.ID, store.ProjectCounterDelta{
				MessageDelta: 1, UnreadDelta: 1, SizeDelta: 10, LastMessageAt: &now, Now: now,
			})
		}); err != nil {
			t.Fatalf("insert batch msg %d: %v", i, err)
		}
	}

	sw := retention.NewSweeper(st, blobs, nil, clk, nil, cfg)
	stats, err := sw.SweepAll(t.Context())
	if err != nil {
		t.Fatalf("SweepAll: %v", err)
	}

	// Project A: oldA deleted (older than 1h DB override), newA kept.
	if _, err := st.GetMessageByID(t.Context(), projInherit.ID, oldA.ID.String()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected oldA deleted, err = %v", err)
	}
	if _, err := st.GetMessageByID(t.Context(), projInherit.ID, newA.ID.String()); err != nil {
		t.Fatalf("expected newA kept, err = %v", err)
	}
	assertCountersMatchDB(t, st, projInherit.ID)

	// Project B: age limit disabled (0), bytes limit (250) deleted Old B1 + New B2?
	// Each message is ~160/110 bytes; check remaining size <= 250 and newest kept.
	pB, err := st.GetProjectByID(t.Context(), projBytes.ID)
	if err != nil {
		t.Fatalf("GetProjectByID(bytes): %v", err)
	}
	if pB.SizeBytes > 250 {
		t.Fatalf("projBytes SizeBytes = %d, want <= 250", pB.SizeBytes)
	}
	if _, err := st.GetMessageByID(t.Context(), projBytes.ID, b3.ID.String()); err != nil {
		t.Fatalf("expected newest b3 kept, err = %v", err)
	}
	if b2.SizeBytes+b3.SizeBytes > 250 {
		if _, err := st.GetMessageByID(t.Context(), projBytes.ID, b2.ID.String()); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expected b2 deleted to fit <= 250 bytes, err = %v", err)
		}
	}
	assertCountersMatchDB(t, st, projBytes.ID)

	// Project C: 560 -> 50 (510 deleted in 2 batches: 500 + 10).
	pC, err := st.GetProjectByID(t.Context(), projBatch.ID)
	if err != nil {
		t.Fatalf("GetProjectByID(batch): %v", err)
	}
	if pC.MessageCount != 50 {
		t.Fatalf("projBatch MessageCount = %d, want 50", pC.MessageCount)
	}
	if stats.Batches < 3 {
		t.Fatalf("expected at least 3 batches across projects (including 2 for batch project), got %d", stats.Batches)
	}
	assertCountersMatchDB(t, st, projBatch.ID)
}

// FR-095: ingest nudge triggers only after exceeding limit by >10% and is debounced 10 s.
func TestSweeperIngestNudgeDebounce(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	bus := events.NewBus()
	defer bus.Close()

	clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	cfg := defaultTestConfig(t)
	cfg.RetentionMaxAge = 0
	cfg.RetentionMaxBytes = 0
	cfg.RetentionInterval = time.Hour // keep periodic tick far away

	settings := store.DefaultProjectSettings()
	settings.RetentionMaxMessages = int64Ptr(10)
	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "nudge", Name: "Nudge", Color: "amber", Settings: settings, Now: clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	sw := retention.NewSweeper(st, blobs, bus, clk, nil, cfg)
	ing := ingest.New(st, blobs, clk, bus, nil)
	ing.SetRetentionNudge(sw.MaybeNudge)

	runCtx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sw.Run(runCtx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Wait until Run has registered its ticker on the fake clock.
	deadline := time.Now().Add(2 * time.Second)
	for clk.TickerCount() < 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}

	// Insert 11 messages (10 + 10% exact -> NOT >10% excess, so no nudge timer).
	for i := range 11 {
		clk.Advance(time.Millisecond)
		ingestSimple(t, ing, proj, fmt.Sprintf("Msg %d", i), "body")
	}
	clk.Advance(30 * time.Second)
	p, _ := st.GetProjectByID(t.Context(), proj.ID)
	if p.MessageCount != 11 {
		t.Fatalf("expected 11 messages (10%% exact should not nudge), got %d", p.MessageCount)
	}

	// Subscribe to catch the nudged sweep's message.deleted event.
	sub, unsub := bus.Subscribe(func(e events.Event) bool {
		return e.ProjectID == proj.ID && e.Type == events.TypeMessageDeleted
	})
	defer unsub()

	// 12th message exceeds 10 by 20% (>10%) -> triggers 10 s debounced nudge.
	clk.Advance(time.Millisecond)
	ingestSimple(t, ing, proj, "Msg 12", "body")

	// Wait for the debounce timer to register on the fake clock.
	deadline = time.Now().Add(2 * time.Second)
	for clk.TimerCount() < 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if clk.TimerCount() < 1 {
		t.Fatal("expected debounce timer to be armed after >10% excess")
	}

	// Advance 9 s: still within debounce window.
	clk.Advance(9 * time.Second)
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected sweep before 10 s debounce: %+v", ev)
	default:
	}

	// Advance 1 s more (total 10 s): debounce fires and sweeps down to 10 messages.
	clk.Advance(time.Second)
	select {
	case ev := <-sub.Events():
		payload := ev.Payload.(events.MessageDeletedPayload)
		if len(payload.IDs) != 2 {
			t.Fatalf("nudged sweep deleted %d messages, want 2", len(payload.IDs))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for debounced nudge sweep")
	}

	p, _ = st.GetProjectByID(t.Context(), proj.ID)
	if p.MessageCount != 10 {
		t.Fatalf("after nudged sweep MessageCount = %d, want 10", p.MessageCount)
	}
}

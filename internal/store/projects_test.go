package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestProjectCRUDAndCounters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	maxAge := int64(86400)
	maxMsgs := int64(250)
	maxBytes := int64(10 * 1024 * 1024)

	created, err := st.CreateProject(ctx, CreateProjectParams{
		Slug:  "shop-staging",
		Name:  "Shop Staging",
		Color: "blue",
		Settings: ProjectSettings{
			RetentionMaxAgeSec:      &maxAge,
			RetentionMaxMessages:    &maxMsgs,
			RetentionMaxBytes:       &maxBytes,
			RecipientRoutingEnabled: true,
			PlusAddressTags:         true,
			ViewerHideTechnical:     false,
			MembersCanRevealSMTP:    true,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.ID <= 0 || created.Slug != "shop-staging" || created.Name != "Shop Staging" {
		t.Fatalf("unexpected created project: %+v", created)
	}
	if created.Settings.RetentionMaxAgeSec == nil || *created.Settings.RetentionMaxAgeSec != maxAge {
		t.Fatalf("RetentionMaxAgeSec = %v, want %d", created.Settings.RetentionMaxAgeSec, maxAge)
	}
	if !created.Settings.RecipientRoutingEnabled || created.Settings.ViewerHideTechnical || !created.Settings.MembersCanRevealSMTP {
		t.Fatalf("unexpected settings: %+v", created.Settings)
	}

	// Duplicate slug must return ErrConflict.
	_, err = st.CreateProject(ctx, CreateProjectParams{
		Slug:     "shop-staging",
		Name:     "Duplicate",
		Color:    "slate",
		Settings: DefaultProjectSettings(),
		Now:      now,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrConflict", err)
	}

	// Get by ID and slug.
	byID, err := st.GetProjectByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	bySlug, err := st.GetProjectBySlug(ctx, "shop-staging")
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	if byID.ID != bySlug.ID || bySlug.Name != "Shop Staging" {
		t.Fatalf("GetProject mismatch: byID=%+v bySlug=%+v", byID, bySlug)
	}

	// Missing project returns ErrNotFound.
	if _, err := st.GetProjectBySlug(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProjectBySlug(missing) err = %v, want ErrNotFound", err)
	}

	// Create a second project and verify ListProjects ordering.
	second, err := st.CreateProject(ctx, CreateProjectParams{
		Slug:     "alpha",
		Name:     "Alpha",
		Color:    "green",
		Settings: DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject second: %v", err)
	}

	list, err := st.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != created.ID {
		t.Fatalf("ListProjects = %+v, want [Alpha, Shop Staging]", list)
	}

	// Update project (rename slug, reset nullable retention overrides).
	updatedAt := now.Add(time.Minute)
	updated, err := st.UpdateProject(ctx, UpdateProjectParams{
		ID:       created.ID,
		Slug:     "shop-prod-preview",
		Name:     "Shop Preview",
		Color:    "amber",
		Settings: DefaultProjectSettings(),
		Now:      updatedAt,
	})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if updated.Slug != "shop-prod-preview" || updated.Name != "Shop Preview" || updated.Settings.RetentionMaxAgeSec != nil {
		t.Fatalf("unexpected updated project: %+v", updated)
	}

	// Counter updates (FR-034).
	t1 := now.Add(2 * time.Minute)
	t2 := now.Add(5 * time.Minute)
	tEarlier := now.Add(3 * time.Minute)

	if err := st.UpdateProjectCounters(ctx, created.ID, ProjectCounterDelta{
		MessageDelta:  2,
		UnreadDelta:   2,
		SizeDelta:     4096,
		LastMessageAt: &t2,
		Now:           t2,
	}); err != nil {
		t.Fatalf("UpdateProjectCounters 1: %v", err)
	}

	// An older LastMessageAt should not move last_message_at backwards.
	if err := st.UpdateProjectCounters(ctx, created.ID, ProjectCounterDelta{
		MessageDelta:  1,
		UnreadDelta:   0,
		SizeDelta:     1024,
		LastMessageAt: &tEarlier,
		Now:           t2.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpdateProjectCounters 2: %v", err)
	}

	// Decrement on delete / mark read (LastMessageAt nil preserves existing timestamp).
	if err := st.UpdateProjectCounters(ctx, created.ID, ProjectCounterDelta{
		MessageDelta: -1,
		UnreadDelta:  -1,
		SizeDelta:    -512,
		Now:          t1,
	}); err != nil {
		t.Fatalf("UpdateProjectCounters 3: %v", err)
	}

	got, err := st.GetProjectByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProjectByID after counters: %v", err)
	}
	if got.MessageCount != 2 || got.UnreadCount != 1 || got.SizeBytes != 4608 {
		t.Fatalf("counters = (msg=%d unread=%d bytes=%d), want (2, 1, 4608)", got.MessageCount, got.UnreadCount, got.SizeBytes)
	}
	if got.LastMessageAt == nil || !got.LastMessageAt.Equal(t2) {
		t.Fatalf("LastMessageAt = %v, want %v", got.LastMessageAt, t2)
	}

	// Counter update on nonexistent project returns ErrNotFound.
	if err := st.UpdateProjectCounters(ctx, 999999, ProjectCounterDelta{MessageDelta: 1, Now: now}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateProjectCounters(missing) err = %v, want ErrNotFound", err)
	}

	// Delete project.
	if err := st.DeleteProject(ctx, created.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if err := st.DeleteProject(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteProject second err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetProjectByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProjectByID after delete err = %v, want ErrNotFound", err)
	}
}

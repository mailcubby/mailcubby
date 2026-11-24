package store_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestValidateTagName(t *testing.T) {
	t.Parallel()

	valid := []string{"invoice", "Staging", "v1.2_beta-3", "ação", "日本語", strings.Repeat("a", 40)}
	for _, name := range valid {
		if err := store.ValidateTagName(name); err != nil {
			t.Errorf("ValidateTagName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{"", "has space", "a/b", "tag!", "a,b", strings.Repeat("a", 41)}
	for _, name := range invalid {
		if err := store.ValidateTagName(name); err == nil {
			t.Errorf("ValidateTagName(%q) expected error, got nil", name)
		}
	}
}

func TestTagsCRUDAndTenantIsolation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := testutil.NewStore(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	projA, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "alpha",
		Name:     "Alpha",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject alpha: %v", err)
	}
	projB, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "beta",
		Name:     "Beta",
		Color:    "green",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject beta: %v", err)
	}

	// Create tag in project A.
	tagA, err := st.CreateTag(ctx, projA.ID, "Invoice", "amber", now)
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if tagA.Name != "Invoice" || tagA.Color != "amber" || tagA.MessageCount != 0 {
		t.Fatalf("unexpected tagA: %+v", tagA)
	}

	// Case-insensitive duplicate in same project returns ErrConflict.
	if _, err := st.CreateTag(ctx, projA.ID, "invoice", "blue", now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate tag err = %v, want ErrConflict", err)
	}

	// Same tag name in project B succeeds (isolated per project).
	tagB, err := st.CreateTag(ctx, projB.ID, "invoice", "red", now)
	if err != nil {
		t.Fatalf("CreateTag in projB: %v", err)
	}

	// Cross-tenant Get/Update/Delete must return ErrNotFound.
	if _, err := st.GetTagByID(ctx, projA.ID, tagB.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant GetTagByID err = %v, want ErrNotFound", err)
	}
	if _, err := st.DeleteTag(ctx, projA.ID, tagB.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant DeleteTag err = %v, want ErrNotFound", err)
	}

	// Insert a message in projA and patch its tags.
	msgA, err := st.InsertMessage(ctx, store.InsertMessageParams{
		ID:           "01JB0000000000000000000001",
		ProjectID:    projA.ID,
		ReceivedAt:   now,
		Subject:      "Hello",
		EnvelopeFrom: "a@b.test",
		EnvelopeTo:   []string{"c@d.test"},
		SizeBytes:    100,
		BlobPath:     "1/2026/11/01JB0000000000000000000001.eml.zst",
	})
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Patch with existing tag (different case) + auto-created tag.
	wantTags := []string{"invoice", "urgent"}
	patchRes, err := st.PatchMessage(ctx, projA.ID, msgA.ID, nil, &wantTags, now)
	if err != nil {
		t.Fatalf("PatchMessage: %v", err)
	}
	if !patchRes.TagsChanged {
		t.Fatalf("expected TagsChanged=true")
	}
	// Canonical casing of "Invoice" preserved.
	if !slices.Equal(patchRes.Tags, []string{"Invoice", "urgent"}) {
		t.Fatalf("patched tags = %v, want [Invoice urgent]", patchRes.Tags)
	}

	// ListTags shows message_count=1 for both tags.
	tags, err := st.ListTags(ctx, projA.ID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 2 || tags[0].MessageCount != 1 || tags[1].MessageCount != 1 {
		t.Fatalf("unexpected tags list: %+v", tags)
	}

	// Rename tagA -> affected message update returned.
	renamed := "Billing"
	updatedTag, msgUpdates, err := st.UpdateTag(ctx, projA.ID, tagA.ID, &renamed, nil)
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	if updatedTag.Name != "Billing" || updatedTag.MessageCount != 1 {
		t.Fatalf("unexpected updatedTag: %+v", updatedTag)
	}
	if len(msgUpdates) != 1 || !slices.Equal(msgUpdates[0].Tags, []string{"Billing", "urgent"}) {
		t.Fatalf("unexpected rename msgUpdates: %+v", msgUpdates)
	}

	// Bulk remove and add tags.
	bulkRm, err := st.BulkUpdateMessageTags(ctx, projA.ID, []string{msgA.ID}, false, []string{"urgent", "nonexistent"}, now)
	if err != nil {
		t.Fatalf("BulkUpdateMessageTags remove: %v", err)
	}
	if len(bulkRm) != 1 || !slices.Equal(bulkRm[0].Tags, []string{"Billing"}) {
		t.Fatalf("unexpected bulk remove updates: %+v", bulkRm)
	}

	// Delete tag removes it from the message via cascade.
	delUpdates, err := st.DeleteTag(ctx, projA.ID, tagA.ID)
	if err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if len(delUpdates) != 1 || len(delUpdates[0].Tags) != 0 {
		t.Fatalf("unexpected delete tag updates: %+v", delUpdates)
	}
}

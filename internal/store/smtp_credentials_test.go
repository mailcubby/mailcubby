package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSMTPCredentialsCRUDAndIsolation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st, err := Open(ctx, filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	projA, err := st.CreateProject(ctx, CreateProjectParams{
		Slug: "shop", Name: "Shop", Color: "blue", Settings: DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject(A): %v", err)
	}
	projB, err := st.CreateProject(ctx, CreateProjectParams{
		Slug: "blog", Name: "Blog", Color: "green", Settings: DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject(B): %v", err)
	}

	cred, err := st.CreateSMTPCredential(ctx, CreateSMTPCredentialParams{
		ProjectID:   projA.ID,
		Label:       "staging",
		Username:    "shop-abcd1234",
		PasswordEnc: []byte{0x01, 0x02, 0x03},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}
	if cred.Label != "staging" || cred.Username != "shop-abcd1234" || cred.DisabledAt != nil || cred.LastUsedAt != nil {
		t.Fatalf("unexpected credential: %+v", cred)
	}

	// Duplicate username conflicts globally.
	if _, err := st.CreateSMTPCredential(ctx, CreateSMTPCredentialParams{
		ProjectID:   projB.ID,
		Label:       "other",
		Username:    "shop-abcd1234",
		PasswordEnc: []byte{0x01, 0x09},
		Now:         now,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username err = %v, want ErrConflict", err)
	}

	// Tenant isolation: Project B cannot get, update, rotate, or delete Project A's credential.
	if _, err := st.GetSMTPCredentialByID(ctx, projB.ID, cred.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project Get err = %v, want ErrNotFound", err)
	}
	if _, err := st.UpdateSMTPCredential(ctx, projB.ID, cred.ID, "hijack", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project Update err = %v, want ErrNotFound", err)
	}
	if _, err := st.UpdateSMTPCredentialPassword(ctx, projB.ID, cred.ID, []byte{0x09}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project Rotate err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteSMTPCredential(ctx, projB.ID, cred.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project Delete err = %v, want ErrNotFound", err)
	}

	// Lookup by username returns project metadata.
	lookup, err := st.LookupSMTPCredentialByUsername(ctx, "shop-abcd1234")
	if err != nil {
		t.Fatalf("LookupSMTPCredentialByUsername: %v", err)
	}
	if lookup.ProjectSlug != "shop" || !lookup.ProjectPlusAddressTags || lookup.Credential.ID != cred.ID {
		t.Fatalf("unexpected lookup: %+v", lookup)
	}

	// Count and list.
	cnt, err := st.CountSMTPCredentialsByProject(ctx, projA.ID)
	if err != nil || cnt != 1 {
		t.Fatalf("Count = (%d, %v), want (1, nil)", cnt, err)
	}
	list, err := st.ListSMTPCredentialsByProject(ctx, projA.ID)
	if err != nil || len(list) != 1 || list[0].ID != cred.ID {
		t.Fatalf("List = (%+v, %v), want 1 item", list, err)
	}

	// Update label + disable, rotate password, touch last_used_at.
	disabledAt := now.Add(time.Minute)
	updated, err := st.UpdateSMTPCredential(ctx, projA.ID, cred.ID, "staging-v2", &disabledAt)
	if err != nil {
		t.Fatalf("UpdateSMTPCredential: %v", err)
	}
	if updated.Label != "staging-v2" || updated.DisabledAt == nil || !updated.DisabledAt.Equal(disabledAt) {
		t.Fatalf("unexpected updated credential: %+v", updated)
	}

	newEnc := []byte{0x01, 0xaa, 0xbb, 0xcc}
	rotated, err := st.UpdateSMTPCredentialPassword(ctx, projA.ID, cred.ID, newEnc)
	if err != nil {
		t.Fatalf("UpdateSMTPCredentialPassword: %v", err)
	}
	if !bytes.Equal(rotated.PasswordEnc, newEnc) {
		t.Fatalf("PasswordEnc = %x, want %x", rotated.PasswordEnc, newEnc)
	}

	usedAt := now.Add(2 * time.Minute)
	if err := st.TouchSMTPCredentialLastUsed(ctx, projA.ID, cred.ID, usedAt); err != nil {
		t.Fatalf("TouchSMTPCredentialLastUsed: %v", err)
	}
	got, err := st.GetSMTPCredentialByID(ctx, projA.ID, cred.ID)
	if err != nil {
		t.Fatalf("GetSMTPCredentialByID: %v", err)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(usedAt) {
		t.Fatalf("LastUsedAt = %v, want %v", got.LastUsedAt, usedAt)
	}

	// Deleting the project cascades and removes the credential (FR-033).
	if err := st.DeleteProject(ctx, projA.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := st.LookupSMTPCredentialByUsername(ctx, "shop-abcd1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup after project delete err = %v, want ErrNotFound", err)
	}
}

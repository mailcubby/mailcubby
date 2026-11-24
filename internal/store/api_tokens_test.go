package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// FR-065: store-level CRUD, lookup, touch, and revocation for personal API tokens.
func TestStoreAPITokensLifecycle(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

	u1, err := st.CreateUser(ctx, store.CreateUserParams{
		Email: "alice@example.test", Name: "Alice", Now: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(u1): %v", err)
	}
	u2, err := st.CreateUser(ctx, store.CreateUserParams{
		Email: "bob@example.test", Name: "Bob", Now: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(u2): %v", err)
	}
	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug: "shop", Name: "Shop", Color: "blue", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	exp := now.Add(7 * 24 * time.Hour)
	t1, err := st.CreateAPIToken(ctx, store.CreateAPITokenParams{
		UserID:    u1.ID,
		ProjectID: &proj.ID,
		Name:      "CI Read",
		Prefix:    "AAAABBBBCCCC",
		TokenHash: "hash-1",
		Scope:     "read",
		Now:       now,
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatalf("CreateAPIToken(t1): %v", err)
	}
	if t1.ID == 0 || t1.Scope != "read" || t1.ProjectID == nil || *t1.ProjectID != proj.ID {
		t.Fatalf("unexpected t1: %+v", t1)
	}

	// Lookup by prefix returns joined project slug.
	found, err := st.GetAPITokenByPrefix(ctx, "AAAABBBBCCCC")
	if err != nil {
		t.Fatalf("GetAPITokenByPrefix: %v", err)
	}
	if found.ProjectSlug == nil || *found.ProjectSlug != "shop" {
		t.Fatalf("ProjectSlug = %v, want shop", found.ProjectSlug)
	}

	// Touch updates last_used_at.
	usedAt := now.Add(2 * time.Minute)
	if err := st.TouchAPITokenLastUsed(ctx, t1.ID, usedAt); err != nil {
		t.Fatalf("TouchAPITokenLastUsed: %v", err)
	}
	found, err = st.GetAPITokenByPrefix(ctx, "AAAABBBBCCCC")
	if err != nil {
		t.Fatalf("GetAPITokenByPrefix after touch: %v", err)
	}
	if found.LastUsedAt == nil || !found.LastUsedAt.Equal(usedAt) {
		t.Fatalf("LastUsedAt = %v, want %v", found.LastUsedAt, usedAt)
	}

	// List returns only owner's active tokens.
	list, err := st.ListAPITokensByUser(ctx, u1.ID)
	if err != nil {
		t.Fatalf("ListAPITokensByUser: %v", err)
	}
	if len(list) != 1 || list[0].ID != t1.ID {
		t.Fatalf("ListAPITokensByUser = %+v, want [t1]", list)
	}

	// Another user cannot revoke u1's token.
	err = st.WithTx(ctx, func(tx *store.Tx) error {
		return tx.RevokeAPITokenForUser(ctx, t1.ID, u2.ID, now.Add(time.Hour))
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RevokeAPITokenForUser(wrong user) = %v, want ErrNotFound", err)
	}

	// Owner revokes token; it disappears from list and cannot be revoked twice.
	err = st.WithTx(ctx, func(tx *store.Tx) error {
		return tx.RevokeAPITokenForUser(ctx, t1.ID, u1.ID, now.Add(time.Hour))
	})
	if err != nil {
		t.Fatalf("RevokeAPITokenForUser: %v", err)
	}

	list, err = st.ListAPITokensByUser(ctx, u1.ID)
	if err != nil {
		t.Fatalf("ListAPITokensByUser after revoke: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 active tokens after revoke, got %d", len(list))
	}

	err = st.WithTx(ctx, func(tx *store.Tx) error {
		return tx.RevokeAPITokenForUser(ctx, t1.ID, u1.ID, now.Add(2*time.Hour))
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second RevokeAPITokenForUser = %v, want ErrNotFound", err)
	}
}

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestUsersSessionsResetsAndAudit(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	cnt, err := st.CountUsers(ctx)
	if err != nil || cnt != 0 {
		t.Fatalf("CountUsers = (%d, %v), want (0, nil)", cnt, err)
	}

	hash := "$argon2id$v=19$m=19456,t=2,p=1$salt$hash"
	user, err := st.CreateUser(ctx, store.CreateUserParams{
		Email:             "Admin@Example.Test",
		Name:              "Admin",
		PasswordHash:      &hash,
		IsAdmin:           true,
		CanCreateProjects: true,
		Now:               now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Case-insensitive lookup and unique email conflict (FR-041).
	byEmail, err := st.GetUserByEmail(ctx, "admin@example.test")
	if err != nil || byEmail.ID != user.ID {
		t.Fatalf("GetUserByEmail = (%+v, %v), want ID %d", byEmail, err, user.ID)
	}
	if _, err := st.CreateUser(ctx, store.CreateUserParams{
		Email: "admin@example.test",
		Name:  "Dup",
		Now:   now,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate email err = %v, want ErrConflict", err)
	}

	// Sessions lifecycle (FR-044).
	s1, err := st.CreateSession(ctx, store.CreateSessionParams{
		ID:        "sess-1",
		UserID:    user.ID,
		CSRFToken: "csrf-1",
		IP:        "127.0.0.1",
		UserAgent: "TestAgent/1",
		Now:       now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	s2, err := st.CreateSession(ctx, store.CreateSessionParams{
		ID:        "sess-2",
		UserID:    user.ID,
		CSRFToken: "csrf-2",
		IP:        "127.0.0.1",
		UserAgent: "TestAgent/2",
		Now:       now.Add(time.Minute),
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}

	list, err := st.ListSessionsByUser(ctx, user.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSessionsByUser = (%d, %v), want 2", len(list), err)
	}

	// Password change deletes other sessions.
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		deleted, err := tx.DeleteOtherSessionsForUser(ctx, user.ID, s2.ID)
		if err != nil {
			return err
		}
		if deleted != 1 {
			t.Fatalf("DeleteOtherSessionsForUser deleted = %d, want 1", deleted)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTx delete others: %v", err)
	}
	if _, err := st.GetSessionByID(ctx, s1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected s1 deleted, got err = %v", err)
	}
	if _, err := st.GetSessionByID(ctx, s2.ID); err != nil {
		t.Fatalf("expected s2 retained, got err = %v", err)
	}

	// Password reset single-use consume (FR-046).
	reset, err := st.CreatePasswordReset(ctx, store.CreatePasswordResetParams{
		UserID:    user.ID,
		TokenHash: "hash-reset-1",
		Now:       now,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		consumed, err := tx.ConsumePasswordReset(ctx, reset.ID, now.Add(5*time.Minute))
		if err != nil || !consumed {
			t.Fatalf("first ConsumePasswordReset = (%v, %v), want (true, nil)", consumed, err)
		}
		consumedAgain, err := tx.ConsumePasswordReset(ctx, reset.ID, now.Add(6*time.Minute))
		if err != nil || consumedAgain {
			t.Fatalf("second ConsumePasswordReset = (%v, %v), want (false, nil)", consumedAgain, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTx consume reset: %v", err)
	}

	// Audit writer (FR-105).
	ip := "127.0.0.1"
	label := user.Email
	entry, err := st.InsertAudit(ctx, store.InsertAuditParams{
		At:          now,
		ActorType:   store.AuditActorUser,
		ActorUserID: &user.ID,
		ActorLabel:  &label,
		Action:      "auth.login",
		IP:          &ip,
		Metadata:    map[string]any{"method": "password"},
	})
	if err != nil {
		t.Fatalf("InsertAudit: %v", err)
	}
	if entry.Action != "auth.login" || entry.MetadataJSON == nil {
		t.Fatalf("unexpected audit entry: %+v", entry)
	}
	entries, err := st.ListAuditEntries(ctx, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListAuditEntries = (%d, %v), want 1", len(entries), err)
	}
}

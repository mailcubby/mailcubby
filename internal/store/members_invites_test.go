package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestStoreMembersAndInvitesLifecycle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := testutil.NewStore(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	u1, err := st.CreateUser(ctx, store.CreateUserParams{
		Email: "owner@example.test", Name: "Owner", Now: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(u1): %v", err)
	}
	u2, err := st.CreateUser(ctx, store.CreateUserParams{
		Email: "member@example.test", Name: "Member", Now: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(u2): %v", err)
	}

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug: "acme", Name: "Acme", Color: "blue", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := st.AddProjectMember(ctx, proj.ID, u1.ID, "owner", now); err != nil {
		t.Fatalf("AddProjectMember(u1): %v", err)
	}
	if _, err := st.AddProjectMember(ctx, proj.ID, u2.ID, "viewer", now.Add(time.Second)); err != nil {
		t.Fatalf("AddProjectMember(u2): %v", err)
	}

	if owners, err := st.CountProjectOwners(ctx, proj.ID); err != nil || owners != 1 {
		t.Fatalf("CountProjectOwners = (%d, %v), want (1, nil)", owners, err)
	}

	members, err := st.ListProjectMembers(ctx, proj.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListProjectMembers = (%d, %v), want (2, nil)", len(members), err)
	}

	if err := st.UpdateProjectMemberRole(ctx, proj.ID, u2.ID, "member"); err != nil {
		t.Fatalf("UpdateProjectMemberRole: %v", err)
	}
	got, err := st.GetProjectMember(ctx, proj.ID, u2.ID)
	if err != nil || got.Role != "member" {
		t.Fatalf("GetProjectMember = (%+v, %v), want role=member", got, err)
	}

	ids, err := st.ListProjectIDsForUser(ctx, u2.ID)
	if err != nil || len(ids) != 1 || ids[0] != proj.ID {
		t.Fatalf("ListProjectIDsForUser = (%v, %v), want [%d]", ids, err, proj.ID)
	}

	// Project invite + atomic single-use consume.
	role := "member"
	inv, err := st.CreateInvite(ctx, store.CreateInviteParams{
		ProjectID: &proj.ID,
		Email:     "new@example.test",
		Role:      &role,
		TokenHash: "hash-project-invite",
		CreatedBy: &u1.ID,
		Now:       now,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if inv.Status(now) != "pending" {
		t.Fatalf("invite status = %q, want pending", inv.Status(now))
	}
	if inv.Status(now.Add(25*time.Hour)) != "expired" {
		t.Fatalf("invite status after expiry = %q, want expired", inv.Status(now.Add(25*time.Hour)))
	}

	looked, err := st.GetInviteByTokenHash(ctx, "hash-project-invite")
	if err != nil {
		t.Fatalf("GetInviteByTokenHash: %v", err)
	}
	if looked.ProjectSlug == nil || *looked.ProjectSlug != "acme" {
		t.Fatalf("looked.ProjectSlug = %v, want acme", looked.ProjectSlug)
	}

	var firstOK, secondOK bool
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		firstOK, err = tx.ConsumeInvite(ctx, inv.ID, &u2.ID, now.Add(time.Minute))
		if err != nil {
			return err
		}
		secondOK, err = tx.ConsumeInvite(ctx, inv.ID, &u2.ID, now.Add(time.Minute))
		return err
	}); err != nil {
		t.Fatalf("ConsumeInvite tx: %v", err)
	}
	if !firstOK || secondOK {
		t.Fatalf("ConsumeInvite = (first=%v, second=%v), want (true, false)", firstOK, secondOK)
	}

	// Deleting project cascades members and invites.
	if err := st.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := st.GetProjectMember(ctx, proj.ID, u1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after project delete GetProjectMember err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetInviteByTokenHash(ctx, "hash-project-invite"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after project delete GetInviteByTokenHash err = %v, want ErrNotFound", err)
	}
}

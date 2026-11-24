package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// Invite represents a project or instance invitation (SPEC FR-055 to FR-057).
type Invite struct {
	ID             int64
	ProjectID      *int64
	ProjectSlug    *string
	ProjectName    *string
	Email          string
	Role           *string
	TokenHash      string
	CreatedBy      *int64
	CreatedByName  *string
	CreatedByEmail *string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	AcceptedAt     *time.Time
	AcceptedBy     *int64
	RevokedAt      *time.Time
}

// Status returns the invite lifecycle state at now: "pending", "accepted",
// "revoked", or "expired" (SPEC FR-055).
func (i Invite) Status(now time.Time) string {
	if i.AcceptedAt != nil {
		return "accepted"
	}
	if i.RevokedAt != nil {
		return "revoked"
	}
	if !now.UTC().Before(i.ExpiresAt.UTC()) {
		return "expired"
	}
	return "pending"
}

// InviterLabel returns the inviter's display name, falling back to email, or nil
// if the invite was created by CLI or the inviter account was deleted.
func (i Invite) InviterLabel() *string {
	if i.CreatedByName != nil && strings.TrimSpace(*i.CreatedByName) != "" {
		v := strings.TrimSpace(*i.CreatedByName)
		return &v
	}
	if i.CreatedByEmail != nil && strings.TrimSpace(*i.CreatedByEmail) != "" {
		v := strings.TrimSpace(*i.CreatedByEmail)
		return &v
	}
	return nil
}

// CreateInviteParams specifies inputs for inserting a project or instance invite.
// ProjectID and Role must either both be set (project invite) or both be nil (instance invite).
type CreateInviteParams struct {
	ProjectID *int64
	Email     string
	Role      *string
	TokenHash string
	CreatedBy *int64
	Now       time.Time
	ExpiresAt time.Time
}

// CreateInvite inserts a new invite row and returns it.
func (s *Store) CreateInvite(ctx context.Context, p CreateInviteParams) (Invite, error) {
	return createInvite(ctx, s.writeQ, p)
}

// CreateInvite inserts a new invite row inside tx and returns it.
func (t *Tx) CreateInvite(ctx context.Context, p CreateInviteParams) (Invite, error) {
	return createInvite(ctx, t.q, p)
}

func createInvite(ctx context.Context, q *sqlc.Queries, p CreateInviteParams) (Invite, error) {
	row, err := q.CreateInvite(ctx, sqlc.CreateInviteParams{
		ProjectID: nullInt64Ptr(p.ProjectID),
		Email:     p.Email,
		Role:      toNullString(p.Role),
		TokenHash: p.TokenHash,
		CreatedBy: nullInt64Ptr(p.CreatedBy),
		CreatedAt: p.Now.UTC().UnixMilli(),
		ExpiresAt: p.ExpiresAt.UTC().UnixMilli(),
	})
	if err != nil {
		return Invite{}, fmt.Errorf("store: create invite: %w", mapError(err))
	}
	return inviteFromBaseRow(row), nil
}

// GetProjectInviteByID fetches a single invite belonging to projectID.
func (s *Store) GetProjectInviteByID(ctx context.Context, projectID, inviteID int64) (Invite, error) {
	return getProjectInviteByID(ctx, s.readQ, projectID, inviteID)
}

// GetProjectInviteByID fetches a single invite belonging to projectID inside tx.
func (t *Tx) GetProjectInviteByID(ctx context.Context, projectID, inviteID int64) (Invite, error) {
	return getProjectInviteByID(ctx, t.q, projectID, inviteID)
}

func getProjectInviteByID(ctx context.Context, q *sqlc.Queries, projectID, inviteID int64) (Invite, error) {
	row, err := q.GetProjectInviteByID(ctx, sqlc.GetProjectInviteByIDParams{
		ProjectID: sql.NullInt64{Int64: projectID, Valid: true},
		ID:        inviteID,
	})
	if err != nil {
		return Invite{}, fmt.Errorf("store: get project invite (project=%d, id=%d): %w", projectID, inviteID, mapError(err))
	}
	return Invite{
		ID:             row.ID,
		ProjectID:      int64PtrFromNull(row.ProjectID),
		Email:          row.Email,
		Role:           fromNullString(row.Role),
		TokenHash:      row.TokenHash,
		CreatedBy:      int64PtrFromNull(row.CreatedBy),
		CreatedByName:  fromNullString(row.CreatedByName),
		CreatedByEmail: fromNullString(row.CreatedByEmail),
		CreatedAt:      time.UnixMilli(row.CreatedAt).UTC(),
		ExpiresAt:      time.UnixMilli(row.ExpiresAt).UTC(),
		AcceptedAt:     timePtrFromNullMs(row.AcceptedAt),
		AcceptedBy:     int64PtrFromNull(row.AcceptedBy),
		RevokedAt:      timePtrFromNullMs(row.RevokedAt),
	}, nil
}

// ListProjectInvites returns all invites for projectID ordered newest first.
func (s *Store) ListProjectInvites(ctx context.Context, projectID int64) ([]Invite, error) {
	rows, err := s.readQ.ListProjectInvites(ctx, sql.NullInt64{Int64: projectID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("store: list project invites (project=%d): %w", projectID, mapError(err))
	}
	out := make([]Invite, 0, len(rows))
	for _, row := range rows {
		out = append(out, Invite{
			ID:             row.ID,
			ProjectID:      int64PtrFromNull(row.ProjectID),
			Email:          row.Email,
			Role:           fromNullString(row.Role),
			TokenHash:      row.TokenHash,
			CreatedBy:      int64PtrFromNull(row.CreatedBy),
			CreatedByName:  fromNullString(row.CreatedByName),
			CreatedByEmail: fromNullString(row.CreatedByEmail),
			CreatedAt:      time.UnixMilli(row.CreatedAt).UTC(),
			ExpiresAt:      time.UnixMilli(row.ExpiresAt).UTC(),
			AcceptedAt:     timePtrFromNullMs(row.AcceptedAt),
			AcceptedBy:     int64PtrFromNull(row.AcceptedBy),
			RevokedAt:      timePtrFromNullMs(row.RevokedAt),
		})
	}
	return out, nil
}

// RevokePendingProjectInvitesForEmail revokes any currently pending invites for
// (projectID, email) (SPEC FR-055).
func (t *Tx) RevokePendingProjectInvitesForEmail(ctx context.Context, projectID int64, email string, now time.Time) (int64, error) {
	nowMs := now.UTC().UnixMilli()
	n, err := t.q.RevokePendingProjectInvitesForEmail(ctx, sqlc.RevokePendingProjectInvitesForEmailParams{
		RevokedAt: sql.NullInt64{Int64: nowMs, Valid: true},
		ProjectID: sql.NullInt64{Int64: projectID, Valid: true},
		Email:     email,
		ExpiresAt: nowMs,
	})
	if err != nil {
		return 0, fmt.Errorf("store: revoke pending invites (project=%d): %w", projectID, mapError(err))
	}
	return n, nil
}

// RevokeProjectInvite revokes inviteID in projectID if not yet accepted or revoked.
// Returns true if the invite was revoked by this call.
func (t *Tx) RevokeProjectInvite(ctx context.Context, projectID, inviteID int64, now time.Time) (bool, error) {
	n, err := t.q.RevokeProjectInvite(ctx, sqlc.RevokeProjectInviteParams{
		RevokedAt: sql.NullInt64{Int64: now.UTC().UnixMilli(), Valid: true},
		ProjectID: sql.NullInt64{Int64: projectID, Valid: true},
		ID:        inviteID,
	})
	if err != nil {
		return false, fmt.Errorf("store: revoke project invite (project=%d, id=%d): %w", projectID, inviteID, mapError(err))
	}
	return n > 0, nil
}

// GetInviteByTokenHash looks up an invite (project or instance) by its SHA-256 token hash.
func (s *Store) GetInviteByTokenHash(ctx context.Context, tokenHash string) (Invite, error) {
	return getInviteByTokenHash(ctx, s.readQ, tokenHash)
}

// GetInviteByTokenHash looks up an invite by token hash inside tx.
func (t *Tx) GetInviteByTokenHash(ctx context.Context, tokenHash string) (Invite, error) {
	return getInviteByTokenHash(ctx, t.q, tokenHash)
}

func getInviteByTokenHash(ctx context.Context, q *sqlc.Queries, tokenHash string) (Invite, error) {
	row, err := q.GetInviteByTokenHash(ctx, tokenHash)
	if err != nil {
		return Invite{}, fmt.Errorf("store: get invite by token hash: %w", mapError(err))
	}
	return Invite{
		ID:             row.ID,
		ProjectID:      int64PtrFromNull(row.ProjectID),
		ProjectSlug:    fromNullString(row.ProjectSlug),
		ProjectName:    fromNullString(row.ProjectName),
		Email:          row.Email,
		Role:           fromNullString(row.Role),
		TokenHash:      row.TokenHash,
		CreatedBy:      int64PtrFromNull(row.CreatedBy),
		CreatedByName:  fromNullString(row.CreatedByName),
		CreatedByEmail: fromNullString(row.CreatedByEmail),
		CreatedAt:      time.UnixMilli(row.CreatedAt).UTC(),
		ExpiresAt:      time.UnixMilli(row.ExpiresAt).UTC(),
		AcceptedAt:     timePtrFromNullMs(row.AcceptedAt),
		AcceptedBy:     int64PtrFromNull(row.AcceptedBy),
		RevokedAt:      timePtrFromNullMs(row.RevokedAt),
	}, nil
}

// ConsumeInvite atomically marks inviteID accepted inside tx only if it is still
// unaccepted, unrevoked, and unexpired at now (SPEC FR-056 single-use guard).
// Returns true iff this call consumed the invite.
func (t *Tx) ConsumeInvite(ctx context.Context, inviteID int64, acceptedBy *int64, now time.Time) (bool, error) {
	nowMs := now.UTC().UnixMilli()
	affected, err := t.q.ConsumeInvite(ctx, sqlc.ConsumeInviteParams{
		AcceptedAt: sql.NullInt64{Int64: nowMs, Valid: true},
		AcceptedBy: nullInt64Ptr(acceptedBy),
		ID:         inviteID,
		ExpiresAt:  nowMs,
	})
	if err != nil {
		return false, fmt.Errorf("store: consume invite %d: %w", inviteID, mapError(err))
	}
	return affected > 0, nil
}

// SetInviteAcceptedBy records the newly created user's ID on an already-consumed
// invite inside the same transaction.
func (t *Tx) SetInviteAcceptedBy(ctx context.Context, inviteID, userID int64) error {
	if err := t.q.SetInviteAcceptedBy(ctx, sqlc.SetInviteAcceptedByParams{
		AcceptedBy: sql.NullInt64{Int64: userID, Valid: true},
		ID:         inviteID,
	}); err != nil {
		return fmt.Errorf("store: set invite %d accepted_by: %w", inviteID, mapError(err))
	}
	return nil
}

func inviteFromBaseRow(row sqlc.Invite) Invite {
	return Invite{
		ID:         row.ID,
		ProjectID:  int64PtrFromNull(row.ProjectID),
		Email:      row.Email,
		Role:       fromNullString(row.Role),
		TokenHash:  row.TokenHash,
		CreatedBy:  int64PtrFromNull(row.CreatedBy),
		CreatedAt:  time.UnixMilli(row.CreatedAt).UTC(),
		ExpiresAt:  time.UnixMilli(row.ExpiresAt).UTC(),
		AcceptedAt: timePtrFromNullMs(row.AcceptedAt),
		AcceptedBy: int64PtrFromNull(row.AcceptedBy),
		RevokedAt:  timePtrFromNullMs(row.RevokedAt),
	}
}

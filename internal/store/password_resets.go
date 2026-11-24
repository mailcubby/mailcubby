package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// PasswordReset represents a single-use password reset token record (SPEC FR-046).
type PasswordReset struct {
	ID        int64
	UserID    int64
	TokenHash string
	CreatedBy *int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// CreatePasswordResetParams specifies inputs for creating a password reset link.
type CreatePasswordResetParams struct {
	UserID    int64
	TokenHash string
	CreatedBy *int64
	Now       time.Time
	ExpiresAt time.Time
}

// CreatePasswordReset inserts a new single-use password reset record.
func (s *Store) CreatePasswordReset(ctx context.Context, p CreatePasswordResetParams) (PasswordReset, error) {
	return createPasswordReset(ctx, s.writeQ, p)
}

// CreatePasswordReset inserts a new single-use password reset record inside tx.
func (t *Tx) CreatePasswordReset(ctx context.Context, p CreatePasswordResetParams) (PasswordReset, error) {
	return createPasswordReset(ctx, t.q, p)
}

func createPasswordReset(ctx context.Context, q *sqlc.Queries, p CreatePasswordResetParams) (PasswordReset, error) {
	row, err := q.CreatePasswordReset(ctx, sqlc.CreatePasswordResetParams{
		UserID:    p.UserID,
		TokenHash: p.TokenHash,
		CreatedBy: nullInt64Ptr(p.CreatedBy),
		CreatedAt: p.Now.UTC().UnixMilli(),
		ExpiresAt: p.ExpiresAt.UTC().UnixMilli(),
	})
	if err != nil {
		return PasswordReset{}, fmt.Errorf("store: create password reset: %w", mapError(err))
	}
	return passwordResetFromRow(row), nil
}

// GetPasswordResetByTokenHash fetches a password reset record by SHA-256 token hash.
func (s *Store) GetPasswordResetByTokenHash(ctx context.Context, tokenHash string) (PasswordReset, error) {
	return getPasswordResetByTokenHash(ctx, s.readQ, tokenHash)
}

// GetPasswordResetByTokenHash fetches a password reset record inside tx.
func (t *Tx) GetPasswordResetByTokenHash(ctx context.Context, tokenHash string) (PasswordReset, error) {
	return getPasswordResetByTokenHash(ctx, t.q, tokenHash)
}

func getPasswordResetByTokenHash(ctx context.Context, q *sqlc.Queries, tokenHash string) (PasswordReset, error) {
	row, err := q.GetPasswordResetByTokenHash(ctx, tokenHash)
	if err != nil {
		return PasswordReset{}, fmt.Errorf("store: get password reset: %w", mapError(err))
	}
	return passwordResetFromRow(row), nil
}

// ConsumePasswordReset atomically marks a password reset record as used if it
// has not already been used and has not expired at now. Returns false if the
// token was already consumed or expired.
func (t *Tx) ConsumePasswordReset(ctx context.Context, id int64, now time.Time) (bool, error) {
	nowMs := now.UTC().UnixMilli()
	affected, err := t.q.ConsumePasswordReset(ctx, sqlc.ConsumePasswordResetParams{
		UsedAt:    sql.NullInt64{Int64: nowMs, Valid: true},
		ID:        id,
		ExpiresAt: nowMs,
	})
	if err != nil {
		return false, fmt.Errorf("store: consume password reset %d: %w", id, mapError(err))
	}
	return affected > 0, nil
}

// DeletePasswordResetsForUser removes all password reset tokens belonging to userID.
func (t *Tx) DeletePasswordResetsForUser(ctx context.Context, userID int64) (int64, error) {
	n, err := t.q.DeletePasswordResetsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("store: delete password resets for user %d: %w", userID, mapError(err))
	}
	return n, nil
}

func passwordResetFromRow(r sqlc.PasswordReset) PasswordReset {
	return PasswordReset{
		ID:        r.ID,
		UserID:    r.UserID,
		TokenHash: r.TokenHash,
		CreatedBy: int64PtrFromNull(r.CreatedBy),
		CreatedAt: time.UnixMilli(r.CreatedAt).UTC(),
		ExpiresAt: time.UnixMilli(r.ExpiresAt).UTC(),
		UsedAt:    fromNullTimeMs(r.UsedAt),
	}
}

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// APIToken represents a personal API bearer token (SPEC FR-065).
type APIToken struct {
	ID          int64
	UserID      int64
	ProjectID   *int64
	ProjectSlug *string
	Name        string
	Prefix      string
	TokenHash   string
	Scope       string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

// CreateAPITokenParams specifies inputs for creating a personal API token.
type CreateAPITokenParams struct {
	UserID    int64
	ProjectID *int64
	Name      string
	Prefix    string
	TokenHash string
	Scope     string
	Now       time.Time
	ExpiresAt *time.Time
}

// CreateAPIToken inserts a new API token row.
func (s *Store) CreateAPIToken(ctx context.Context, p CreateAPITokenParams) (APIToken, error) {
	return createAPIToken(ctx, s.writeQ, p)
}

// CreateAPIToken inserts a new API token row inside tx.
func (t *Tx) CreateAPIToken(ctx context.Context, p CreateAPITokenParams) (APIToken, error) {
	return createAPIToken(ctx, t.q, p)
}

func createAPIToken(ctx context.Context, q *sqlc.Queries, p CreateAPITokenParams) (APIToken, error) {
	row, err := q.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		UserID:    p.UserID,
		ProjectID: nullInt64Ptr(p.ProjectID),
		Name:      p.Name,
		Prefix:    p.Prefix,
		TokenHash: p.TokenHash,
		Scope:     p.Scope,
		CreatedAt: p.Now.UTC().UnixMilli(),
		ExpiresAt: toNullTimeMs(p.ExpiresAt),
	})
	if err != nil {
		return APIToken{}, fmt.Errorf("store: create api token: %w", mapError(err))
	}
	return APIToken{
		ID:         row.ID,
		UserID:     row.UserID,
		ProjectID:  int64PtrFromNull(row.ProjectID),
		Name:       row.Name,
		Prefix:     row.Prefix,
		TokenHash:  row.TokenHash,
		Scope:      row.Scope,
		CreatedAt:  time.UnixMilli(row.CreatedAt).UTC(),
		LastUsedAt: fromNullTimeMs(row.LastUsedAt),
		ExpiresAt:  fromNullTimeMs(row.ExpiresAt),
		RevokedAt:  fromNullTimeMs(row.RevokedAt),
	}, nil
}

// ListAPITokensByUser returns all non-revoked API tokens belonging to userID, newest first.
func (s *Store) ListAPITokensByUser(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.readQ.ListAPITokensByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list api tokens for user %d: %w", userID, mapError(err))
	}
	out := make([]APIToken, 0, len(rows))
	for _, r := range rows {
		out = append(out, APIToken{
			ID:          r.ID,
			UserID:      r.UserID,
			ProjectID:   int64PtrFromNull(r.ProjectID),
			ProjectSlug: fromNullString(r.ProjectSlug),
			Name:        r.Name,
			Prefix:      r.Prefix,
			TokenHash:   r.TokenHash,
			Scope:       r.Scope,
			CreatedAt:   time.UnixMilli(r.CreatedAt).UTC(),
			LastUsedAt:  fromNullTimeMs(r.LastUsedAt),
			ExpiresAt:   fromNullTimeMs(r.ExpiresAt),
			RevokedAt:   fromNullTimeMs(r.RevokedAt),
		})
	}
	return out, nil
}

// GetAPITokenByPrefix looks up an API token by its unique 12-char prefix.
func (s *Store) GetAPITokenByPrefix(ctx context.Context, prefix string) (APIToken, error) {
	r, err := s.readQ.GetAPITokenByPrefix(ctx, prefix)
	if err != nil {
		return APIToken{}, fmt.Errorf("store: get api token by prefix: %w", mapError(err))
	}
	return APIToken{
		ID:          r.ID,
		UserID:      r.UserID,
		ProjectID:   int64PtrFromNull(r.ProjectID),
		ProjectSlug: fromNullString(r.ProjectSlug),
		Name:        r.Name,
		Prefix:      r.Prefix,
		TokenHash:   r.TokenHash,
		Scope:       r.Scope,
		CreatedAt:   time.UnixMilli(r.CreatedAt).UTC(),
		LastUsedAt:  fromNullTimeMs(r.LastUsedAt),
		ExpiresAt:   fromNullTimeMs(r.ExpiresAt),
		RevokedAt:   fromNullTimeMs(r.RevokedAt),
	}, nil
}

// GetAPITokenByIDForUser loads a non-revoked API token owned by userID inside tx.
func (t *Tx) GetAPITokenByIDForUser(ctx context.Context, id, userID int64) (APIToken, error) {
	r, err := t.q.GetAPITokenByIDForUser(ctx, sqlc.GetAPITokenByIDForUserParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		return APIToken{}, fmt.Errorf("store: get api token %d for user %d: %w", id, userID, mapError(err))
	}
	return APIToken{
		ID:          r.ID,
		UserID:      r.UserID,
		ProjectID:   int64PtrFromNull(r.ProjectID),
		ProjectSlug: fromNullString(r.ProjectSlug),
		Name:        r.Name,
		Prefix:      r.Prefix,
		TokenHash:   r.TokenHash,
		Scope:       r.Scope,
		CreatedAt:   time.UnixMilli(r.CreatedAt).UTC(),
		LastUsedAt:  fromNullTimeMs(r.LastUsedAt),
		ExpiresAt:   fromNullTimeMs(r.ExpiresAt),
		RevokedAt:   fromNullTimeMs(r.RevokedAt),
	}, nil
}

// RevokeAPITokenForUser marks an active API token owned by userID as revoked inside tx.
func (t *Tx) RevokeAPITokenForUser(ctx context.Context, id, userID int64, now time.Time) error {
	affected, err := t.q.RevokeAPITokenForUser(ctx, sqlc.RevokeAPITokenForUserParams{
		RevokedAt: sql.NullInt64{Int64: now.UTC().UnixMilli(), Valid: true},
		ID:        id,
		UserID:    userID,
	})
	if err != nil {
		return fmt.Errorf("store: revoke api token %d for user %d: %w", id, userID, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: revoke api token %d for user %d: %w", id, userID, ErrNotFound)
	}
	return nil
}

// TouchAPITokenLastUsed updates api_tokens.last_used_at.
func (s *Store) TouchAPITokenLastUsed(ctx context.Context, id int64, now time.Time) error {
	if err := s.writeQ.TouchAPITokenLastUsed(ctx, sqlc.TouchAPITokenLastUsedParams{
		LastUsedAt: sql.NullInt64{Int64: now.UTC().UnixMilli(), Valid: true},
		ID:         id,
	}); err != nil {
		return fmt.Errorf("store: touch api token %d last_used_at: %w", id, mapError(err))
	}
	return nil
}

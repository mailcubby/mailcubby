package store

import (
	"context"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// Session represents a server-side browser session (SPEC FR-044).
type Session struct {
	ID         string
	UserID     int64
	CSRFToken  string
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSessionParams specifies inputs for creating a session.
type CreateSessionParams struct {
	ID        string
	UserID    int64
	CSRFToken string
	IP        string
	UserAgent string
	Now       time.Time
	ExpiresAt time.Time
}

// CreateSession inserts a new session row.
func (s *Store) CreateSession(ctx context.Context, p CreateSessionParams) (Session, error) {
	return createSession(ctx, s.writeQ, p)
}

// CreateSession inserts a new session row inside tx.
func (t *Tx) CreateSession(ctx context.Context, p CreateSessionParams) (Session, error) {
	return createSession(ctx, t.q, p)
}

func createSession(ctx context.Context, q *sqlc.Queries, p CreateSessionParams) (Session, error) {
	nowMs := p.Now.UTC().UnixMilli()
	var ipPtr, uaPtr *string
	if p.IP != "" {
		ipPtr = &p.IP
	}
	if p.UserAgent != "" {
		uaPtr = &p.UserAgent
	}
	row, err := q.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:         p.ID,
		UserID:     p.UserID,
		CsrfToken:  p.CSRFToken,
		Ip:         toNullString(ipPtr),
		UserAgent:  toNullString(uaPtr),
		CreatedAt:  nowMs,
		LastSeenAt: nowMs,
		ExpiresAt:  p.ExpiresAt.UTC().UnixMilli(),
	})
	if err != nil {
		return Session{}, fmt.Errorf("store: create session: %w", mapError(err))
	}
	return sessionFromRow(row), nil
}

// GetSessionByID fetches a session by its hashed token ID.
func (s *Store) GetSessionByID(ctx context.Context, id string) (Session, error) {
	row, err := s.readQ.GetSessionByID(ctx, id)
	if err != nil {
		return Session{}, fmt.Errorf("store: get session: %w", mapError(err))
	}
	return sessionFromRow(row), nil
}

// TouchSession updates a session's last_seen_at timestamp.
func (s *Store) TouchSession(ctx context.Context, id string, now time.Time) error {
	if err := s.writeQ.TouchSession(ctx, sqlc.TouchSessionParams{
		LastSeenAt: now.UTC().UnixMilli(),
		ID:         id,
	}); err != nil {
		return fmt.Errorf("store: touch session: %w", mapError(err))
	}
	return nil
}

// ListSessionsByUser returns all sessions belonging to userID, ordered newest activity first.
func (s *Store) ListSessionsByUser(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.readQ.ListSessionsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions for user %d: %w", userID, mapError(err))
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, sessionFromRow(r))
	}
	return out, nil
}

// DeleteSessionByID deletes a session by ID.
func (s *Store) DeleteSessionByID(ctx context.Context, id string) error {
	return deleteSessionByID(ctx, s.writeQ, id)
}

// DeleteSessionByID deletes a session by ID inside tx.
func (t *Tx) DeleteSessionByID(ctx context.Context, id string) error {
	return deleteSessionByID(ctx, t.q, id)
}

func deleteSessionByID(ctx context.Context, q *sqlc.Queries, id string) error {
	affected, err := q.DeleteSessionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("store: delete session: %w", mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete session: %w", ErrNotFound)
	}
	return nil
}

// DeleteSessionByIDForUser revokes a specific session owned by userID.
func (s *Store) DeleteSessionByIDForUser(ctx context.Context, id string, userID int64) error {
	affected, err := s.writeQ.DeleteSessionByIDForUser(ctx, sqlc.DeleteSessionByIDForUserParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("store: delete session for user %d: %w", userID, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete session for user %d: %w", userID, ErrNotFound)
	}
	return nil
}

// DeleteOtherSessionsForUser deletes every session of userID except keepSessionID (SPEC FR-044).
func (t *Tx) DeleteOtherSessionsForUser(ctx context.Context, userID int64, keepSessionID string) (int64, error) {
	n, err := t.q.DeleteOtherSessionsForUser(ctx, sqlc.DeleteOtherSessionsForUserParams{
		UserID: userID,
		ID:     keepSessionID,
	})
	if err != nil {
		return 0, fmt.Errorf("store: delete other sessions for user %d: %w", userID, mapError(err))
	}
	return n, nil
}

// DeleteAllSessionsForUser deletes every session belonging to userID (SPEC FR-044, FR-046).
func (t *Tx) DeleteAllSessionsForUser(ctx context.Context, userID int64) (int64, error) {
	n, err := t.q.DeleteAllSessionsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("store: delete all sessions for user %d: %w", userID, mapError(err))
	}
	return n, nil
}

func sessionFromRow(r sqlc.Session) Session {
	return Session{
		ID:         r.ID,
		UserID:     r.UserID,
		CSRFToken:  r.CsrfToken,
		IP:         r.Ip.String,
		UserAgent:  r.UserAgent.String,
		CreatedAt:  time.UnixMilli(r.CreatedAt).UTC(),
		LastSeenAt: time.UnixMilli(r.LastSeenAt).UTC(),
		ExpiresAt:  time.UnixMilli(r.ExpiresAt).UTC(),
	}
}

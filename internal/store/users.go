package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// AdminUser pairs a User with their project membership count for admin views (SPEC FR-100).
type AdminUser struct {
	User         User
	ProjectCount int64
}

// UpdateUserAdminParams specifies fields editable by an instance administrator.
type UpdateUserAdminParams struct {
	ID                int64
	Name              string
	IsAdmin           bool
	CanCreateProjects bool
	DisabledAt        *time.Time
	Now               time.Time
}

// User represents a Mailcubby user account (SPEC FR-041).
type User struct {
	ID                int64
	Email             string
	Name              string
	PasswordHash      *string
	IsAdmin           bool
	CanCreateProjects bool
	DisabledAt        *time.Time
	LastLoginAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CreateUserParams specifies inputs for inserting a new user.
type CreateUserParams struct {
	Email             string
	Name              string
	PasswordHash      *string
	IsAdmin           bool
	CanCreateProjects bool
	DisabledAt        *time.Time
	LastLoginAt       *time.Time
	Now               time.Time
}

// CountUsers returns the total number of users in the database.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	return countUsers(ctx, s.readQ)
}

// CountUsers returns the total number of users inside tx.
func (t *Tx) CountUsers(ctx context.Context) (int64, error) {
	return countUsers(ctx, t.q)
}

func countUsers(ctx context.Context, q *sqlc.Queries) (int64, error) {
	n, err := q.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count users: %w", mapError(err))
	}
	return n, nil
}

// CreateUser inserts a new user row and returns the stored record.
func (s *Store) CreateUser(ctx context.Context, p CreateUserParams) (User, error) {
	return createUser(ctx, s.writeQ, p)
}

// CreateUser inserts a new user row inside tx and returns the stored record.
func (t *Tx) CreateUser(ctx context.Context, p CreateUserParams) (User, error) {
	return createUser(ctx, t.q, p)
}

func createUser(ctx context.Context, q *sqlc.Queries, p CreateUserParams) (User, error) {
	nowMs := p.Now.UTC().UnixMilli()
	row, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		Email:             p.Email,
		Name:              p.Name,
		PasswordHash:      toNullString(p.PasswordHash),
		IsAdmin:           boolToInt64(p.IsAdmin),
		CanCreateProjects: boolToInt64(p.CanCreateProjects),
		DisabledAt:        toNullTimeMs(p.DisabledAt),
		LastLoginAt:       toNullTimeMs(p.LastLoginAt),
		CreatedAt:         nowMs,
		UpdatedAt:         nowMs,
	})
	if err != nil {
		return User{}, fmt.Errorf("store: create user %q: %w", p.Email, mapError(err))
	}
	return userFromRow(row), nil
}

// GetUserByID fetches a user by primary key.
func (s *Store) GetUserByID(ctx context.Context, id int64) (User, error) {
	return getUserByID(ctx, s.readQ, id)
}

// GetUserByID fetches a user by primary key inside tx.
func (t *Tx) GetUserByID(ctx context.Context, id int64) (User, error) {
	return getUserByID(ctx, t.q, id)
}

func getUserByID(ctx context.Context, q *sqlc.Queries, id int64) (User, error) {
	row, err := q.GetUserByID(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("store: get user %d: %w", id, mapError(err))
	}
	return userFromRow(row), nil
}

// GetUserByEmail fetches a user by email (case-insensitive via COLLATE NOCASE).
func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return getUserByEmail(ctx, s.readQ, email)
}

// GetUserByEmail fetches a user by email inside tx.
func (t *Tx) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return getUserByEmail(ctx, t.q, email)
}

func getUserByEmail(ctx context.Context, q *sqlc.Queries, email string) (User, error) {
	row, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		return User{}, fmt.Errorf("store: get user by email: %w", mapError(err))
	}
	return userFromRow(row), nil
}

// UpdateUserName updates a user's display name and returns the updated record.
func (s *Store) UpdateUserName(ctx context.Context, id int64, name string, now time.Time) (User, error) {
	return updateUserName(ctx, s.writeQ, id, name, now)
}

// UpdateUserName updates a user's display name inside tx.
func (t *Tx) UpdateUserName(ctx context.Context, id int64, name string, now time.Time) (User, error) {
	return updateUserName(ctx, t.q, id, name, now)
}

func updateUserName(ctx context.Context, q *sqlc.Queries, id int64, name string, now time.Time) (User, error) {
	row, err := q.UpdateUserName(ctx, sqlc.UpdateUserNameParams{
		Name:      name,
		UpdatedAt: now.UTC().UnixMilli(),
		ID:        id,
	})
	if err != nil {
		return User{}, fmt.Errorf("store: update user %d name: %w", id, mapError(err))
	}
	return userFromRow(row), nil
}

// UpdateUserPassword sets a user's password_hash.
func (s *Store) UpdateUserPassword(ctx context.Context, id int64, passwordHash string, now time.Time) error {
	return updateUserPassword(ctx, s.writeQ, id, passwordHash, now)
}

// UpdateUserPassword sets a user's password_hash inside tx.
func (t *Tx) UpdateUserPassword(ctx context.Context, id int64, passwordHash string, now time.Time) error {
	return updateUserPassword(ctx, t.q, id, passwordHash, now)
}

func updateUserPassword(ctx context.Context, q *sqlc.Queries, id int64, passwordHash string, now time.Time) error {
	affected, err := q.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{
		PasswordHash: sql.NullString{String: passwordHash, Valid: true},
		UpdatedAt:    now.UTC().UnixMilli(),
		ID:           id,
	})
	if err != nil {
		return fmt.Errorf("store: update user %d password: %w", id, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: update user %d password: %w", id, ErrNotFound)
	}
	return nil
}

// UpdateUserLastLogin updates a user's last_login_at timestamp.
func (s *Store) UpdateUserLastLogin(ctx context.Context, id int64, now time.Time) error {
	return updateUserLastLogin(ctx, s.writeQ, id, now)
}

// UpdateUserLastLogin updates a user's last_login_at timestamp inside tx.
func (t *Tx) UpdateUserLastLogin(ctx context.Context, id int64, now time.Time) error {
	return updateUserLastLogin(ctx, t.q, id, now)
}

func updateUserLastLogin(ctx context.Context, q *sqlc.Queries, id int64, now time.Time) error {
	nowMs := now.UTC().UnixMilli()
	if err := q.UpdateUserLastLogin(ctx, sqlc.UpdateUserLastLoginParams{
		LastLoginAt: sql.NullInt64{Int64: nowMs, Valid: true},
		UpdatedAt:   nowMs,
		ID:          id,
	}); err != nil {
		return fmt.Errorf("store: update user %d last login: %w", id, mapError(err))
	}
	return nil
}

// ListUsersAdmin returns a page of users ordered by ID ascending with optional
// case-insensitive substring matching on email or name (SPEC FR-100).
func (s *Store) ListUsersAdmin(ctx context.Context, afterID int64, query string, limit int64) ([]AdminUser, error) {
	rows, err := s.readQ.ListUsersAdmin(ctx, sqlc.ListUsersAdminParams{
		AfterID:    afterID,
		QueryLower: strings.ToLower(strings.TrimSpace(query)),
		PageLimit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list users admin: %w", mapError(err))
	}
	out := make([]AdminUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, AdminUser{
			User: User{
				ID:                r.ID,
				Email:             r.Email,
				Name:              r.Name,
				PasswordHash:      fromNullString(r.PasswordHash),
				IsAdmin:           r.IsAdmin != 0,
				CanCreateProjects: r.CanCreateProjects != 0,
				DisabledAt:        fromNullTimeMs(r.DisabledAt),
				LastLoginAt:       fromNullTimeMs(r.LastLoginAt),
				CreatedAt:         time.UnixMilli(r.CreatedAt).UTC(),
				UpdatedAt:         time.UnixMilli(r.UpdatedAt).UTC(),
			},
			ProjectCount: r.ProjectCount,
		})
	}
	return out, nil
}

// GetUserWithProjectCount returns a single user and their project membership count.
func (s *Store) GetUserWithProjectCount(ctx context.Context, id int64) (AdminUser, error) {
	return getUserWithProjectCount(ctx, s.readQ, id)
}

// GetUserWithProjectCount returns a single user and their project membership count inside tx.
func (t *Tx) GetUserWithProjectCount(ctx context.Context, id int64) (AdminUser, error) {
	return getUserWithProjectCount(ctx, t.q, id)
}

func getUserWithProjectCount(ctx context.Context, q *sqlc.Queries, id int64) (AdminUser, error) {
	r, err := q.GetUserWithProjectCount(ctx, id)
	if err != nil {
		return AdminUser{}, fmt.Errorf("store: get user %d with project count: %w", id, mapError(err))
	}
	return AdminUser{
		User: User{
			ID:                r.ID,
			Email:             r.Email,
			Name:              r.Name,
			PasswordHash:      fromNullString(r.PasswordHash),
			IsAdmin:           r.IsAdmin != 0,
			CanCreateProjects: r.CanCreateProjects != 0,
			DisabledAt:        fromNullTimeMs(r.DisabledAt),
			LastLoginAt:       fromNullTimeMs(r.LastLoginAt),
			CreatedAt:         time.UnixMilli(r.CreatedAt).UTC(),
			UpdatedAt:         time.UnixMilli(r.UpdatedAt).UTC(),
		},
		ProjectCount: r.ProjectCount,
	}, nil
}

// UpdateUserAdmin updates an account's admin-managed attributes inside tx.
func (t *Tx) UpdateUserAdmin(ctx context.Context, p UpdateUserAdminParams) (User, error) {
	row, err := t.q.UpdateUserAdmin(ctx, sqlc.UpdateUserAdminParams{
		Name:              p.Name,
		IsAdmin:           boolToInt64(p.IsAdmin),
		CanCreateProjects: boolToInt64(p.CanCreateProjects),
		DisabledAt:        toNullTimeMs(p.DisabledAt),
		UpdatedAt:         p.Now.UTC().UnixMilli(),
		ID:                p.ID,
	})
	if err != nil {
		return User{}, fmt.Errorf("store: update user %d admin: %w", p.ID, mapError(err))
	}
	return userFromRow(row), nil
}

// DeleteUser removes a user account inside tx.
func (t *Tx) DeleteUser(ctx context.Context, id int64) error {
	affected, err := t.q.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("store: delete user %d: %w", id, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete user %d: %w", id, ErrNotFound)
	}
	return nil
}

// CountAdmins returns the total number of admin accounts inside tx.
func (t *Tx) CountAdmins(ctx context.Context) (int64, error) {
	n, err := t.q.CountAdmins(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count admins: %w", mapError(err))
	}
	return n, nil
}

// CountActiveAdmins returns the number of enabled admin accounts inside tx.
func (t *Tx) CountActiveAdmins(ctx context.Context) (int64, error) {
	n, err := t.q.CountActiveAdmins(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count active admins: %w", mapError(err))
	}
	return n, nil
}

// RevokeAllAPITokensForUser revokes every non-revoked API token owned by userID inside tx (SPEC FR-044).
func (t *Tx) RevokeAllAPITokensForUser(ctx context.Context, userID int64, now time.Time) (int64, error) {
	n, err := t.q.RevokeAllAPITokensForUser(ctx, sqlc.RevokeAllAPITokensForUserParams{
		RevokedAt: sql.NullInt64{Int64: now.UTC().UnixMilli(), Valid: true},
		UserID:    userID,
	})
	if err != nil {
		return 0, fmt.Errorf("store: revoke api tokens for user %d: %w", userID, mapError(err))
	}
	return n, nil
}

func userFromRow(r sqlc.User) User {
	return User{
		ID:                r.ID,
		Email:             r.Email,
		Name:              r.Name,
		PasswordHash:      fromNullString(r.PasswordHash),
		IsAdmin:           r.IsAdmin != 0,
		CanCreateProjects: r.CanCreateProjects != 0,
		DisabledAt:        fromNullTimeMs(r.DisabledAt),
		LastLoginAt:       fromNullTimeMs(r.LastLoginAt),
		CreatedAt:         time.UnixMilli(r.CreatedAt).UTC(),
		UpdatedAt:         time.UnixMilli(r.UpdatedAt).UTC(),
	}
}

func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func fromNullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func toNullTimeMs(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UTC().UnixMilli(), Valid: true}
}

func fromNullTimeMs(ni sql.NullInt64) *time.Time {
	if !ni.Valid {
		return nil
	}
	t := time.UnixMilli(ni.Int64).UTC()
	return &t
}

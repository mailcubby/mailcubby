package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// SMTPCredential represents a per-project SMTP credential stored in smtp_credentials (SPEC FR-060).
type SMTPCredential struct {
	ID          int64
	ProjectID   int64
	Label       string
	Username    string
	PasswordEnc []byte
	CreatedBy   *int64
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	DisabledAt  *time.Time
}

// SMTPCredentialAuthLookup holds the credential and target project fields needed by SMTP AUTH.
type SMTPCredentialAuthLookup struct {
	Credential             SMTPCredential
	ProjectSlug            string
	ProjectPlusAddressTags bool
}

// CreateSMTPCredentialParams specifies fields for creating a new SMTP credential.
type CreateSMTPCredentialParams struct {
	ProjectID   int64
	Label       string
	Username    string
	PasswordEnc []byte
	CreatedBy   *int64
	Now         time.Time
}

// CreateSMTPCredential inserts a new SMTP credential scoped to ProjectID.
func (s *Store) CreateSMTPCredential(ctx context.Context, p CreateSMTPCredentialParams) (SMTPCredential, error) {
	return createSMTPCredential(ctx, s.writeQ, p)
}

// CreateSMTPCredential inserts a new SMTP credential scoped to ProjectID inside tx.
func (t *Tx) CreateSMTPCredential(ctx context.Context, p CreateSMTPCredentialParams) (SMTPCredential, error) {
	return createSMTPCredential(ctx, t.q, p)
}

func createSMTPCredential(ctx context.Context, q *sqlc.Queries, p CreateSMTPCredentialParams) (SMTPCredential, error) {
	row, err := q.CreateSMTPCredential(ctx, sqlc.CreateSMTPCredentialParams{
		ProjectID:   p.ProjectID,
		Label:       p.Label,
		Username:    p.Username,
		PasswordEnc: append([]byte(nil), p.PasswordEnc...),
		CreatedBy:   nullInt64Ptr(p.CreatedBy),
		CreatedAt:   p.Now.UTC().UnixMilli(),
	})
	if err != nil {
		return SMTPCredential{}, fmt.Errorf("store: create smtp credential in project %d: %w", p.ProjectID, mapError(err))
	}
	return smtpCredentialFromRow(row), nil
}

// CountSMTPCredentialsByProject returns the number of SMTP credentials belonging to projectID.
func (s *Store) CountSMTPCredentialsByProject(ctx context.Context, projectID int64) (int64, error) {
	return countSMTPCredentialsByProject(ctx, s.readQ, projectID)
}

// CountSMTPCredentialsByProject returns the number of SMTP credentials belonging to projectID inside tx.
func (t *Tx) CountSMTPCredentialsByProject(ctx context.Context, projectID int64) (int64, error) {
	return countSMTPCredentialsByProject(ctx, t.q, projectID)
}

func countSMTPCredentialsByProject(ctx context.Context, q *sqlc.Queries, projectID int64) (int64, error) {
	n, err := q.CountSMTPCredentialsByProject(ctx, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: count smtp credentials in project %d: %w", projectID, mapError(err))
	}
	return n, nil
}

// ListSMTPCredentialsByProject returns all SMTP credentials for projectID ordered by creation time.
func (s *Store) ListSMTPCredentialsByProject(ctx context.Context, projectID int64) ([]SMTPCredential, error) {
	rows, err := s.readQ.ListSMTPCredentialsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list smtp credentials in project %d: %w", projectID, mapError(err))
	}
	out := make([]SMTPCredential, 0, len(rows))
	for _, r := range rows {
		out = append(out, smtpCredentialFromRow(r))
	}
	return out, nil
}

// GetSMTPCredentialByID fetches a single SMTP credential scoped strictly to projectID.
func (s *Store) GetSMTPCredentialByID(ctx context.Context, projectID, id int64) (SMTPCredential, error) {
	return getSMTPCredentialByID(ctx, s.readQ, projectID, id)
}

// GetSMTPCredentialByID fetches a single SMTP credential scoped strictly to projectID inside tx.
func (t *Tx) GetSMTPCredentialByID(ctx context.Context, projectID, id int64) (SMTPCredential, error) {
	return getSMTPCredentialByID(ctx, t.q, projectID, id)
}

func getSMTPCredentialByID(ctx context.Context, q *sqlc.Queries, projectID, id int64) (SMTPCredential, error) {
	row, err := q.GetSMTPCredentialByID(ctx, sqlc.GetSMTPCredentialByIDParams{
		ProjectID: projectID,
		ID:        id,
	})
	if err != nil {
		return SMTPCredential{}, fmt.Errorf("store: get smtp credential %d in project %d: %w", id, projectID, mapError(err))
	}
	return smtpCredentialFromRow(row), nil
}

// UpdateSMTPCredential updates label and disabled_at for a credential scoped to projectID.
func (s *Store) UpdateSMTPCredential(ctx context.Context, projectID, id int64, label string, disabledAt *time.Time) (SMTPCredential, error) {
	return updateSMTPCredential(ctx, s.writeQ, projectID, id, label, disabledAt)
}

// UpdateSMTPCredential updates label and disabled_at for a credential scoped to projectID inside tx.
func (t *Tx) UpdateSMTPCredential(ctx context.Context, projectID, id int64, label string, disabledAt *time.Time) (SMTPCredential, error) {
	return updateSMTPCredential(ctx, t.q, projectID, id, label, disabledAt)
}

func updateSMTPCredential(ctx context.Context, q *sqlc.Queries, projectID, id int64, label string, disabledAt *time.Time) (SMTPCredential, error) {
	row, err := q.UpdateSMTPCredential(ctx, sqlc.UpdateSMTPCredentialParams{
		Label:      label,
		DisabledAt: nullTimeMsPtr(disabledAt),
		ProjectID:  projectID,
		ID:         id,
	})
	if err != nil {
		return SMTPCredential{}, fmt.Errorf("store: update smtp credential %d in project %d: %w", id, projectID, mapError(err))
	}
	return smtpCredentialFromRow(row), nil
}

// UpdateSMTPCredentialPassword replaces password_enc for a credential scoped to projectID.
func (s *Store) UpdateSMTPCredentialPassword(ctx context.Context, projectID, id int64, passwordEnc []byte) (SMTPCredential, error) {
	return updateSMTPCredentialPassword(ctx, s.writeQ, projectID, id, passwordEnc)
}

// UpdateSMTPCredentialPassword replaces password_enc for a credential scoped to projectID inside tx.
func (t *Tx) UpdateSMTPCredentialPassword(ctx context.Context, projectID, id int64, passwordEnc []byte) (SMTPCredential, error) {
	return updateSMTPCredentialPassword(ctx, t.q, projectID, id, passwordEnc)
}

func updateSMTPCredentialPassword(ctx context.Context, q *sqlc.Queries, projectID, id int64, passwordEnc []byte) (SMTPCredential, error) {
	row, err := q.UpdateSMTPCredentialPassword(ctx, sqlc.UpdateSMTPCredentialPasswordParams{
		PasswordEnc: append([]byte(nil), passwordEnc...),
		ProjectID:   projectID,
		ID:          id,
	})
	if err != nil {
		return SMTPCredential{}, fmt.Errorf("store: rotate smtp credential %d in project %d: %w", id, projectID, mapError(err))
	}
	return smtpCredentialFromRow(row), nil
}

// DeleteSMTPCredential deletes a credential scoped strictly to projectID.
func (s *Store) DeleteSMTPCredential(ctx context.Context, projectID, id int64) error {
	return deleteSMTPCredential(ctx, s.writeQ, projectID, id)
}

// DeleteSMTPCredential deletes a credential scoped strictly to projectID inside tx.
func (t *Tx) DeleteSMTPCredential(ctx context.Context, projectID, id int64) error {
	return deleteSMTPCredential(ctx, t.q, projectID, id)
}

func deleteSMTPCredential(ctx context.Context, q *sqlc.Queries, projectID, id int64) error {
	affected, err := q.DeleteSMTPCredential(ctx, sqlc.DeleteSMTPCredentialParams{
		ProjectID: projectID,
		ID:        id,
	})
	if err != nil {
		return fmt.Errorf("store: delete smtp credential %d in project %d: %w", id, projectID, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete smtp credential %d in project %d: %w", id, projectID, ErrNotFound)
	}
	return nil
}

// TouchSMTPCredentialLastUsed updates last_used_at for a credential scoped to projectID.
func (s *Store) TouchSMTPCredentialLastUsed(ctx context.Context, projectID, id int64, now time.Time) error {
	err := s.writeQ.TouchSMTPCredentialLastUsed(ctx, sqlc.TouchSMTPCredentialLastUsedParams{
		LastUsedAt: sql.NullInt64{Int64: now.UTC().UnixMilli(), Valid: true},
		ProjectID:  projectID,
		ID:         id,
	})
	if err != nil {
		return fmt.Errorf("store: touch smtp credential %d in project %d: %w", id, projectID, mapError(err))
	}
	return nil
}

// LookupSMTPCredentialByUsername resolves a credential and its project by unique SMTP username.
func (s *Store) LookupSMTPCredentialByUsername(ctx context.Context, username string) (SMTPCredentialAuthLookup, error) {
	row, err := s.readQ.LookupSMTPCredentialByUsername(ctx, username)
	if err != nil {
		return SMTPCredentialAuthLookup{}, fmt.Errorf("store: lookup smtp credential: %w", mapError(err))
	}
	return SMTPCredentialAuthLookup{
		Credential: SMTPCredential{
			ID:          row.ID,
			ProjectID:   row.ProjectID,
			Label:       row.Label,
			Username:    row.Username,
			PasswordEnc: append([]byte(nil), row.PasswordEnc...),
			CreatedBy:   int64PtrFromNull(row.CreatedBy),
			CreatedAt:   time.UnixMilli(row.CreatedAt).UTC(),
			LastUsedAt:  timePtrFromNullMs(row.LastUsedAt),
			DisabledAt:  timePtrFromNullMs(row.DisabledAt),
		},
		ProjectSlug:            row.ProjectSlug,
		ProjectPlusAddressTags: row.ProjectPlusAddressTags != 0,
	}, nil
}

func smtpCredentialFromRow(r sqlc.SmtpCredential) SMTPCredential {
	return SMTPCredential{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		Label:       r.Label,
		Username:    r.Username,
		PasswordEnc: append([]byte(nil), r.PasswordEnc...),
		CreatedBy:   int64PtrFromNull(r.CreatedBy),
		CreatedAt:   time.UnixMilli(r.CreatedAt).UTC(),
		LastUsedAt:  timePtrFromNullMs(r.LastUsedAt),
		DisabledAt:  timePtrFromNullMs(r.DisabledAt),
	}
}

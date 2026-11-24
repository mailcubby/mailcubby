package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// AuditActorType identifies who performed an audited action (SPEC 10).
type AuditActorType string

const (
	// AuditActorUser indicates a browser-session user.
	AuditActorUser AuditActorType = "user"
	// AuditActorToken indicates an API bearer token.
	AuditActorToken AuditActorType = "token"
	// AuditActorSystem indicates Mailcubby itself (CLI, startup bootstrap, anonymous auth attempt).
	AuditActorSystem AuditActorType = "system"
	// AuditActorSMTP indicates an SMTP client event (e.g. auth lockout).
	AuditActorSMTP AuditActorType = "smtp"
	// AuditActorLocal indicates the local-mode operator.
	AuditActorLocal AuditActorType = "local"
)

// AuditEntry represents one row in audit_log (SPEC FR-105).
type AuditEntry struct {
	ID           int64
	At           time.Time
	ActorType    AuditActorType
	ActorUserID  *int64
	ActorLabel   *string
	ProjectID    *int64
	ProjectSlug  *string
	Action       string
	TargetType   *string
	TargetID     *string
	IP           *string
	MetadataJSON *string
}

// ListProjectAuditParams filters and paginates project-scoped audit log entries.
type ListProjectAuditParams struct {
	ProjectID int64
	BeforeID  int64
	Action    string
	Limit     int64
}

// ListAdminAuditParams filters and paginates instance-wide audit log entries.
type ListAdminAuditParams struct {
	BeforeID  int64
	Action    string
	ProjectID int64
	UserID    int64
	UserLabel string
	Limit     int64
}

// InsertAuditParams specifies inputs for writing an audit log entry.
// Metadata must never contain passwords, tokens, or secrets (SPEC 12.3).
type InsertAuditParams struct {
	At          time.Time
	ActorType   AuditActorType
	ActorUserID *int64
	ActorLabel  *string
	ProjectID   *int64
	Action      string
	TargetType  *string
	TargetID    *string
	IP          *string
	Metadata    map[string]any
}

// InsertAudit appends a single entry to audit_log.
func (s *Store) InsertAudit(ctx context.Context, p InsertAuditParams) (AuditEntry, error) {
	return insertAudit(ctx, s.writeQ, p)
}

// InsertAudit appends a single entry to audit_log inside tx.
func (t *Tx) InsertAudit(ctx context.Context, p InsertAuditParams) (AuditEntry, error) {
	return insertAudit(ctx, t.q, p)
}

func insertAudit(ctx context.Context, q *sqlc.Queries, p InsertAuditParams) (AuditEntry, error) {
	var meta sql.NullString
	if len(p.Metadata) > 0 {
		b, err := json.Marshal(p.Metadata)
		if err != nil {
			return AuditEntry{}, fmt.Errorf("store: marshal audit metadata: %w", err)
		}
		meta = sql.NullString{String: string(b), Valid: true}
	}

	row, err := q.InsertAudit(ctx, sqlc.InsertAuditParams{
		At:           p.At.UTC().UnixMilli(),
		ActorType:    string(p.ActorType),
		ActorUserID:  nullInt64Ptr(p.ActorUserID),
		ActorLabel:   toNullString(p.ActorLabel),
		ProjectID:    nullInt64Ptr(p.ProjectID),
		Action:       p.Action,
		TargetType:   toNullString(p.TargetType),
		TargetID:     toNullString(p.TargetID),
		Ip:           toNullString(p.IP),
		MetadataJson: meta,
	})
	if err != nil {
		return AuditEntry{}, fmt.Errorf("store: insert audit %q: %w", p.Action, mapError(err))
	}
	return auditFromRow(row), nil
}

// ListAuditEntries returns the newest audit entries up to limit.
func (s *Store) ListAuditEntries(ctx context.Context, limit int64) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.readQ.ListAuditEntries(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list audit entries: %w", mapError(err))
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditFromRow(r))
	}
	return out, nil
}

// ListProjectAudit returns paginated audit entries scoped to p.ProjectID, newest first.
func (s *Store) ListProjectAudit(ctx context.Context, p ListProjectAuditParams) ([]AuditEntry, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.readQ.ListProjectAudit(ctx, sqlc.ListProjectAuditParams{
		ProjectID:    sql.NullInt64{Int64: p.ProjectID, Valid: true},
		BeforeID:     p.BeforeID,
		ActionFilter: strings.TrimSpace(p.Action),
		PageLimit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list project audit: %w", mapError(err))
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditEntry{
			ID:           r.ID,
			At:           time.UnixMilli(r.At).UTC(),
			ActorType:    AuditActorType(r.ActorType),
			ActorUserID:  int64PtrFromNull(r.ActorUserID),
			ActorLabel:   fromNullString(r.ActorLabel),
			ProjectID:    int64PtrFromNull(r.ProjectID),
			ProjectSlug:  fromNullString(r.ProjectSlug),
			Action:       r.Action,
			TargetType:   fromNullString(r.TargetType),
			TargetID:     fromNullString(r.TargetID),
			IP:           fromNullString(r.Ip),
			MetadataJSON: fromNullString(r.MetadataJson),
		})
	}
	return out, nil
}

// ListAdminAudit returns paginated, filtered audit entries across the instance, newest first.
func (s *Store) ListAdminAudit(ctx context.Context, p ListAdminAuditParams) ([]AuditEntry, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.readQ.ListAdminAudit(ctx, sqlc.ListAdminAuditParams{
		BeforeID:        p.BeforeID,
		ActionFilter:    strings.TrimSpace(p.Action),
		ProjectIDFilter: p.ProjectID,
		UserIDFilter:    p.UserID,
		UserLabelLower:  strings.ToLower(strings.TrimSpace(p.UserLabel)),
		PageLimit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list admin audit: %w", mapError(err))
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditEntry{
			ID:           r.ID,
			At:           time.UnixMilli(r.At).UTC(),
			ActorType:    AuditActorType(r.ActorType),
			ActorUserID:  int64PtrFromNull(r.ActorUserID),
			ActorLabel:   fromNullString(r.ActorLabel),
			ProjectID:    int64PtrFromNull(r.ProjectID),
			ProjectSlug:  fromNullString(r.ProjectSlug),
			Action:       r.Action,
			TargetType:   fromNullString(r.TargetType),
			TargetID:     fromNullString(r.TargetID),
			IP:           fromNullString(r.Ip),
			MetadataJSON: fromNullString(r.MetadataJson),
		})
	}
	return out, nil
}

func auditFromRow(r sqlc.AuditLog) AuditEntry {
	return AuditEntry{
		ID:           r.ID,
		At:           time.UnixMilli(r.At).UTC(),
		ActorType:    AuditActorType(r.ActorType),
		ActorUserID:  int64PtrFromNull(r.ActorUserID),
		ActorLabel:   fromNullString(r.ActorLabel),
		ProjectID:    int64PtrFromNull(r.ProjectID),
		Action:       r.Action,
		TargetType:   fromNullString(r.TargetType),
		TargetID:     fromNullString(r.TargetID),
		IP:           fromNullString(r.Ip),
		MetadataJSON: fromNullString(r.MetadataJson),
	}
}

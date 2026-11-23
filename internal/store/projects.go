package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// ProjectSettings holds per-project configurable flags and retention overrides.
type ProjectSettings struct {
	RetentionMaxAgeSec      *int64
	RetentionMaxMessages    *int64
	RetentionMaxBytes       *int64
	RecipientRoutingEnabled bool
	PlusAddressTags         bool
	ViewerHideTechnical     bool
	MembersCanRevealSMTP    bool
}

// DefaultProjectSettings returns the default settings for a newly created project (SPEC 10).
func DefaultProjectSettings() ProjectSettings {
	return ProjectSettings{
		RecipientRoutingEnabled: false,
		PlusAddressTags:         true,
		ViewerHideTechnical:     true,
		MembersCanRevealSMTP:    false,
	}
}

// Project represents a Mailcubby project and its maintained counters.
type Project struct {
	ID            int64
	Slug          string
	Name          string
	Color         string
	Settings      ProjectSettings
	MessageCount  int64
	UnreadCount   int64
	SizeBytes     int64
	LastMessageAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateProjectParams specifies the inputs for creating a project.
type CreateProjectParams struct {
	Slug     string
	Name     string
	Color    string
	Settings ProjectSettings
	Now      time.Time
}

// UpdateProjectParams specifies the inputs for updating a project.
type UpdateProjectParams struct {
	ID       int64
	Slug     string
	Name     string
	Color    string
	Settings ProjectSettings
	Now      time.Time
}

// ProjectCounterDelta specifies incremental counter updates for a project.
type ProjectCounterDelta struct {
	MessageDelta  int64
	UnreadDelta   int64
	SizeDelta     int64
	LastMessageAt *time.Time
	Now           time.Time
}

// CreateProject inserts a new project and returns the stored record.
func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (Project, error) {
	return createProject(ctx, s.writeQ, p)
}

// CreateProject inserts a new project inside tx and returns the stored record.
func (t *Tx) CreateProject(ctx context.Context, p CreateProjectParams) (Project, error) {
	return createProject(ctx, t.q, p)
}

func createProject(ctx context.Context, q *sqlc.Queries, p CreateProjectParams) (Project, error) {
	nowMs := p.Now.UTC().UnixMilli()
	row, err := q.CreateProject(ctx, sqlc.CreateProjectParams{
		Slug:                    p.Slug,
		Name:                    p.Name,
		Color:                   p.Color,
		RetentionMaxAgeS:        nullInt64Ptr(p.Settings.RetentionMaxAgeSec),
		RetentionMaxMessages:    nullInt64Ptr(p.Settings.RetentionMaxMessages),
		RetentionMaxBytes:       nullInt64Ptr(p.Settings.RetentionMaxBytes),
		RecipientRoutingEnabled: boolToInt64(p.Settings.RecipientRoutingEnabled),
		PlusAddressTags:         boolToInt64(p.Settings.PlusAddressTags),
		ViewerHideTechnical:     boolToInt64(p.Settings.ViewerHideTechnical),
		MembersCanRevealSmtp:    boolToInt64(p.Settings.MembersCanRevealSMTP),
		CreatedAt:               nowMs,
		UpdatedAt:               nowMs,
	})
	if err != nil {
		return Project{}, fmt.Errorf("store: create project %q: %w", p.Slug, mapError(err))
	}
	return projectFromRow(row), nil
}

// GetProjectByID fetches a project by its primary key.
func (s *Store) GetProjectByID(ctx context.Context, id int64) (Project, error) {
	return getProjectByID(ctx, s.readQ, id)
}

// GetProjectByID fetches a project by its primary key inside tx.
func (t *Tx) GetProjectByID(ctx context.Context, id int64) (Project, error) {
	return getProjectByID(ctx, t.q, id)
}

func getProjectByID(ctx context.Context, q *sqlc.Queries, id int64) (Project, error) {
	row, err := q.GetProjectByID(ctx, id)
	if err != nil {
		return Project{}, fmt.Errorf("store: get project %d: %w", id, mapError(err))
	}
	return projectFromRow(row), nil
}

// GetProjectBySlug fetches a project by its unique slug.
func (s *Store) GetProjectBySlug(ctx context.Context, slug string) (Project, error) {
	return getProjectBySlug(ctx, s.readQ, slug)
}

// GetProjectBySlug fetches a project by its unique slug inside tx.
func (t *Tx) GetProjectBySlug(ctx context.Context, slug string) (Project, error) {
	return getProjectBySlug(ctx, t.q, slug)
}

func getProjectBySlug(ctx context.Context, q *sqlc.Queries, slug string) (Project, error) {
	row, err := q.GetProjectBySlug(ctx, slug)
	if err != nil {
		return Project{}, fmt.Errorf("store: get project %q: %w", slug, mapError(err))
	}
	return projectFromRow(row), nil
}

// ListProjects returns all projects ordered by name ascending.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.readQ.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", mapError(err))
	}
	out := make([]Project, 0, len(rows))
	for _, r := range rows {
		out = append(out, projectFromRow(r))
	}
	return out, nil
}

// UpdateProject updates a project's metadata and settings.
func (s *Store) UpdateProject(ctx context.Context, p UpdateProjectParams) (Project, error) {
	return updateProject(ctx, s.writeQ, p)
}

// UpdateProject updates a project's metadata and settings inside tx.
func (t *Tx) UpdateProject(ctx context.Context, p UpdateProjectParams) (Project, error) {
	return updateProject(ctx, t.q, p)
}

func updateProject(ctx context.Context, q *sqlc.Queries, p UpdateProjectParams) (Project, error) {
	row, err := q.UpdateProject(ctx, sqlc.UpdateProjectParams{
		Slug:                    p.Slug,
		Name:                    p.Name,
		Color:                   p.Color,
		RetentionMaxAgeS:        nullInt64Ptr(p.Settings.RetentionMaxAgeSec),
		RetentionMaxMessages:    nullInt64Ptr(p.Settings.RetentionMaxMessages),
		RetentionMaxBytes:       nullInt64Ptr(p.Settings.RetentionMaxBytes),
		RecipientRoutingEnabled: boolToInt64(p.Settings.RecipientRoutingEnabled),
		PlusAddressTags:         boolToInt64(p.Settings.PlusAddressTags),
		ViewerHideTechnical:     boolToInt64(p.Settings.ViewerHideTechnical),
		MembersCanRevealSmtp:    boolToInt64(p.Settings.MembersCanRevealSMTP),
		UpdatedAt:               p.Now.UTC().UnixMilli(),
		ID:                      p.ID,
	})
	if err != nil {
		return Project{}, fmt.Errorf("store: update project %d: %w", p.ID, mapError(err))
	}
	return projectFromRow(row), nil
}

// DeleteProject deletes a project by ID, returning ErrNotFound if it does not exist.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	return deleteProject(ctx, s.writeQ, id)
}

// DeleteProject deletes a project by ID inside tx.
func (t *Tx) DeleteProject(ctx context.Context, id int64) error {
	return deleteProject(ctx, t.q, id)
}

func deleteProject(ctx context.Context, q *sqlc.Queries, id int64) error {
	affected, err := q.DeleteProject(ctx, id)
	if err != nil {
		return fmt.Errorf("store: delete project %d: %w", id, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete project %d: %w", id, ErrNotFound)
	}
	return nil
}

// UpdateProjectCounters atomically adjusts a project's message, unread, and byte counters.
func (s *Store) UpdateProjectCounters(ctx context.Context, projectID int64, d ProjectCounterDelta) error {
	return updateProjectCounters(ctx, s.writeQ, projectID, d)
}

// UpdateProjectCounters atomically adjusts a project's counters inside tx.
func (t *Tx) UpdateProjectCounters(ctx context.Context, projectID int64, d ProjectCounterDelta) error {
	return updateProjectCounters(ctx, t.q, projectID, d)
}

func updateProjectCounters(ctx context.Context, q *sqlc.Queries, projectID int64, d ProjectCounterDelta) error {
	var lastMsg sql.NullInt64
	if d.LastMessageAt != nil {
		lastMsg = sql.NullInt64{Int64: d.LastMessageAt.UTC().UnixMilli(), Valid: true}
	}
	affected, err := q.UpdateProjectCounters(ctx, sqlc.UpdateProjectCountersParams{
		MessageDelta:  d.MessageDelta,
		UnreadDelta:   d.UnreadDelta,
		SizeDelta:     d.SizeDelta,
		LastMessageAt: lastMsg,
		UpdatedAt:     d.Now.UTC().UnixMilli(),
		ID:            projectID,
	})
	if err != nil {
		return fmt.Errorf("store: update project %d counters: %w", projectID, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: update project %d counters: %w", projectID, ErrNotFound)
	}
	return nil
}

func projectFromRow(r sqlc.Project) Project {
	return Project{
		ID:    r.ID,
		Slug:  r.Slug,
		Name:  r.Name,
		Color: r.Color,
		Settings: ProjectSettings{
			RetentionMaxAgeSec:      int64PtrFromNull(r.RetentionMaxAgeS),
			RetentionMaxMessages:    int64PtrFromNull(r.RetentionMaxMessages),
			RetentionMaxBytes:       int64PtrFromNull(r.RetentionMaxBytes),
			RecipientRoutingEnabled: r.RecipientRoutingEnabled != 0,
			PlusAddressTags:         r.PlusAddressTags != 0,
			ViewerHideTechnical:     r.ViewerHideTechnical != 0,
			MembersCanRevealSMTP:    r.MembersCanRevealSmtp != 0,
		},
		MessageCount:  r.MessageCount,
		UnreadCount:   r.UnreadCount,
		SizeBytes:     r.SizeBytes,
		LastMessageAt: timePtrFromNullMs(r.LastMessageAt),
		CreatedAt:     time.UnixMilli(r.CreatedAt).UTC(),
		UpdatedAt:     time.UnixMilli(r.UpdatedAt).UTC(),
	}
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullInt64Ptr(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func int64PtrFromNull(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func timePtrFromNullMs(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	ts := time.UnixMilli(v.Int64).UTC()
	return &ts
}

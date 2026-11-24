package store

import (
	"context"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// ProjectMember represents a user's membership and role within a project (SPEC 6.5).
type ProjectMember struct {
	ProjectID int64
	UserID    int64
	UserEmail string
	UserName  string
	Role      string
	CreatedAt time.Time
}

// UserMembership summarizes one project membership owned by a user (for GET /auth/me).
type UserMembership struct {
	ProjectID   int64
	ProjectSlug string
	Role        string
	CreatedAt   time.Time
}

// ProjectWithRole pairs a Project with the caller's membership role (for GET /projects).
type ProjectWithRole struct {
	Project Project
	Role    string
}

// ProjectOwner identifies an owner user of a project (for GET /admin/projects).
type ProjectOwner struct {
	ProjectID int64
	UserID    int64
	Email     string
	Name      string
}

// AddProjectMember adds userID to projectID with role.
func (s *Store) AddProjectMember(ctx context.Context, projectID, userID int64, role string, now time.Time) (ProjectMember, error) {
	return addProjectMember(ctx, s.writeQ, projectID, userID, role, now)
}

// AddProjectMember adds userID to projectID with role inside tx.
func (t *Tx) AddProjectMember(ctx context.Context, projectID, userID int64, role string, now time.Time) (ProjectMember, error) {
	return addProjectMember(ctx, t.q, projectID, userID, role, now)
}

func addProjectMember(ctx context.Context, q *sqlc.Queries, projectID, userID int64, role string, now time.Time) (ProjectMember, error) {
	if _, err := q.AddProjectMember(ctx, sqlc.AddProjectMemberParams{
		ProjectID: projectID,
		UserID:    userID,
		Role:      role,
		CreatedAt: now.UTC().UnixMilli(),
	}); err != nil {
		return ProjectMember{}, fmt.Errorf("store: add project member (project=%d, user=%d): %w", projectID, userID, mapError(err))
	}
	return getProjectMember(ctx, q, projectID, userID)
}

// GetProjectMember returns the membership for (projectID, userID), or ErrNotFound.
func (s *Store) GetProjectMember(ctx context.Context, projectID, userID int64) (ProjectMember, error) {
	return getProjectMember(ctx, s.readQ, projectID, userID)
}

// GetProjectMember returns the membership for (projectID, userID) inside tx.
func (t *Tx) GetProjectMember(ctx context.Context, projectID, userID int64) (ProjectMember, error) {
	return getProjectMember(ctx, t.q, projectID, userID)
}

func getProjectMember(ctx context.Context, q *sqlc.Queries, projectID, userID int64) (ProjectMember, error) {
	row, err := q.GetProjectMember(ctx, sqlc.GetProjectMemberParams{
		ProjectID: projectID,
		UserID:    userID,
	})
	if err != nil {
		return ProjectMember{}, fmt.Errorf("store: get project member (project=%d, user=%d): %w", projectID, userID, mapError(err))
	}
	return ProjectMember{
		ProjectID: row.ProjectID,
		UserID:    row.UserID,
		UserEmail: row.UserEmail,
		UserName:  row.UserName,
		Role:      row.Role,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(),
	}, nil
}

// ListProjectMembers returns all members of projectID ordered by creation time ascending.
func (s *Store) ListProjectMembers(ctx context.Context, projectID int64) ([]ProjectMember, error) {
	rows, err := s.readQ.ListProjectMembers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list project members (project=%d): %w", projectID, mapError(err))
	}
	out := make([]ProjectMember, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectMember{
			ProjectID: r.ProjectID,
			UserID:    r.UserID,
			UserEmail: r.UserEmail,
			UserName:  r.UserName,
			Role:      r.Role,
			CreatedAt: time.UnixMilli(r.CreatedAt).UTC(),
		})
	}
	return out, nil
}

// UpdateProjectMemberRole changes the role of (projectID, userID).
func (s *Store) UpdateProjectMemberRole(ctx context.Context, projectID, userID int64, role string) error {
	return updateProjectMemberRole(ctx, s.writeQ, projectID, userID, role)
}

// UpdateProjectMemberRole changes the role of (projectID, userID) inside tx.
func (t *Tx) UpdateProjectMemberRole(ctx context.Context, projectID, userID int64, role string) error {
	return updateProjectMemberRole(ctx, t.q, projectID, userID, role)
}

func updateProjectMemberRole(ctx context.Context, q *sqlc.Queries, projectID, userID int64, role string) error {
	affected, err := q.UpdateProjectMemberRole(ctx, sqlc.UpdateProjectMemberRoleParams{
		Role:      role,
		ProjectID: projectID,
		UserID:    userID,
	})
	if err != nil {
		return fmt.Errorf("store: update project member role (project=%d, user=%d): %w", projectID, userID, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: update project member role (project=%d, user=%d): %w", projectID, userID, ErrNotFound)
	}
	return nil
}

// DeleteProjectMember removes userID from projectID, returning ErrNotFound if not a member.
func (s *Store) DeleteProjectMember(ctx context.Context, projectID, userID int64) error {
	return deleteProjectMember(ctx, s.writeQ, projectID, userID)
}

// DeleteProjectMember removes userID from projectID inside tx.
func (t *Tx) DeleteProjectMember(ctx context.Context, projectID, userID int64) error {
	return deleteProjectMember(ctx, t.q, projectID, userID)
}

func deleteProjectMember(ctx context.Context, q *sqlc.Queries, projectID, userID int64) error {
	affected, err := q.DeleteProjectMember(ctx, sqlc.DeleteProjectMemberParams{
		ProjectID: projectID,
		UserID:    userID,
	})
	if err != nil {
		return fmt.Errorf("store: delete project member (project=%d, user=%d): %w", projectID, userID, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete project member (project=%d, user=%d): %w", projectID, userID, ErrNotFound)
	}
	return nil
}

// CountProjectOwners returns the number of explicit owners in projectID.
func (s *Store) CountProjectOwners(ctx context.Context, projectID int64) (int64, error) {
	return countProjectOwners(ctx, s.readQ, projectID)
}

// CountProjectOwners returns the number of explicit owners in projectID inside tx.
func (t *Tx) CountProjectOwners(ctx context.Context, projectID int64) (int64, error) {
	return countProjectOwners(ctx, t.q, projectID)
}

func countProjectOwners(ctx context.Context, q *sqlc.Queries, projectID int64) (int64, error) {
	n, err := q.CountProjectOwners(ctx, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: count project owners (project=%d): %w", projectID, mapError(err))
	}
	return n, nil
}

// ListMembershipsByUser returns all explicit project memberships for userID.
func (s *Store) ListMembershipsByUser(ctx context.Context, userID int64) ([]UserMembership, error) {
	rows, err := s.readQ.ListMembershipsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list memberships for user %d: %w", userID, mapError(err))
	}
	out := make([]UserMembership, 0, len(rows))
	for _, r := range rows {
		out = append(out, UserMembership{
			ProjectID:   r.ProjectID,
			ProjectSlug: r.ProjectSlug,
			Role:        r.Role,
			CreatedAt:   time.UnixMilli(r.CreatedAt).UTC(),
		})
	}
	return out, nil
}

// ListProjectsForUser returns all projects userID belongs to, along with their role.
func (s *Store) ListProjectsForUser(ctx context.Context, userID int64) ([]ProjectWithRole, error) {
	rows, err := s.readQ.ListProjectsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list projects for user %d: %w", userID, mapError(err))
	}
	out := make([]ProjectWithRole, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectWithRole{
			Project: Project{
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
			},
			Role: r.MemberRole,
		})
	}
	return out, nil
}

// ListProjectIDsForUser returns the IDs of all projects userID belongs to.
func (s *Store) ListProjectIDsForUser(ctx context.Context, userID int64) ([]int64, error) {
	ids, err := s.readQ.ListProjectIDsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list project ids for user %d: %w", userID, mapError(err))
	}
	return ids, nil
}

// ListAllProjectOwners returns every owner row across all projects, grouped for admin views.
func (s *Store) ListAllProjectOwners(ctx context.Context) (map[int64][]ProjectOwner, error) {
	rows, err := s.readQ.ListAllProjectOwners(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list all project owners: %w", mapError(err))
	}
	out := make(map[int64][]ProjectOwner)
	for _, r := range rows {
		out[r.ProjectID] = append(out[r.ProjectID], ProjectOwner{
			ProjectID: r.ProjectID,
			UserID:    r.UserID,
			Email:     r.UserEmail,
			Name:      r.UserName,
		})
	}
	return out, nil
}

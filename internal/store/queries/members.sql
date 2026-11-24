-- name: AddProjectMember :one
INSERT INTO project_members (
    project_id,
    user_id,
    role,
    created_at
) VALUES (?, ?, ?, ?)
RETURNING project_id, user_id, role, created_at;

-- name: GetProjectMember :one
SELECT
    pm.project_id,
    pm.user_id,
    pm.role,
    pm.created_at,
    u.email AS user_email,
    u.name AS user_name
FROM project_members pm
JOIN users u ON u.id = pm.user_id
WHERE pm.project_id = ? AND pm.user_id = ?;

-- name: ListProjectMembers :many
SELECT
    pm.project_id,
    pm.user_id,
    pm.role,
    pm.created_at,
    u.email AS user_email,
    u.name AS user_name
FROM project_members pm
JOIN users u ON u.id = pm.user_id
WHERE pm.project_id = ?
ORDER BY pm.created_at ASC, pm.user_id ASC;

-- name: UpdateProjectMemberRole :execrows
UPDATE project_members
SET role = ?
WHERE project_id = ? AND user_id = ?;

-- name: DeleteProjectMember :execrows
DELETE FROM project_members
WHERE project_id = ? AND user_id = ?;

-- name: CountProjectOwners :one
SELECT COUNT(*)
FROM project_members
WHERE project_id = ? AND role = 'owner';

-- name: ListMembershipsByUser :many
-- isolation: global (lists a single user's own project memberships for GET /auth/me)
SELECT
    pm.project_id,
    p.slug AS project_slug,
    pm.role,
    pm.created_at
FROM project_members pm
JOIN projects p ON p.id = pm.project_id
WHERE pm.user_id = ?
ORDER BY p.name ASC, p.id ASC;

-- name: ListProjectsForUser :many
-- isolation: global (lists projects accessible to a single user for GET /projects)
SELECT
    p.id,
    p.slug,
    p.name,
    p.color,
    p.retention_max_age_s,
    p.retention_max_messages,
    p.retention_max_bytes,
    p.recipient_routing_enabled,
    p.plus_address_tags,
    p.viewer_hide_technical,
    p.members_can_reveal_smtp,
    p.message_count,
    p.unread_count,
    p.size_bytes,
    p.last_message_at,
    p.created_at,
    p.updated_at,
    pm.role AS member_role
FROM projects p
JOIN project_members pm ON pm.project_id = p.id
WHERE pm.user_id = ?
ORDER BY p.name ASC, p.id ASC;

-- name: ListProjectIDsForUser :many
-- isolation: global (lists accessible project IDs for a single user for SSE filtering)
SELECT project_id
FROM project_members
WHERE user_id = ?
ORDER BY project_id ASC;

-- name: ListAllProjectOwners :many
-- isolation: global (admin all-projects listing displays owners per project)
SELECT
    pm.project_id,
    u.id AS user_id,
    u.email AS user_email,
    u.name AS user_name
FROM project_members pm
JOIN users u ON u.id = pm.user_id
WHERE pm.role = 'owner'
ORDER BY pm.project_id ASC, pm.created_at ASC, u.id ASC;

-- name: CountSoleOwnedProjectsForUser :one
-- isolation: global (prevents deleting a user who is the sole owner of any project)
SELECT COUNT(*)
FROM project_members pm
WHERE pm.user_id = ?
  AND pm.role = 'owner'
  AND NOT EXISTS (
    SELECT 1
    FROM project_members other
    WHERE other.project_id = pm.project_id
      AND other.role = 'owner'
      AND other.user_id != pm.user_id
  );

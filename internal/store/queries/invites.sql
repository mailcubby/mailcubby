-- name: CreateInvite :one
INSERT INTO invites (
    project_id,
    email,
    role,
    token_hash,
    created_by,
    created_at,
    expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING
    id,
    project_id,
    email,
    role,
    token_hash,
    created_by,
    created_at,
    expires_at,
    accepted_at,
    accepted_by,
    revoked_at;

-- name: GetProjectInviteByID :one
SELECT
    i.id,
    i.project_id,
    i.email,
    i.role,
    i.token_hash,
    i.created_by,
    i.created_at,
    i.expires_at,
    i.accepted_at,
    i.accepted_by,
    i.revoked_at,
    u.name AS created_by_name,
    u.email AS created_by_email
FROM invites i
LEFT JOIN users u ON u.id = i.created_by
WHERE i.project_id = ? AND i.id = ?;

-- name: ListProjectInvites :many
SELECT
    i.id,
    i.project_id,
    i.email,
    i.role,
    i.token_hash,
    i.created_by,
    i.created_at,
    i.expires_at,
    i.accepted_at,
    i.accepted_by,
    i.revoked_at,
    u.name AS created_by_name,
    u.email AS created_by_email
FROM invites i
LEFT JOIN users u ON u.id = i.created_by
WHERE i.project_id = ?
ORDER BY i.created_at DESC, i.id DESC;

-- name: RevokePendingProjectInvitesForEmail :execrows
UPDATE invites
SET revoked_at = ?
WHERE project_id = ?
  AND email = ? COLLATE NOCASE
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > ?;

-- name: RevokeProjectInvite :execrows
UPDATE invites
SET revoked_at = ?
WHERE project_id = ?
  AND id = ?
  AND accepted_at IS NULL
  AND revoked_at IS NULL;

-- name: GetInviteByTokenHash :one
-- isolation: global (invite preview and acceptance look up invite by unique token hash)
SELECT
    i.id,
    i.project_id,
    i.email,
    i.role,
    i.token_hash,
    i.created_by,
    i.created_at,
    i.expires_at,
    i.accepted_at,
    i.accepted_by,
    i.revoked_at,
    p.slug AS project_slug,
    p.name AS project_name,
    u.name AS created_by_name,
    u.email AS created_by_email
FROM invites i
LEFT JOIN projects p ON p.id = i.project_id
LEFT JOIN users u ON u.id = i.created_by
WHERE i.token_hash = ?;

-- name: ConsumeInvite :execrows
-- isolation: global (single-use atomic invite acceptance guard by primary key)
UPDATE invites
SET accepted_at = ?,
    accepted_by = ?
WHERE id = ?
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > ?;

-- name: SetInviteAcceptedBy :exec
-- isolation: global (sets accepted_by inside the same transaction after new user creation)
UPDATE invites
SET accepted_by = ?
WHERE id = ?;

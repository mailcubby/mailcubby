-- name: CountUsers :one
SELECT COUNT(*)
FROM users;

-- name: CreateUser :one
INSERT INTO users (
    email,
    name,
    password_hash,
    is_admin,
    can_create_projects,
    disabled_at,
    last_login_at,
    created_at,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, email, name, password_hash, is_admin, can_create_projects, disabled_at, last_login_at, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, email, name, password_hash, is_admin, can_create_projects, disabled_at, last_login_at, created_at, updated_at
FROM users
WHERE id = ?;

-- name: GetUserByEmail :one
SELECT id, email, name, password_hash, is_admin, can_create_projects, disabled_at, last_login_at, created_at, updated_at
FROM users
WHERE email = ?;

-- name: UpdateUserName :one
UPDATE users
SET name = ?,
    updated_at = ?
WHERE id = ?
RETURNING id, email, name, password_hash, is_admin, can_create_projects, disabled_at, last_login_at, created_at, updated_at;

-- name: UpdateUserPassword :execrows
UPDATE users
SET password_hash = ?,
    updated_at = ?
WHERE id = ?;

-- name: UpdateUserLastLogin :exec
UPDATE users
SET last_login_at = ?,
    updated_at = ?
WHERE id = ?;

-- name: ListUsersAdmin :many
-- isolation: global (admin user listing aggregates project membership count per user)
SELECT
    u.id,
    u.email,
    u.name,
    u.password_hash,
    u.is_admin,
    u.can_create_projects,
    u.disabled_at,
    u.last_login_at,
    u.created_at,
    u.updated_at,
    COUNT(pm.project_id) AS project_count
FROM users u
LEFT JOIN project_members pm ON pm.user_id = u.id
WHERE u.id > sqlc.arg(after_id)
  AND (
    CAST(sqlc.arg(query_lower) AS TEXT) = ''
    OR INSTR(LOWER(u.email), CAST(sqlc.arg(query_lower) AS TEXT)) > 0
    OR INSTR(LOWER(u.name), CAST(sqlc.arg(query_lower) AS TEXT)) > 0
  )
GROUP BY u.id
ORDER BY u.id ASC
LIMIT sqlc.arg(page_limit);

-- name: GetUserWithProjectCount :one
-- isolation: global (admin user response includes project membership count)
SELECT
    u.id,
    u.email,
    u.name,
    u.password_hash,
    u.is_admin,
    u.can_create_projects,
    u.disabled_at,
    u.last_login_at,
    u.created_at,
    u.updated_at,
    COUNT(pm.project_id) AS project_count
FROM users u
LEFT JOIN project_members pm ON pm.user_id = u.id
WHERE u.id = ?
GROUP BY u.id;

-- name: UpdateUserAdmin :one
UPDATE users
SET name = ?,
    is_admin = ?,
    can_create_projects = ?,
    disabled_at = ?,
    updated_at = ?
WHERE id = ?
RETURNING id, email, name, password_hash, is_admin, can_create_projects, disabled_at, last_login_at, created_at, updated_at;

-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = ?;

-- name: CountAdmins :one
SELECT COUNT(*)
FROM users
WHERE is_admin = 1;

-- name: CountActiveAdmins :one
SELECT COUNT(*)
FROM users
WHERE is_admin = 1 AND disabled_at IS NULL;

-- name: RevokeAllAPITokensForUser :execrows
UPDATE api_tokens
SET revoked_at = ?
WHERE user_id = ? AND revoked_at IS NULL;

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

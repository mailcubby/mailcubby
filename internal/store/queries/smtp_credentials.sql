-- name: CreateSMTPCredential :one
INSERT INTO smtp_credentials (
    project_id,
    label,
    username,
    password_enc,
    created_by,
    created_at
) VALUES (?, ?, ?, ?, ?, ?)
RETURNING
    id,
    project_id,
    label,
    username,
    password_enc,
    created_by,
    created_at,
    last_used_at,
    disabled_at;

-- name: CountSMTPCredentialsByProject :one
SELECT COUNT(*)
FROM smtp_credentials
WHERE project_id = ?;

-- name: ListSMTPCredentialsByProject :many
SELECT
    id,
    project_id,
    label,
    username,
    password_enc,
    created_by,
    created_at,
    last_used_at,
    disabled_at
FROM smtp_credentials
WHERE project_id = ?
ORDER BY created_at ASC, id ASC;

-- name: GetSMTPCredentialByID :one
SELECT
    id,
    project_id,
    label,
    username,
    password_enc,
    created_by,
    created_at,
    last_used_at,
    disabled_at
FROM smtp_credentials
WHERE project_id = ? AND id = ?;

-- name: UpdateSMTPCredential :one
UPDATE smtp_credentials
SET label = ?,
    disabled_at = ?
WHERE project_id = ? AND id = ?
RETURNING
    id,
    project_id,
    label,
    username,
    password_enc,
    created_by,
    created_at,
    last_used_at,
    disabled_at;

-- name: UpdateSMTPCredentialPassword :one
UPDATE smtp_credentials
SET password_enc = ?
WHERE project_id = ? AND id = ?
RETURNING
    id,
    project_id,
    label,
    username,
    password_enc,
    created_by,
    created_at,
    last_used_at,
    disabled_at;

-- name: DeleteSMTPCredential :execrows
DELETE FROM smtp_credentials
WHERE project_id = ? AND id = ?;

-- name: TouchSMTPCredentialLastUsed :exec
UPDATE smtp_credentials
SET last_used_at = ?
WHERE project_id = ? AND id = ?;

-- name: LookupSMTPCredentialByUsername :one
-- isolation: global (SMTP AUTH looks up credential by unique username)
SELECT
    c.id,
    c.project_id,
    c.label,
    c.username,
    c.password_enc,
    c.created_by,
    c.created_at,
    c.last_used_at,
    c.disabled_at,
    p.slug AS project_slug,
    p.plus_address_tags AS project_plus_address_tags
FROM smtp_credentials c
JOIN projects p ON p.id = c.project_id
WHERE c.username = ?;

-- name: ListSMTPCredentialSecrets :many
-- isolation: global (startup MAILCUBBY_APP_KEY check over every credential; system only, no handler reaches it)
SELECT id, username, password_enc
FROM smtp_credentials
ORDER BY id;

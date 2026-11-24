-- name: CreateAPIToken :one
INSERT INTO api_tokens (
    user_id,
    project_id,
    name,
    prefix,
    token_hash,
    scope,
    created_at,
    expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING
    id,
    user_id,
    project_id,
    name,
    prefix,
    token_hash,
    scope,
    created_at,
    last_used_at,
    expires_at,
    revoked_at;

-- name: ListAPITokensByUser :many
SELECT
    t.id,
    t.user_id,
    t.project_id,
    p.slug AS project_slug,
    t.name,
    t.prefix,
    t.token_hash,
    t.scope,
    t.created_at,
    t.last_used_at,
    t.expires_at,
    t.revoked_at
FROM api_tokens t
LEFT JOIN projects p ON p.id = t.project_id
WHERE t.user_id = ?
  AND t.revoked_at IS NULL
ORDER BY t.created_at DESC, t.id DESC;

-- name: GetAPITokenByPrefix :one
SELECT
    t.id,
    t.user_id,
    t.project_id,
    p.slug AS project_slug,
    t.name,
    t.prefix,
    t.token_hash,
    t.scope,
    t.created_at,
    t.last_used_at,
    t.expires_at,
    t.revoked_at
FROM api_tokens t
LEFT JOIN projects p ON p.id = t.project_id
WHERE t.prefix = ?;

-- name: GetAPITokenByIDForUser :one
SELECT
    t.id,
    t.user_id,
    t.project_id,
    p.slug AS project_slug,
    t.name,
    t.prefix,
    t.token_hash,
    t.scope,
    t.created_at,
    t.last_used_at,
    t.expires_at,
    t.revoked_at
FROM api_tokens t
LEFT JOIN projects p ON p.id = t.project_id
WHERE t.id = ?
  AND t.user_id = ?
  AND t.revoked_at IS NULL;

-- name: RevokeAPITokenForUser :execrows
UPDATE api_tokens
SET revoked_at = ?
WHERE id = ?
  AND user_id = ?
  AND revoked_at IS NULL;

-- name: TouchAPITokenLastUsed :exec
UPDATE api_tokens
SET last_used_at = ?
WHERE id = ?;

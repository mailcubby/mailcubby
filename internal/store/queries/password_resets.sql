-- name: CreatePasswordReset :one
INSERT INTO password_resets (
    user_id,
    token_hash,
    created_by,
    created_at,
    expires_at
) VALUES (?, ?, ?, ?, ?)
RETURNING id, user_id, token_hash, created_by, created_at, expires_at, used_at;

-- name: GetPasswordResetByTokenHash :one
SELECT id, user_id, token_hash, created_by, created_at, expires_at, used_at
FROM password_resets
WHERE token_hash = ?;

-- name: ConsumePasswordReset :execrows
UPDATE password_resets
SET used_at = ?
WHERE id = ?
  AND used_at IS NULL
  AND expires_at > ?;

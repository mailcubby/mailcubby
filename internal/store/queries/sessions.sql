-- name: CreateSession :one
INSERT INTO sessions (
    id,
    user_id,
    csrf_token,
    ip,
    user_agent,
    created_at,
    last_seen_at,
    expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at;

-- name: GetSessionByID :one
SELECT id, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at
FROM sessions
WHERE id = ?;

-- name: TouchSession :exec
UPDATE sessions
SET last_seen_at = ?
WHERE id = ?;

-- name: ListSessionsByUser :many
SELECT id, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at
FROM sessions
WHERE user_id = ?
ORDER BY last_seen_at DESC, created_at DESC;

-- name: DeleteSessionByID :execrows
DELETE FROM sessions
WHERE id = ?;

-- name: DeleteSessionByIDForUser :execrows
DELETE FROM sessions
WHERE id = ? AND user_id = ?;

-- name: DeleteOtherSessionsForUser :execrows
DELETE FROM sessions
WHERE user_id = ? AND id != ?;

-- name: DeleteAllSessionsForUser :execrows
DELETE FROM sessions
WHERE user_id = ?;

-- name: GetSetting :one
SELECT key, value, updated_at
FROM settings
WHERE key = ?;

-- name: SetSetting :exec
INSERT INTO settings (key, value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    updated_at = excluded.updated_at;

-- name: DeleteSetting :execrows
DELETE FROM settings
WHERE key = ?;

-- name: ListSettings :many
SELECT key, value, updated_at
FROM settings
ORDER BY key ASC;

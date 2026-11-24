-- name: CreateProject :one
INSERT INTO projects (
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    created_at,
    updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at;

-- name: GetProjectByID :one
SELECT
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at
FROM projects
WHERE id = ?;

-- name: GetProjectBySlug :one
SELECT
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at
FROM projects
WHERE slug = ?;

-- name: ListProjects :many
SELECT
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at
FROM projects
ORDER BY name ASC, id ASC;

-- name: UpdateProject :one
UPDATE projects
SET
    slug = ?,
    name = ?,
    color = ?,
    retention_max_age_s = ?,
    retention_max_messages = ?,
    retention_max_bytes = ?,
    recipient_routing_enabled = ?,
    plus_address_tags = ?,
    viewer_hide_technical = ?,
    members_can_reveal_smtp = ?,
    updated_at = ?
WHERE id = ?
RETURNING
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at;

-- name: DeleteProject :execrows
DELETE FROM projects
WHERE id = ?;

-- name: UpdateProjectCounters :execrows
UPDATE projects
SET
    message_count = message_count + sqlc.arg(message_delta),
    unread_count = unread_count + sqlc.arg(unread_delta),
    size_bytes = size_bytes + sqlc.arg(size_delta),
    last_message_at = CASE
        WHEN CAST(sqlc.narg(last_message_at) AS INTEGER) IS NOT NULL
            AND (last_message_at IS NULL OR CAST(sqlc.narg(last_message_at) AS INTEGER) > last_message_at)
        THEN CAST(sqlc.narg(last_message_at) AS INTEGER)
        ELSE last_message_at
    END,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateProjectCountersAndLastMessageAt :one
UPDATE projects
SET
    message_count = message_count + sqlc.arg(message_delta),
    unread_count = unread_count + sqlc.arg(unread_delta),
    size_bytes = size_bytes + sqlc.arg(size_delta),
    last_message_at = sqlc.narg(last_message_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at;

-- name: ResetProjectCounters :one
UPDATE projects
SET
    message_count = 0,
    unread_count = 0,
    size_bytes = 0,
    last_message_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING
    id,
    slug,
    name,
    color,
    retention_max_age_s,
    retention_max_messages,
    retention_max_bytes,
    recipient_routing_enabled,
    plus_address_tags,
    viewer_hide_technical,
    members_can_reveal_smtp,
    message_count,
    unread_count,
    size_bytes,
    last_message_at,
    created_at,
    updated_at;

-- name: ListProjectIDs :many
SELECT id
FROM projects
ORDER BY id ASC;

-- name: GetInstanceProjectTotals :one
SELECT
    COUNT(*) AS project_count,
    CAST(COALESCE(SUM(message_count), 0) AS INTEGER) AS total_messages,
    CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS total_bytes
FROM projects;

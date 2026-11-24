-- name: ListTagsByProject :many
SELECT
    t.id,
    t.project_id,
    t.name,
    t.color,
    t.created_at,
    COUNT(mt.message_pk) AS message_count
FROM tags t
LEFT JOIN message_tags mt ON mt.tag_id = t.id AND mt.project_id = t.project_id
WHERE t.project_id = ?
GROUP BY t.id
ORDER BY t.name COLLATE NOCASE ASC;

-- name: GetTagByID :one
SELECT
    t.id,
    t.project_id,
    t.name,
    t.color,
    t.created_at,
    COUNT(mt.message_pk) AS message_count
FROM tags t
LEFT JOIN message_tags mt ON mt.tag_id = t.id AND mt.project_id = t.project_id
WHERE t.project_id = ? AND t.id = ?
GROUP BY t.id;

-- name: GetTagByName :one
SELECT id, project_id, name, color, created_at
FROM tags
WHERE project_id = ? AND name = ? COLLATE NOCASE;

-- name: CreateTag :one
INSERT INTO tags (project_id, name, color, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, project_id, name, color, created_at;

-- name: EnsureTagInsert :exec
INSERT INTO tags (project_id, name, color, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(project_id, name) DO NOTHING;

-- name: UpdateTag :one
UPDATE tags
SET name = ?, color = ?
WHERE project_id = ? AND id = ?
RETURNING id, project_id, name, color, created_at;

-- name: DeleteTag :execrows
DELETE FROM tags
WHERE project_id = ? AND id = ?;

-- name: ListMessageIDsByTagID :many
SELECT m.id
FROM messages m
JOIN message_tags mt ON mt.message_pk = m.pk AND mt.project_id = m.project_id
WHERE mt.project_id = ? AND mt.tag_id = ?
ORDER BY m.id DESC;

-- name: AddMessageTag :execrows
INSERT INTO message_tags (message_pk, tag_id, project_id)
VALUES (?, ?, ?)
ON CONFLICT(message_pk, tag_id) DO NOTHING;

-- name: RemoveMessageTag :execrows
DELETE FROM message_tags
WHERE project_id = ? AND message_pk = ? AND tag_id = ?;

-- name: DeleteMessageTagsByMessagePK :exec
DELETE FROM message_tags
WHERE project_id = ? AND message_pk = ?;

-- name: ListTagNamesByMessagePK :many
SELECT t.name
FROM tags t
JOIN message_tags mt ON mt.tag_id = t.id AND mt.project_id = t.project_id
WHERE mt.project_id = ? AND mt.message_pk = ?
ORDER BY t.name COLLATE NOCASE ASC;

-- name: ListTagsByMessagePKs :many
SELECT mt.message_pk, t.name
FROM tags t
JOIN message_tags mt ON mt.tag_id = t.id AND mt.project_id = t.project_id
WHERE mt.project_id = ? AND mt.message_pk IN (sqlc.slice('message_pks'))
ORDER BY t.name COLLATE NOCASE ASC;

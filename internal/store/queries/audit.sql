-- name: InsertAudit :one
INSERT INTO audit_log (
    at,
    actor_type,
    actor_user_id,
    actor_label,
    project_id,
    action,
    target_type,
    target_id,
    ip,
    metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, at, actor_type, actor_user_id, actor_label, project_id, action, target_type, target_id, ip, metadata_json;

-- name: ListAuditEntries :many
SELECT id, at, actor_type, actor_user_id, actor_label, project_id, action, target_type, target_id, ip, metadata_json
FROM audit_log
ORDER BY at DESC, id DESC
LIMIT ?;

-- name: DeleteOldAuditEntries :execrows
DELETE FROM audit_log
WHERE at < ?;

-- name: ListProjectAudit :many
SELECT
    a.id,
    a.at,
    a.actor_type,
    a.actor_user_id,
    a.actor_label,
    a.project_id,
    p.slug AS project_slug,
    a.action,
    a.target_type,
    a.target_id,
    a.ip,
    a.metadata_json
FROM audit_log a
LEFT JOIN projects p ON p.id = a.project_id
WHERE a.project_id = sqlc.arg(project_id)
  AND (CAST(sqlc.arg(before_id) AS INTEGER) = 0 OR a.id < CAST(sqlc.arg(before_id) AS INTEGER))
  AND (CAST(sqlc.arg(action_filter) AS TEXT) = '' OR a.action = CAST(sqlc.arg(action_filter) AS TEXT))
ORDER BY a.id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListAdminAudit :many
SELECT
    a.id,
    a.at,
    a.actor_type,
    a.actor_user_id,
    a.actor_label,
    a.project_id,
    p.slug AS project_slug,
    a.action,
    a.target_type,
    a.target_id,
    a.ip,
    a.metadata_json
FROM audit_log a
LEFT JOIN projects p ON p.id = a.project_id
WHERE (CAST(sqlc.arg(before_id) AS INTEGER) = 0 OR a.id < CAST(sqlc.arg(before_id) AS INTEGER))
  AND (CAST(sqlc.arg(action_filter) AS TEXT) = '' OR a.action = CAST(sqlc.arg(action_filter) AS TEXT))
  AND (CAST(sqlc.arg(project_id_filter) AS INTEGER) = 0 OR a.project_id = CAST(sqlc.arg(project_id_filter) AS INTEGER))
  AND (CAST(sqlc.arg(user_id_filter) AS INTEGER) = 0 OR a.actor_user_id = CAST(sqlc.arg(user_id_filter) AS INTEGER))
  AND (
    CAST(sqlc.arg(user_label_lower) AS TEXT) = ''
    OR INSTR(LOWER(COALESCE(a.actor_label, '')), CAST(sqlc.arg(user_label_lower) AS TEXT)) > 0
  )
ORDER BY a.id DESC
LIMIT sqlc.arg(page_limit);

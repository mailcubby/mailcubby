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

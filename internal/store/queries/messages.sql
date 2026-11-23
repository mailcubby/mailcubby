-- name: InsertMessage :one
INSERT INTO messages (
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING
    pk,
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json;

-- name: GetMessageByID :one
SELECT
    pk,
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json
FROM messages
WHERE project_id = ? AND id = ?;

-- name: ListMessages :many
SELECT
    pk,
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json
FROM messages
WHERE project_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: ListMessagesBeforeCursor :many
SELECT
    pk,
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json
FROM messages
WHERE project_id = ? AND id < ?
ORDER BY id DESC
LIMIT ?;

-- name: GetProjectSparkline :many
SELECT
    CAST(received_at / 3600000 AS INTEGER) AS hour_bucket,
    COUNT(*) AS message_count
FROM messages
WHERE project_id = ? AND received_at >= ?
GROUP BY hour_bucket
ORDER BY hour_bucket ASC;

-- name: InsertAttachment :one
INSERT INTO attachments (
    message_pk,
    project_id,
    part_path,
    filename,
    content_type,
    size_bytes,
    content_id,
    disposition,
    sha256
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING
    id,
    message_pk,
    project_id,
    part_path,
    filename,
    content_type,
    size_bytes,
    content_id,
    disposition,
    sha256;

-- name: ListAttachmentsByMessagePK :many
SELECT
    id,
    message_pk,
    project_id,
    part_path,
    filename,
    content_type,
    size_bytes,
    content_id,
    disposition,
    sha256
FROM attachments
WHERE project_id = ? AND message_pk = ?
ORDER BY id ASC;

-- name: GetAttachmentByID :one
SELECT
    id,
    message_pk,
    project_id,
    part_path,
    filename,
    content_type,
    size_bytes,
    content_id,
    disposition,
    sha256
FROM attachments
WHERE project_id = ? AND message_pk = ? AND id = ?;

-- name: UpdateMessageSeenIfChanged :execrows
UPDATE messages
SET seen = sqlc.arg(seen)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND seen != sqlc.arg(seen);

-- name: DeleteMessageByID :execrows
DELETE FROM messages
WHERE project_id = ? AND id = ?;

-- name: ListMessageBlobPathsByProject :many
SELECT blob_path
FROM messages
WHERE project_id = ?;

-- name: DeleteAllMessagesByProject :execrows
DELETE FROM messages
WHERE project_id = ?;

-- name: GetLatestMessageAfterID :one
SELECT
    pk,
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json
FROM messages
WHERE project_id = ? AND id > ?
ORDER BY id DESC
LIMIT 1;

-- name: GetLatestMessageSinceTime :one
SELECT
    pk,
    id,
    project_id,
    smtp_credential_id,
    received_at,
    subject,
    from_name,
    from_addr,
    to_json,
    cc_json,
    bcc_json,
    reply_to_json,
    envelope_from,
    envelope_to_json,
    message_id_header,
    date_header,
    snippet,
    has_html,
    has_text,
    text_generated,
    attachment_count,
    size_bytes,
    blob_path,
    seen,
    remote_ip,
    helo,
    auth_user,
    tls,
    parse_warnings_json
FROM messages
WHERE project_id = ? AND received_at >= ?
ORDER BY id DESC
LIMIT 1;

-- name: GetLatestMessageReceivedAt :one
SELECT received_at
FROM messages
WHERE project_id = ?
ORDER BY received_at DESC
LIMIT 1;

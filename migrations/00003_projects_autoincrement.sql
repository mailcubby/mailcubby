-- +goose NO TRANSACTION
-- +goose Up

-- Rebuild projects with AUTOINCREMENT so a deleted project's ID is never reused
-- by a later project (stale IDs in SSE subscriptions, admin-access audit dedup
-- and blob cleanup must never point at an unrelated tenant).
--
-- SQLite cannot add AUTOINCREMENT in place, so this follows the documented
-- table-rebuild procedure. Foreign keys must be OFF while the old table is
-- dropped: with them ON, DROP TABLE performs an implicit DELETE that cascades
-- to every child table (members, invites, credentials, tokens, messages, tags).
-- The PRAGMA is a no-op inside a transaction, hence NO TRANSACTION; goose runs
-- every statement of this file on the same connection.

PRAGMA foreign_keys = OFF;

BEGIN IMMEDIATE;

CREATE TABLE projects_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    color TEXT NOT NULL,
    retention_max_age_s INTEGER,
    retention_max_messages INTEGER,
    retention_max_bytes INTEGER,
    recipient_routing_enabled INTEGER NOT NULL DEFAULT 0,
    plus_address_tags INTEGER NOT NULL DEFAULT 1,
    viewer_hide_technical INTEGER NOT NULL DEFAULT 1,
    members_can_reveal_smtp INTEGER NOT NULL DEFAULT 0,
    message_count INTEGER NOT NULL DEFAULT 0,
    unread_count INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    last_message_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

INSERT INTO projects_new (
    id, slug, name, color,
    retention_max_age_s, retention_max_messages, retention_max_bytes,
    recipient_routing_enabled, plus_address_tags, viewer_hide_technical, members_can_reveal_smtp,
    message_count, unread_count, size_bytes, last_message_at,
    created_at, updated_at
)
SELECT
    id, slug, name, color,
    retention_max_age_s, retention_max_messages, retention_max_bytes,
    recipient_routing_enabled, plus_address_tags, viewer_hide_technical, members_can_reveal_smtp,
    message_count, unread_count, size_bytes, last_message_at,
    created_at, updated_at
FROM projects;

DROP TABLE projects;

ALTER TABLE projects_new RENAME TO projects;

-- IDs of projects deleted before this migration are only known from the audit
-- log; start the sequence above them so they are not handed out again either.
INSERT INTO sqlite_sequence (name, seq)
SELECT 'projects', 0
WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'projects');

UPDATE sqlite_sequence
SET seq = MAX(seq, (
    SELECT COALESCE(MAX(CAST(target_id AS INTEGER)), 0)
    FROM audit_log
    WHERE target_type = 'project' AND action IN ('project.create', 'project.delete')
))
WHERE name = 'projects';

-- Abort (leaving the transaction uncommitted) if any child row lost its parent.
CREATE TEMP TABLE projects_fk_guard (orphaned_child_rows INTEGER NOT NULL CHECK (orphaned_child_rows = 0));
INSERT INTO projects_fk_guard (orphaned_child_rows) SELECT COUNT(*) FROM pragma_foreign_key_check;
DROP TABLE projects_fk_guard;

COMMIT;

PRAGMA foreign_keys = ON;

-- +goose Down

-- Intentionally empty: AUTOINCREMENT on projects.id is compatible with every
-- earlier schema version, and rebuilding the table again would only reopen the
-- ID-reuse hazard this migration closes.

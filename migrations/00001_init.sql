-- +goose Up

CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL,
    password_hash TEXT,
    is_admin INTEGER NOT NULL DEFAULT 0,
    can_create_projects INTEGER NOT NULL DEFAULT 0,
    disabled_at INTEGER,
    last_login_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    ip TEXT,
    user_agent TEXT,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE password_resets (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER
);

CREATE TABLE projects (
    id INTEGER PRIMARY KEY,
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

CREATE TABLE project_members (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'member', 'viewer')),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX idx_project_members_user_id ON project_members(user_id);

CREATE TABLE invites (
    id INTEGER PRIMARY KEY,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    role TEXT CHECK (role IN ('owner', 'member', 'viewer')),
    token_hash TEXT NOT NULL UNIQUE,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    accepted_at INTEGER,
    accepted_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    revoked_at INTEGER,
    CHECK ((project_id IS NULL) = (role IS NULL))
);

CREATE INDEX idx_invites_project_id ON invites(project_id);

CREATE TABLE smtp_credentials (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    username TEXT NOT NULL UNIQUE,
    password_enc BLOB NOT NULL,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER,
    disabled_at INTEGER
);

CREATE INDEX idx_smtp_credentials_project_id ON smtp_credentials(project_id);

CREATE TABLE api_tokens (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL UNIQUE,
    token_hash TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    created_at INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at INTEGER,
    revoked_at INTEGER
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);

CREATE TABLE messages (
    pk INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    smtp_credential_id INTEGER REFERENCES smtp_credentials(id) ON DELETE SET NULL,
    received_at INTEGER NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    from_name TEXT NOT NULL DEFAULT '',
    from_addr TEXT NOT NULL DEFAULT '',
    to_json TEXT NOT NULL DEFAULT '[]',
    cc_json TEXT NOT NULL DEFAULT '[]',
    bcc_json TEXT NOT NULL DEFAULT '[]',
    reply_to_json TEXT NOT NULL DEFAULT '[]',
    envelope_from TEXT NOT NULL,
    envelope_to_json TEXT NOT NULL,
    message_id_header TEXT,
    date_header INTEGER,
    snippet TEXT NOT NULL DEFAULT '',
    has_html INTEGER NOT NULL,
    has_text INTEGER NOT NULL,
    text_generated INTEGER NOT NULL,
    attachment_count INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL,
    blob_path TEXT NOT NULL,
    seen INTEGER NOT NULL DEFAULT 0,
    remote_ip TEXT,
    helo TEXT,
    auth_user TEXT,
    tls INTEGER NOT NULL DEFAULT 0,
    parse_warnings_json TEXT
);

CREATE INDEX idx_messages_project_id_desc ON messages(project_id, id DESC);
CREATE INDEX idx_messages_project_received_at ON messages(project_id, received_at);
CREATE INDEX idx_messages_project_unseen ON messages(project_id, seen) WHERE seen = 0;
CREATE INDEX idx_messages_project_smtp_credential ON messages(project_id, smtp_credential_id);

CREATE TABLE attachments (
    id INTEGER PRIMARY KEY,
    message_pk INTEGER NOT NULL REFERENCES messages(pk) ON DELETE CASCADE,
    project_id INTEGER NOT NULL,
    part_path TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    content_id TEXT,
    disposition TEXT NOT NULL,
    sha256 TEXT NOT NULL
);

CREATE INDEX idx_attachments_message_pk ON attachments(message_pk);
CREATE INDEX idx_attachments_project_id ON attachments(project_id);

CREATE TABLE tags (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    color TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(project_id, name COLLATE NOCASE)
);

CREATE TABLE message_tags (
    message_pk INTEGER NOT NULL REFERENCES messages(pk) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL,
    PRIMARY KEY (message_pk, tag_id)
);

CREATE INDEX idx_message_tags_project_tag ON message_tags(project_id, tag_id);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY,
    at INTEGER NOT NULL,
    actor_type TEXT NOT NULL,
    actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    actor_label TEXT,
    project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    ip TEXT,
    metadata_json TEXT
);

CREATE INDEX idx_audit_log_at ON audit_log(at);
CREATE INDEX idx_audit_log_project_at ON audit_log(project_id, at);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS message_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS smtp_credentials;
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS password_resets;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;

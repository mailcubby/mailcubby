package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// childSnapshotQueries capture, per table, every row's link to projects (and to
// the rows that link to projects). Equal snapshots before and after the
// 00003 rebuild prove no cascade ran: audit_log.project_id is ON DELETE SET NULL,
// so its values are compared, not only its row count.
var childSnapshotQueries = []string{
	"SELECT id, slug FROM projects ORDER BY id",
	"SELECT project_id, user_id, role FROM project_members ORDER BY project_id, user_id",
	"SELECT id, project_id FROM invites ORDER BY id",
	"SELECT id, project_id FROM smtp_credentials ORDER BY id",
	"SELECT id, project_id FROM api_tokens ORDER BY id",
	"SELECT pk, id, project_id, smtp_credential_id FROM messages ORDER BY pk",
	"SELECT id, message_pk, project_id FROM attachments ORDER BY id",
	"SELECT id, project_id, name FROM tags ORDER BY id",
	"SELECT message_pk, tag_id, project_id FROM message_tags ORDER BY message_pk, tag_id",
	"SELECT id, project_id, action FROM audit_log ORDER BY id",
	"SELECT rowid FROM messages_fts WHERE messages_fts MATCH 'autoincrementtoken' ORDER BY rowid",
}

// queryAsText renders every row of q as text and returns it with the row count.
func queryAsText(ctx context.Context, t *testing.T, db *sql.DB, q string) (string, int) {
	t.Helper()
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns %q: %v", q, err)
	}
	var b strings.Builder
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %q: %v", q, err)
		}
		fmt.Fprintf(&b, "%v;", vals)
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows %q: %v", q, err)
	}
	return b.String(), n
}

func snapshotProjectChildren(ctx context.Context, t *testing.T, db *sql.DB) []string {
	t.Helper()
	out := make([]string, 0, len(childSnapshotQueries))
	for _, q := range childSnapshotQueries {
		text, n := queryAsText(ctx, t, db, q)
		if n == 0 {
			t.Fatalf("snapshot %q: no rows; every child table must be populated for this test", q)
		}
		out = append(out, q+" => "+text)
	}
	return out
}

// Migration 00003 rebuilds projects with AUTOINCREMENT on a populated database
// without losing or orphaning any child row, and IDs are never reused afterwards.
func TestMigrationProjectsAutoincrementPreservesChildren(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), DBFilename)

	// 1. Build a data dir at schema version 2 using the production writer DSN.
	db, err := sql.Open("sqlite", buildDSN(dbPath, writerDSNQuery))
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	db.SetMaxOpenConns(1)
	provider, err := newGooseProvider(db)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("migrate up to 2: %v", err)
	}

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC).UnixMilli()
	seed := []string{
		`INSERT INTO users (id, email, name, created_at, updated_at) VALUES (1, 'a@x.test', 'A', 0, 0), (2, 'b@x.test', 'B', 0, 0)`,
		`INSERT INTO projects (id, slug, name, color, created_at, updated_at) VALUES (1, 'alpha', 'Alpha', 'blue', 0, 0), (2, 'beta', 'Beta', 'red', 0, 0)`,
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (1, 1, 'owner', 0), (2, 2, 'owner', 0), (2, 1, 'viewer', 0)`,
		`INSERT INTO invites (id, project_id, email, role, token_hash, created_by, created_at, expires_at) VALUES (1, 1, 'i1@x.test', 'member', 'h1', 1, 0, 1), (2, 2, 'i2@x.test', 'viewer', 'h2', 2, 0, 1)`,
		`INSERT INTO smtp_credentials (id, project_id, label, username, password_enc, created_at) VALUES (1, 1, 'app', 'alpha-1', x'00', 0), (2, 2, 'app', 'beta-1', x'00', 0)`,
		`INSERT INTO api_tokens (id, user_id, project_id, name, prefix, token_hash, scope, created_at) VALUES (1, 1, 1, 't1', 'p1', 'h', 'read', 0), (2, 2, 2, 't2', 'p2', 'h', 'write', 0)`,
		`INSERT INTO messages (pk, id, project_id, smtp_credential_id, received_at, envelope_from, envelope_to_json, has_html, has_text, text_generated, size_bytes, blob_path) VALUES
			(1, '01JAUTOINC00000000000000001', 1, 1, 0, 'f@x.test', '[]', 0, 1, 0, 10, 'b1'),
			(2, '01JAUTOINC00000000000000002', 2, 2, 0, 'f@x.test', '[]', 0, 1, 0, 10, 'b2')`,
		`INSERT INTO attachments (id, message_pk, project_id, part_path, filename, content_type, size_bytes, disposition, sha256) VALUES (1, 1, 1, '1', 'a.txt', 'text/plain', 1, 'attachment', 's'), (2, 2, 2, '1', 'b.txt', 'text/plain', 1, 'attachment', 's')`,
		`INSERT INTO tags (id, project_id, name, color, created_at) VALUES (1, 1, 'x', 'blue', 0), (2, 2, 'y', 'red', 0)`,
		`INSERT INTO message_tags (message_pk, tag_id, project_id) VALUES (1, 1, 1), (2, 2, 2)`,
		// Project 4 was deleted before the migration; only the audit log remembers its ID.
		fmt.Sprintf(`INSERT INTO audit_log (id, at, actor_type, actor_user_id, project_id, action, target_type, target_id) VALUES
			(1, %[1]d, 'user', 1, 1, 'project.create', 'project', '1'),
			(2, %[1]d, 'user', 2, 2, 'project.admin_access', 'project', '2'),
			(3, %[1]d, 'user', 1, NULL, 'project.delete', 'project', '4')`, now),
		`INSERT INTO messages_fts (rowid, subject, from_text, to_text, body, attachments) VALUES (1, 'autoincrementtoken', '', '', '', ''), (2, 'autoincrementtoken', '', '', '', '')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	// The migration must protect children even though the connection has FKs ON.
	var fkOn int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkOn); err != nil || fkOn != 1 {
		t.Fatalf("PRAGMA foreign_keys before migration = %d (err %v), want 1", fkOn, err)
	}
	before := snapshotProjectChildren(ctx, t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	// 2. Open through the production path, which applies 00003.
	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (migrating): %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.MigrationsApplied(ctx); err != nil {
		t.Fatalf("MigrationsApplied: %v", err)
	}

	after := snapshotProjectChildren(ctx, t, st.writer)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("child rows changed by migration:\nbefore:\n%s\nafter:\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}

	if text, violations := queryAsText(ctx, t, st.writer, "PRAGMA foreign_key_check"); violations != 0 {
		t.Fatalf("foreign_key_check reported %d violations: %s", violations, text)
	}

	var ddl string
	if err := st.writer.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'projects'").Scan(&ddl); err != nil {
		t.Fatalf("read projects ddl: %v", err)
	}
	if !strings.Contains(ddl, "AUTOINCREMENT") {
		t.Fatalf("projects ddl lacks AUTOINCREMENT: %s", ddl)
	}
	var leftovers int
	if err := st.writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'projects_new'").Scan(&leftovers); err != nil || leftovers != 0 {
		t.Fatalf("projects_new leftovers = %d (err %v), want 0", leftovers, err)
	}

	// 3. Deleting the max-id project still cascades, and its ID is never reused.
	if err := st.DeleteProject(ctx, 2); err != nil {
		t.Fatalf("DeleteProject(2): %v", err)
	}
	var orphans int
	if err := st.writer.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM messages WHERE project_id = 2) + (SELECT COUNT(*) FROM project_members WHERE project_id = 2)").Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("rows of deleted project 2 = %d (err %v), want 0", orphans, err)
	}
	created, err := st.CreateProject(ctx, CreateProjectParams{
		Slug: "gamma", Name: "Gamma", Color: "green", Settings: DefaultProjectSettings(), Now: time.UnixMilli(now),
	})
	if err != nil {
		t.Fatalf("CreateProject(gamma): %v", err)
	}
	// Above both the just-deleted ID 2 and the pre-migration deleted ID 4.
	if created.ID != 5 {
		t.Fatalf("new project id = %d, want 5 (no reuse of 2 or 4)", created.ID)
	}
}

// Migration 00003 refuses to commit when foreign_key_check finds an orphan,
// leaving the original projects table in place.
func TestMigrationProjectsAutoincrementAbortsOnOrphans(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), DBFilename)

	db, err := sql.Open("sqlite", buildDSN(dbPath, writerDSNQuery))
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	db.SetMaxOpenConns(1)
	provider, err := newGooseProvider(db)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("migrate up to 2: %v", err)
	}
	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO projects (id, slug, name, color, created_at, updated_at) VALUES (1, 'alpha', 'Alpha', 'blue', 0, 0)`,
		`INSERT INTO tags (id, project_id, name, color, created_at) VALUES (1, 99, 'orphan', 'blue', 0)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	st, err := Open(ctx, dbPath)
	if err == nil {
		_ = st.Close()
		t.Fatal("Open succeeded, want migration 00003 to fail on the orphaned tag")
	}
	if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("Open error = %v, want the foreign_key_check guard to fail", err)
	}

	db, err = sql.Open("sqlite", buildDSN(dbPath, readerDSNQuery))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	var ddl string
	if err := db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'projects'").Scan(&ddl); err != nil {
		t.Fatalf("read projects ddl: %v", err)
	}
	if strings.Contains(ddl, "AUTOINCREMENT") {
		t.Fatalf("projects was rebuilt despite the failed check: %s", ddl)
	}
	var projects int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&projects); err != nil || projects != 1 {
		t.Fatalf("projects rows = %d (err %v), want 1", projects, err)
	}
}

package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpenMigrationsAndPragmas(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), DBFilename)

	// Before opening, migration status should list all migrations as pending without creating the file.
	pending, err := CheckMigrationStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("CheckMigrationStatus (before open): %v", err)
	}
	if len(pending) < 2 {
		t.Fatalf("expected at least 2 migrations, got %d", len(pending))
	}
	for _, m := range pending {
		if m.State != "pending" {
			t.Errorf("migration %d state = %q, want pending", m.Version, m.State)
		}
	}

	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open first time: %v", err)
	}

	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	var journalMode string
	if err := st.writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := st.writer.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var autoVacuum int
	if err := st.writer.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if autoVacuum != 2 {
		t.Errorf("auto_vacuum = %d, want 2", autoVacuum)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close first store: %v", err)
	}

	// Opening a second time must be a no-op for migrations.
	st2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open second time: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	applied, err := CheckMigrationStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("CheckMigrationStatus (after open): %v", err)
	}
	for _, m := range applied {
		if m.State != "applied" {
			t.Errorf("migration %d state = %q, want applied", m.Version, m.State)
		}
	}
}

func TestSQLiteVersionAndFTSDeleteTrigger(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var ver string
	if err := st.reader.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&ver); err != nil {
		t.Fatalf("SELECT sqlite_version(): %v", err)
	}
	parts := strings.Split(ver, ".")
	if len(parts) < 2 {
		t.Fatalf("unexpected sqlite_version %q", ver)
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	if major < 3 || (major == 3 && minor < 43) {
		t.Fatalf("sqlite_version %q is older than required 3.43", ver)
	}

	// Create two projects, insert messages and FTS rows, test direct delete and project cascade.
	var p1ID, p2ID int64
	if err := st.WithTx(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `
			INSERT INTO projects (slug, name, color, created_at, updated_at)
			VALUES ('alpha', 'Alpha', 'blue', 1000, 1000)
		`)
		if err != nil {
			return err
		}
		p1ID, err = res.LastInsertId()
		if err != nil {
			return err
		}

		res, err = tx.tx.ExecContext(ctx, `
			INSERT INTO projects (slug, name, color, created_at, updated_at)
			VALUES ('beta', 'Beta', 'green', 1000, 1000)
		`)
		if err != nil {
			return err
		}
		p2ID, err = res.LastInsertId()
		return err
	}); err != nil {
		t.Fatalf("insert projects: %v", err)
	}

	insertMessageWithFTS := func(projectID int64, ulid, subject string) int64 {
		t.Helper()
		var pk int64
		err := st.WithTx(ctx, func(tx *Tx) error {
			res, err := tx.tx.ExecContext(ctx, `
				INSERT INTO messages (
					id, project_id, received_at, subject, envelope_from, envelope_to_json,
					has_html, has_text, text_generated, size_bytes, blob_path
				) VALUES (?, ?, 1000, ?, 'a@example.test', '["b@example.test"]', 0, 1, 0, 42, '1/2026/11/x.eml.zst')
			`, ulid, projectID, subject)
			if err != nil {
				return err
			}
			pk, err = res.LastInsertId()
			if err != nil {
				return err
			}
			_, err = tx.tx.ExecContext(ctx, `
				INSERT INTO messages_fts (rowid, subject, from_text, to_text, body, attachments)
				VALUES (?, ?, 'Alice', 'Bob', 'hello world', '')
			`, pk, subject)
			return err
		})
		if err != nil {
			t.Fatalf("insert message: %v", err)
		}
		return pk
	}

	pk1 := insertMessageWithFTS(p1ID, "01J00000000000000000000001", "UniqueDirectDeleteToken")
	pk2 := insertMessageWithFTS(p2ID, "01J00000000000000000000002", "UniqueCascadeDeleteToken")

	countFTS := func(term string) int {
		t.Helper()
		var n int
		if err := st.reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?", term).Scan(&n); err != nil {
			t.Fatalf("FTS MATCH %q: %v", term, err)
		}
		return n
	}

	if got := countFTS("UniqueDirectDeleteToken"); got != 1 {
		t.Fatalf("FTS count before direct delete = %d, want 1", got)
	}
	if got := countFTS("UniqueCascadeDeleteToken"); got != 1 {
		t.Fatalf("FTS count before cascade delete = %d, want 1", got)
	}

	// Direct message delete should fire messages_fts_delete trigger.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, "DELETE FROM messages WHERE pk = ?", pk1)
		return err
	}); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	if got := countFTS("UniqueDirectDeleteToken"); got != 0 {
		t.Fatalf("FTS count after direct delete = %d, want 0", got)
	}

	// Deleting project p2 should cascade to messages and fire messages_fts_delete trigger.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", p2ID)
		return err
	}); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if got := countFTS("UniqueCascadeDeleteToken"); got != 0 {
		t.Fatalf("FTS count after project cascade delete = %d, want 0 (pk=%d)", got, pk2)
	}
}

func TestInvitesRoleChecks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var projectID int64
	if err := st.WithTx(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `
			INSERT INTO projects (slug, name, color, created_at, updated_at)
			VALUES ('shop', 'Shop', 'slate', 1000, 1000)
		`)
		if err != nil {
			return err
		}
		projectID, err = res.LastInsertId()
		return err
	}); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	// Valid instance invite: project_id NULL, role NULL.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `
			INSERT INTO invites (project_id, email, role, token_hash, created_at, expires_at)
			VALUES (NULL, 'inst@example.test', NULL, 'hash1', 1000, 2000)
		`)
		return err
	}); err != nil {
		t.Fatalf("insert instance invite failed: %v", err)
	}

	// Valid project invite: project_id set, role valid.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `
			INSERT INTO invites (project_id, email, role, token_hash, created_at, expires_at)
			VALUES (?, 'proj@example.test', 'viewer', 'hash2', 1000, 2000)
		`, projectID)
		return err
	}); err != nil {
		t.Fatalf("insert project invite failed: %v", err)
	}

	// Invalid: project_id NULL with non-NULL role.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `
			INSERT INTO invites (project_id, email, role, token_hash, created_at, expires_at)
			VALUES (NULL, 'bad1@example.test', 'viewer', 'hash3', 1000, 2000)
		`)
		return err
	}); err == nil {
		t.Fatalf("expected CHECK failure for NULL project_id with non-NULL role")
	}

	// Invalid: project_id non-NULL with NULL role.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `
			INSERT INTO invites (project_id, email, role, token_hash, created_at, expires_at)
			VALUES (?, 'bad2@example.test', NULL, 'hash4', 1000, 2000)
		`, projectID)
		return err
	}); err == nil {
		t.Fatalf("expected CHECK failure for non-NULL project_id with NULL role")
	}

	// Invalid: bad role value.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `
			INSERT INTO invites (project_id, email, role, token_hash, created_at, expires_at)
			VALUES (?, 'bad3@example.test', 'superadmin', 'hash5', 1000, 2000)
		`, projectID)
		return err
	}); err == nil {
		t.Fatalf("expected CHECK failure for invalid role")
	}
}

func TestWithTxRollbackAndErrorMapping(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sentinel := errors.New("boom")
	err = st.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES ('k', '"v"', 1000)
		`); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx err = %v, want %v", err, sentinel)
	}

	var v string
	err = st.reader.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'k'").Scan(&v)
	if !errors.Is(mapError(err), ErrNotFound) {
		t.Fatalf("expected ErrNotFound after rollback, got %v", err)
	}
}

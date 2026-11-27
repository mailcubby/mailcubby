package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	// Register the pure-Go modernc.org/sqlite database/sql driver for PRAGMA integrity_check.
	_ "modernc.org/sqlite"

	"github.com/mailcubby/mailcubby/internal/cli"
	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
)

const testBackupProjectSlug = "acme-staging"

func seedLiveBackupStore(t *testing.T, dataDir string) (*store.Store, store.Project) {
	t.Helper()

	ctx := t.Context()
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	st, err := store.Open(ctx, filepath.Join(dataDir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.CreateUser(ctx, store.CreateUserParams{
		Email:             "ops@example.test",
		Name:              "Ops Admin",
		IsAdmin:           true,
		CanCreateProjects: true,
		Now:               now,
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:  testBackupProjectSlug,
		Name:  "Acme Staging",
		Color: "blue",
		Now:   now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		msg, err := tx.InsertMessage(ctx, store.InsertMessageParams{
			ID:           "01JBACUP000000000000000001",
			ProjectID:    proj.ID,
			ReceivedAt:   now,
			Subject:      "Fatura de João",
			FromName:     "Billing",
			FromAddr:     "billing@acme.test",
			To:           []store.Address{{Name: "Customer", Address: "cust@acme.test"}},
			EnvelopeFrom: "billing@acme.test",
			EnvelopeTo:   []string{"cust@acme.test"},
			Snippet:      "Your invoice is ready",
			HasText:      true,
			SizeBytes:    512,
			BlobPath:     "1/2026/11/01JBACUP000000000000000001.eml.zst",
		})
		if err != nil {
			return err
		}
		return search.IndexTx(ctx, tx, msg.PK, search.Document{
			Subject:  msg.Subject,
			FromText: "Billing billing@acme.test",
			ToText:   "Customer cust@acme.test",
			Body:     "Your invoice is ready for download",
		})
	}); err != nil {
		t.Fatalf("seed message + FTS: %v", err)
	}

	return st, proj
}

func verifyBackupDB(t *testing.T, backupPath string) {
	t.Helper()
	ctx := t.Context()

	// Verify standalone SQLite integrity_check.
	rawDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", backupPath, err)
	}
	defer func() { _ = rawDB.Close() }()

	var integrity string
	if err := rawDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("PRAGMA integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("PRAGMA integrity_check = %q, want ok", integrity)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw backup db: %v", err)
	}

	// Open backed-up DB via store.Store and verify schema, data, and FTS5.
	restored, err := store.Open(ctx, backupPath)
	if err != nil {
		t.Fatalf("store.Open(backup): %v", err)
	}
	defer func() { _ = restored.Close() }()

	if err := restored.MigrationsApplied(ctx); err != nil {
		t.Fatalf("MigrationsApplied on backup: %v", err)
	}

	user, err := restored.GetUserByEmail(ctx, "ops@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail on backup: %v", err)
	}
	if user.Name != "Ops Admin" {
		t.Fatalf("restored user name = %q, want Ops Admin", user.Name)
	}

	proj, err := restored.GetProjectBySlug(ctx, testBackupProjectSlug)
	if err != nil {
		t.Fatalf("GetProjectBySlug on backup: %v", err)
	}

	msg, err := restored.GetMessageByID(ctx, proj.ID, "01JBACUP000000000000000001")
	if err != nil {
		t.Fatalf("GetMessageByID on backup: %v", err)
	}
	if msg.Subject != "Fatura de João" {
		t.Fatalf("restored subject = %q, want %q", msg.Subject, "Fatura de João")
	}

	// Verify FTS5 index survived VACUUM INTO intact (diacritic-insensitive match).
	q, err := search.Parse("joao")
	if err != nil {
		t.Fatalf("search.Parse: %v", err)
	}
	found, err := search.Messages(ctx, restored, proj.ID, q, search.BuildOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search.Messages on backup: %v", err)
	}
	if len(found) != 1 || found[0].ID != "01JBACUP000000000000000001" {
		t.Fatalf("FTS search on backup returned %d messages (%+v), want 1 matching message", len(found), found)
	}
}

// FR-115: Online backup while live WAL store has committed and active uncommitted writes.
func TestBackupOnlineWALAndUncommittedWrite(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	outDir := t.TempDir()
	st, proj := seedLiveBackupStore(t, dataDir)

	// Open an uncommitted write transaction in a separate goroutine to prove
	// VACUUM INTO takes a consistent WAL snapshot without blocking on writers
	// and without leaking uncommitted rows.
	txStarted := make(chan struct{})
	releaseTx := make(chan struct{})
	txErrCh := make(chan error, 1)
	go func() {
		txErrCh <- st.WithTx(context.Background(), func(tx *store.Tx) error {
			if _, err := tx.InsertMessage(context.Background(), store.InsertMessageParams{
				ID:           "01JBACUP000000000000000099",
				ProjectID:    proj.ID,
				ReceivedAt:   time.Now().UTC(),
				Subject:      "Uncommitted draft",
				EnvelopeFrom: "draft@acme.test",
				EnvelopeTo:   []string{"x@acme.test"},
				BlobPath:     "1/2026/11/01JBACUP000000000000000099.eml.zst",
			}); err != nil {
				return err
			}
			close(txStarted)
			<-releaseTx
			// Return a sentinel error so the transaction rolls back.
			return os.ErrInvalid
		})
	}()
	<-txStarted

	dstPath := filepath.Join(outDir, "app.db.bak")
	env := func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return dataDir, true
		}
		return "", false
	}

	var stdout, stderr bytes.Buffer
	code := cli.RunBackup(t.Context(), []string{"--out", dstPath}, &stdout, &stderr, env)
	close(releaseTx)
	<-txErrCh

	if code != 0 {
		t.Fatalf("RunBackup exit=%d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), dstPath) {
		t.Fatalf("stdout = %q, want destination path mentioned", stdout.String())
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dstPath)
		if err != nil {
			t.Fatalf("stat backup: %v", err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("backup file mode = %o, want 600", got)
		}
	}

	// Ensure no leftover staging directories remain in outDir.
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir(outDir): %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "app.db.bak" {
		t.Fatalf("unexpected entries in outDir: %+v", entries)
	}

	verifyBackupDB(t, dstPath)

	// Confirm uncommitted row is absent from the backup.
	restored, err := store.Open(t.Context(), dstPath)
	if err != nil {
		t.Fatalf("store.Open(backup): %v", err)
	}
	defer func() { _ = restored.Close() }()
	if _, err := restored.GetMessageByID(t.Context(), proj.ID, "01JBACUP000000000000000099"); err == nil {
		t.Fatal("uncommitted message appeared in backup")
	}
}

// FR-115: Refuses to overwrite existing destination by default; allows with --overwrite.
func TestBackupOverwriteProtection(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	outDir := t.TempDir()
	_, _ = seedLiveBackupStore(t, dataDir)

	dstPath := filepath.Join(outDir, "existing.db")
	const sentinel = "do-not-clobber-me"
	if err := os.WriteFile(dstPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.RunBackup(t.Context(), []string{"--data-dir", dataDir, "--out", dstPath}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected exit 1 on existing file, got %d (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("stderr = %q, want 'already exists'", stderr.String())
	}

	//nolint:gosec // G304: dstPath is inside t.TempDir()
	gotBytes, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("ReadFile sentinel: %v", err)
	}
	if string(gotBytes) != sentinel {
		t.Fatalf("existing destination was modified: got %q, want %q", string(gotBytes), sentinel)
	}

	// With --overwrite, replacement succeeds atomically with mode 0600.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunBackup(t.Context(), []string{"--data-dir", dataDir, "--out", dstPath, "--overwrite"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected exit 0 with --overwrite, got %d (stderr: %q)", code, stderr.String())
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dstPath)
		if err != nil {
			t.Fatalf("stat overwritten backup: %v", err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("overwritten backup mode = %o, want 600", got)
		}
	}
	verifyBackupDB(t, dstPath)
}

// FR-115: Refuses missing source app.db without creating any files in dataDir.
func TestBackupMissingSourceDB(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	outDir := t.TempDir()
	dstPath := filepath.Join(outDir, "backup.db")

	for _, args := range [][]string{
		{"--data-dir", dataDir, "--out", dstPath},
		{"--data-dir", dataDir, "--out", "-"},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.RunBackup(t.Context(), args, &stdout, &stderr, nil)
		if code != 1 {
			t.Fatalf("args=%v: exit=%d, want 1 (stderr: %q)", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "does not exist") {
			t.Fatalf("args=%v: stderr=%q, want 'does not exist'", args, stderr.String())
		}
	}

	// Confirm dataDir is still completely empty (no app.db, app.key, or tmp/).
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("ReadDir(dataDir): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dataDir was modified on missing source DB: %+v", entries)
	}
	if _, err := os.Stat(dstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dstPath should not exist on error, stat err=%v", err)
	}
}

// FR-115: --out - streams valid SQLite database bytes to stdout and cleans up tmp/.
func TestBackupStdoutStreaming(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	_, _ = seedLiveBackupStore(t, dataDir)

	env := func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return dataDir, true
		}
		return "", false
	}

	var stdout, stderr bytes.Buffer
	code := cli.RunBackup(t.Context(), []string{"--out", "-"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("RunBackup(--out -) exit=%d, want 0 (stderr: %q)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr on stdout backup: %q", stderr.String())
	}

	raw := stdout.Bytes()
	const sqliteHeader = "SQLite format 3\x00"
	if len(raw) < len(sqliteHeader) || string(raw[:len(sqliteHeader)]) != sqliteHeader {
		t.Fatalf("stdout does not start with SQLite header (len=%d)", len(raw))
	}

	// Verify temp staging directory inside dataDir/tmp/ was cleaned up.
	tmpEntries, err := os.ReadDir(filepath.Join(dataDir, "tmp"))
	if err != nil {
		t.Fatalf("ReadDir(tmp): %v", err)
	}
	if len(tmpEntries) != 0 {
		t.Fatalf("leftover entries in dataDir/tmp after stdout backup: %+v", tmpEntries)
	}

	// Write streamed bytes to a file and verify integrity + contents + FTS.
	restoredPath := filepath.Join(t.TempDir(), "streamed.db")
	if err := os.WriteFile(restoredPath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile streamed backup: %v", err)
	}
	verifyBackupDB(t, restoredPath)
}

func TestBackupSafetyGuards(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	seedLiveBackupStore(t, dataDir)

	// Refuses overwriting the live database file or its WAL/SHM companions.
	for _, target := range []string{
		filepath.Join(dataDir, store.DBFilename),
		filepath.Join(dataDir, store.DBFilename+"-wal"),
		filepath.Join(dataDir, store.DBFilename+"-shm"),
	} {
		var stdout, stderr bytes.Buffer
		code := cli.RunBackup(t.Context(), []string{"--data-dir", dataDir, "--out", target, "--overwrite"}, &stdout, &stderr, nil)
		if code != 1 {
			t.Fatalf("target=%q: exit=%d, want 1 (stderr: %q)", target, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "live database") {
			t.Fatalf("target=%q: stderr=%q, want 'live database'", target, stderr.String())
		}
	}

	// Refuses writing to an existing directory even with --overwrite.
	existingDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.RunBackup(t.Context(), []string{"--data-dir", dataDir, "--out", existingDir, "--overwrite"}, &stdout, &stderr, nil)
	if code != 1 || !strings.Contains(stderr.String(), "directory") {
		t.Fatalf("dir target = (%d, %q), want (1, contains directory)", code, stderr.String())
	}
}

func TestBackupFlagsAndHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{
			name:     "missing --out",
			args:     nil,
			wantCode: 2,
			wantErr:  "--out is required",
		},
		{
			name:     "unknown flag",
			args:     []string{"--bogus"},
			wantCode: 2,
			wantErr:  "flag provided but not defined",
		},
		{
			name:     "extra positional arg",
			args:     []string{"--out", "/tmp/x.db", "extra"},
			wantCode: 2,
			wantErr:  "takes no positional arguments",
		},
		{
			name:     "help flag",
			args:     []string{"--help"},
			wantCode: 0,
			wantOut:  "Usage: mailcubby backup",
		},
		{
			name:     "short help flag",
			args:     []string{"-h"},
			wantCode: 0,
			wantOut:  "--out",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := cli.RunBackup(t.Context(), tc.args, &stdout, &stderr, nil)
			if code != tc.wantCode {
				t.Fatalf("exit=%d, want %d (stdout=%q stderr=%q)", code, tc.wantCode, stdout.String(), stderr.String())
			}
			if tc.wantOut != "" && !strings.Contains(stdout.String(), tc.wantOut) {
				t.Fatalf("stdout=%q, want substring %q", stdout.String(), tc.wantOut)
			}
			if tc.wantErr != "" && !strings.Contains(stderr.String(), tc.wantErr) {
				t.Fatalf("stderr=%q, want substring %q", stderr.String(), tc.wantErr)
			}
		})
	}
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// backupDSNQuery omits query_only(1) because VACUUM INTO writes to the newly
// created target database file on the same SQLite connection (query_only would
// reject it with SQLITE_READONLY), while keeping busy_timeout so concurrent
// writers in WAL mode never cause spurious SQLITE_BUSY errors.
const backupDSNQuery = "_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"

// Backup creates a consistent, compacted online backup of the SQLite database at
// srcDBPath into dstPath using SQLite's VACUUM INTO statement. It is safe to
// run while another process has srcDBPath open in WAL mode.
//
// srcDBPath must already exist (Backup never creates an empty source database).
// dstPath must not already exist; callers that support atomic replacement or
// streaming should pass a fresh staging path inside a 0700 temporary directory.
// On success, dstPath has mode 0600 and has been fsynced to disk.
func Backup(ctx context.Context, srcDBPath, dstPath string) error {
	if strings.TrimSpace(srcDBPath) == "" {
		return errors.New("store: source db path must not be empty")
	}
	if strings.TrimSpace(dstPath) == "" {
		return errors.New("store: backup destination path must not be empty")
	}

	srcAbs, err := filepath.Abs(srcDBPath)
	if err != nil {
		return fmt.Errorf("store: resolve source db path %q: %w", srcDBPath, err)
	}
	dstAbs, err := filepath.Abs(dstPath)
	if err != nil {
		return fmt.Errorf("store: resolve backup destination path %q: %w", dstPath, err)
	}

	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("store: database %q does not exist: %w", srcAbs, err)
		}
		return fmt.Errorf("store: stat source db %q: %w", srcAbs, err)
	}
	if srcInfo.IsDir() {
		return fmt.Errorf("store: source db %q is a directory", srcAbs)
	}

	_, statDstErr := os.Lstat(dstAbs)
	if statDstErr == nil {
		return fmt.Errorf("store: backup destination %q already exists: %w", dstAbs, os.ErrExist)
	}
	if !errors.Is(statDstErr, os.ErrNotExist) {
		return fmt.Errorf("store: stat backup destination %q: %w", dstAbs, statDstErr)
	}

	db, err := sql.Open("sqlite", buildDSN(srcAbs, backupDSNQuery))
	if err != nil {
		return fmt.Errorf("store: open source db for backup: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dstAbs); err != nil {
		cleanupBackupArtifacts(dstAbs)
		return fmt.Errorf("store: vacuum into %q: %w", dstAbs, err)
	}

	if err := os.Chmod(dstAbs, dbFileMode); err != nil {
		cleanupBackupArtifacts(dstAbs)
		return fmt.Errorf("store: chmod backup %q: %w", dstAbs, err)
	}

	if err := syncFile(dstAbs); err != nil {
		cleanupBackupArtifacts(dstAbs)
		return fmt.Errorf("store: sync backup %q: %w", dstAbs, err)
	}

	return nil
}

func cleanupBackupArtifacts(dstAbs string) {
	for _, p := range []string{dstAbs, dstAbs + "-journal", dstAbs + "-wal", dstAbs + "-shm"} {
		_ = os.Remove(p)
	}
}

func syncFile(path string) error {
	//nolint:gosec // G304: path is a validated backup output path
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

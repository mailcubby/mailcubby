// Package store manages the Mailcubby SQLite database, migrations, transactions,
// and typed data access repositories.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	// Register the pure-Go modernc.org/sqlite database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
	"github.com/mailcubby/mailcubby/migrations"
)

const (
	// DBFilename is the SQLite database filename inside MAILCUBBY_DATA_DIR.
	DBFilename = "app.db"

	writerDSNQuery = "_auto_vacuum=INCREMENTAL&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)&_txlock=immediate"
	readerDSNQuery = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)&_pragma=query_only(1)"
)

// Store wraps the SQLite writer and reader handles.
type Store struct {
	writer *sql.DB
	reader *sql.DB
	writeQ *sqlc.Queries
	readQ  *sqlc.Queries
	path   string
}

// Tx represents an active SQLite write transaction.
type Tx struct {
	tx *sql.Tx
	q  *sqlc.Queries
}

// SQLTx returns the underlying *sql.Tx for packages that execute custom
// parameterized statements (such as internal/search FTS indexing and filtered deletes).
func (t *Tx) SQLTx() *sql.Tx {
	return t.tx
}

// ReaderDB returns the read-only *sql.DB pool for packages that execute custom
// parameterized read queries (such as internal/search). Callers must filter every
// tenant table by project_id and pass all user values as bind parameters.
func (s *Store) ReaderDB() *sql.DB {
	return s.reader
}

// MigrationInfo describes the status of one embedded database migration.
type MigrationInfo struct {
	Version   int64
	Source    string
	State     string
	AppliedAt time.Time
}

// Open opens the SQLite database at dbPath, runs embedded goose migrations,
// verifies required PRAGMAs, and opens the read-only connection pool.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("store: db path must not be empty")
	}

	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: resolve db path %q: %w", dbPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("store: create db directory: %w", err)
	}

	_, statErr := os.Stat(absPath)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, fmt.Errorf("store: stat %q: %w", absPath, statErr)
	}

	writer, err := sql.Open("sqlite", buildDSN(absPath, writerDSNQuery))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)

	if err := writer.PingContext(ctx); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("store: ping writer: %w", err)
	}

	if err := runMigrations(ctx, writer); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("store: run migrations: %w", err)
	}

	if err := verifyPragmas(ctx, writer, created); err != nil {
		_ = writer.Close()
		return nil, err
	}

	reader, err := sql.Open("sqlite", buildDSN(absPath, readerDSNQuery))
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	readerConns := max(4, runtime.GOMAXPROCS(0))
	reader.SetMaxOpenConns(readerConns)
	reader.SetMaxIdleConns(readerConns)

	if err := reader.PingContext(ctx); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("store: ping reader: %w", err)
	}

	return &Store{
		writer: writer,
		reader: reader,
		writeQ: sqlc.New(writer),
		readQ:  sqlc.New(reader),
		path:   absPath,
	}, nil
}

// Close closes the reader handle, checkpoints the WAL, and closes the writer handle.
func (s *Store) Close() error {
	var errs []error
	if s.reader != nil {
		if err := s.reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store: close reader: %w", err))
		}
	}
	if s.writer != nil {
		// Best-effort passive WAL checkpoint on clean shutdown.
		_, _ = s.writer.ExecContext(context.Background(), "PRAGMA wal_checkpoint(PASSIVE)")
		if err := s.writer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store: close writer: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Ping verifies both reader and writer handles respond to SELECT 1.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	if err := s.reader.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("store: ping reader: %w", err)
	}
	if err := s.writer.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("store: ping writer: %w", err)
	}
	return nil
}

// MigrationsApplied verifies that all embedded database migrations have been applied.
func (s *Store) MigrationsApplied(ctx context.Context) error {
	provider, err := newGooseProvider(s.reader)
	if err != nil {
		return fmt.Errorf("store: init goose provider: %w", err)
	}
	pending, err := provider.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("store: check pending migrations: %w", err)
	}
	if pending {
		return errors.New("store: database has pending migrations")
	}
	return nil
}

// WithTx executes fn inside a write transaction on the writer handle.
// If fn returns an error or panics, the transaction is rolled back.
func (s *Store) WithTx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()

	if err := fn(&Tx{tx: sqlTx, q: s.writeQ.WithTx(sqlTx)}); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("store: commit tx: %w", err)
	}
	committed = true
	return nil
}

// CheckMigrationStatus reports the status of every embedded migration against dbPath.
// If dbPath does not exist yet, all migrations are reported as pending without creating the file.
func CheckMigrationStatus(ctx context.Context, dbPath string) ([]MigrationInfo, error) {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: resolve db path %q: %w", dbPath, err)
	}

	_, statErr := os.Stat(absPath)
	if errors.Is(statErr, os.ErrNotExist) {
		return pendingMigrationList()
	}
	if statErr != nil {
		return nil, fmt.Errorf("store: stat %q: %w", absPath, statErr)
	}

	db, err := sql.Open("sqlite", buildDSN(absPath, readerDSNQuery))
	if err != nil {
		return nil, fmt.Errorf("store: open db for status: %w", err)
	}
	defer func() { _ = db.Close() }()

	// If the goose version table does not exist yet, all migrations are pending.
	var tableName string
	err = db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return pendingMigrationList()
	}
	if err != nil {
		return nil, fmt.Errorf("store: check goose_db_version: %w", err)
	}

	provider, err := newGooseProvider(db)
	if err != nil {
		return nil, fmt.Errorf("store: init goose provider: %w", err)
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: goose status: %w", err)
	}

	out := make([]MigrationInfo, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, MigrationInfo{
			Version:   st.Source.Version,
			Source:    filepath.Base(st.Source.Path),
			State:     string(st.State),
			AppliedAt: st.AppliedAt,
		})
	}
	return out, nil
}

func pendingMigrationList() ([]MigrationInfo, error) {
	memDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("store: open memory db: %w", err)
	}
	defer func() { _ = memDB.Close() }()

	provider, err := newGooseProvider(memDB)
	if err != nil {
		return nil, fmt.Errorf("store: init goose provider: %w", err)
	}
	sources := provider.ListSources()
	out := make([]MigrationInfo, 0, len(sources))
	for _, src := range sources {
		out = append(out, MigrationInfo{
			Version: src.Version,
			Source:  filepath.Base(src.Path),
			State:   string(goose.StatePending),
		})
	}
	return out, nil
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	provider, err := newGooseProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return err
	}
	return nil
}

func newGooseProvider(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(
		goose.DialectSQLite3,
		db,
		migrations.FS,
		goose.WithLogger(goose.NopLogger()),
		goose.WithDisableGlobalRegistry(true),
	)
}

func verifyPragmas(ctx context.Context, db *sql.DB, created bool) error {
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("store: read PRAGMA journal_mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("store: expected PRAGMA journal_mode=wal, got %q", journalMode)
	}

	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("store: read PRAGMA foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("store: expected PRAGMA foreign_keys=1, got %d", foreignKeys)
	}

	if created {
		var autoVacuum int
		if err := db.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
			return fmt.Errorf("store: read PRAGMA auto_vacuum: %w", err)
		}
		if autoVacuum != 2 {
			return fmt.Errorf("store: expected PRAGMA auto_vacuum=2 (INCREMENTAL), got %d", autoVacuum)
		}
	}

	return nil
}

func buildDSN(absPath, rawQuery string) string {
	u := &url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(absPath),
		RawQuery: rawQuery,
	}
	return u.String()
}

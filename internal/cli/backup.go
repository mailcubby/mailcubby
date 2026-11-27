package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mailcubby/mailcubby/internal/store"
)

const backupDirPerm = 0o700

var liveDBCompanionSuffixes = [...]string{"", "-wal", "-shm", "-journal"}

type readerFunc func([]byte) (int, error)

func (fn readerFunc) Read(p []byte) (int, error) {
	return fn(p)
}

// RunBackup implements "mailcubby backup --out <path|-> [--data-dir path] [--overwrite]"
// (SPEC FR-115). It creates a consistent online SQLite backup of app.db using
// VACUUM INTO, safe to run while "mailcubby serve" is active in WAL mode.
func RunBackup(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	dataDirDefault := defaultDataDir
	if lookupEnv != nil {
		if v, ok := lookupEnv("MAILCUBBY_DATA_DIR"); ok && strings.TrimSpace(v) != "" {
			dataDirDefault = strings.TrimSpace(v)
		}
	}

	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	outFlag := fs.String("out", "", "destination file path, or '-' to stream to stdout")
	dataDirFlag := fs.String("data-dir", dataDirDefault, "data directory containing app.db")
	overwriteFlag := fs.Bool("overwrite", false, "replace destination file if it already exists")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printBackupUsage(stdout)
			return 0
		}
		printBackupUsage(stderr)
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "backup takes no positional arguments")
		printBackupUsage(stderr)
		return 2
	}

	outPath := strings.TrimSpace(*outFlag)
	if outPath == "" {
		_, _ = fmt.Fprintln(stderr, "backup: --out is required (pass a file path or '-' for stdout)")
		printBackupUsage(stderr)
		return 2
	}

	dataDir := strings.TrimSpace(*dataDirFlag)
	if dataDir == "" {
		_, _ = fmt.Fprintln(stderr, "backup: --data-dir must not be empty")
		return 2
	}

	srcDBPath := filepath.Join(dataDir, store.DBFilename)
	srcAbs, err := filepath.Abs(srcDBPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backup: resolve database path: %v\n", err)
		return 1
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(stderr, "backup: database %q does not exist\n", srcAbs)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "backup: stat database %q: %v\n", srcAbs, err)
		return 1
	}
	if srcInfo.IsDir() {
		_, _ = fmt.Fprintf(stderr, "backup: %q is a directory, not a database file\n", srcAbs)
		return 1
	}

	if outPath == "-" {
		if err := backupToWriter(ctx, dataDir, srcAbs, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "backup: %v\n", err)
			return 1
		}
		return 0
	}

	writtenBytes, err := backupToFile(ctx, srcAbs, srcInfo, outPath, *overwriteFlag)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backup: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "backed up %s (%d bytes)\n", outPath, writtenBytes)
	return 0
}

func printBackupUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: mailcubby backup --out <path|-> [--data-dir path] [--overwrite]

Create a consistent online backup of the SQLite database (app.db) using
VACUUM INTO. Safe to run while 'mailcubby serve' is active in WAL mode.

Flags:
  --out <path|->     Destination file path, or '-' to stream raw SQLite bytes to stdout (required)
  --data-dir <path>  Data directory containing app.db (default: MAILCUBBY_DATA_DIR or ./data)
  --overwrite        Replace an existing destination file instead of failing
  -h, --help         Show this help and exit

Notes:
  - Back up the blobs/ directory and MAILCUBBY_APP_KEY separately.
  - Docker Compose example:
      docker compose -f deploy/compose.server.yml exec -T mailcubby /mailcubby backup --out - > app.db
`)
}

func backupToWriter(ctx context.Context, dataDir, srcAbs string, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tmpBase := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tmpBase, backupDirPerm); err != nil {
		return fmt.Errorf("create tmp directory %q: %w", tmpBase, err)
	}

	tmpDir, err := os.MkdirTemp(tmpBase, "backup-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	stagePath := filepath.Join(tmpDir, store.DBFilename)
	if err := store.Backup(ctx, srcAbs, stagePath); err != nil {
		return err
	}

	//nolint:gosec // G304: stagePath is created inside our private 0700 tmpDir
	f, err := os.Open(stagePath)
	if err != nil {
		return fmt.Errorf("open staged backup: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Wrap f so io.Copy reads in chunks and stops promptly on context cancellation.
	ctxReader := readerFunc(func(p []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return f.Read(p)
	})
	if _, err := io.Copy(w, ctxReader); err != nil {
		return fmt.Errorf("stream backup to stdout: %w", err)
	}
	return nil
}

func backupToFile(ctx context.Context, srcAbs string, srcInfo os.FileInfo, outPath string, overwrite bool) (int64, error) {
	dstAbs, err := filepath.Abs(outPath)
	if err != nil {
		return 0, fmt.Errorf("resolve destination %q: %w", outPath, err)
	}

	if err := validateBackupDestination(srcAbs, srcInfo, dstAbs, outPath, overwrite); err != nil {
		return 0, err
	}

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	dstDir := filepath.Dir(dstAbs)
	tmpDir, err := os.MkdirTemp(dstDir, ".mailcubby-backup-*")
	if err != nil {
		return 0, fmt.Errorf("create staging directory in %q: %w", dstDir, err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	stagePath := filepath.Join(tmpDir, store.DBFilename)
	if err := store.Backup(ctx, srcAbs, stagePath); err != nil {
		return 0, err
	}

	fi, err := os.Stat(stagePath)
	if err != nil {
		return 0, fmt.Errorf("stat staged backup: %w", err)
	}

	if overwrite {
		if err := os.Rename(stagePath, dstAbs); err != nil {
			return 0, fmt.Errorf("move backup to %q: %w", outPath, err)
		}
	} else {
		if err := linkNoClobber(stagePath, dstAbs, outPath); err != nil {
			return 0, err
		}
	}

	// Best-effort directory fsync so the directory entry survives crashes.
	_ = syncDirBestEffort(dstDir)
	return fi.Size(), nil
}

func validateBackupDestination(srcAbs string, srcInfo os.FileInfo, dstAbs, outPath string, overwrite bool) error {
	protectedPaths, protectedInfos := collectProtectedLiveDB(srcAbs, srcInfo)

	if matchesProtectedPath(dstAbs, protectedPaths) {
		return fmt.Errorf("destination %q must not be the live database file", outPath)
	}

	dstInfo, lstatErr := os.Lstat(dstAbs)
	if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return fmt.Errorf("stat destination %q: %w", outPath, lstatErr)
	}
	if lstatErr == nil {
		if sameAsAnyFile(dstInfo, protectedInfos) {
			return fmt.Errorf("destination %q must not be the live database file", outPath)
		}
		if dstInfo.IsDir() {
			return fmt.Errorf("destination %q is a directory", outPath)
		}
		if dstInfo.Mode()&os.ModeSymlink != 0 {
			if targetReal, err := filepath.EvalSymlinks(dstAbs); err == nil {
				if matchesProtectedPath(targetReal, protectedPaths) {
					return fmt.Errorf("destination %q must not point to the live database file", outPath)
				}
			}
			if linkTarget, err := os.Readlink(dstAbs); err == nil {
				if !filepath.IsAbs(linkTarget) {
					linkTarget = filepath.Join(filepath.Dir(dstAbs), linkTarget)
				}
				if matchesProtectedPath(filepath.Clean(linkTarget), protectedPaths) {
					return fmt.Errorf("destination %q must not point to the live database file", outPath)
				}
			}
			if targetInfo, statErr := os.Stat(dstAbs); statErr == nil {
				if sameAsAnyFile(targetInfo, protectedInfos) {
					return fmt.Errorf("destination %q must not point to the live database file", outPath)
				}
				if targetInfo.IsDir() {
					return fmt.Errorf("destination %q is a directory", outPath)
				}
			}
		}
		if !overwrite {
			return fmt.Errorf("destination %q already exists (use --overwrite to replace)", outPath)
		}
	}

	return nil
}

func collectProtectedLiveDB(srcAbs string, srcInfo os.FileInfo) (map[string]struct{}, []os.FileInfo) {
	bases := make([]string, 0, 3)
	addBase := func(p string) {
		if p == "" {
			return
		}
		for _, existing := range bases {
			if existing == p {
				return
			}
		}
		bases = append(bases, p)
	}

	addBase(srcAbs)
	if srcReal, err := filepath.EvalSymlinks(srcAbs); err == nil {
		addBase(srcReal)
	}
	if srcDirReal, err := filepath.EvalSymlinks(filepath.Dir(srcAbs)); err == nil {
		addBase(filepath.Join(srcDirReal, filepath.Base(srcAbs)))
	}

	paths := make(map[string]struct{}, len(bases)*len(liveDBCompanionSuffixes))
	infos := make([]os.FileInfo, 0, len(liveDBCompanionSuffixes)+1)
	if srcInfo != nil {
		infos = append(infos, srcInfo)
	}

	for _, base := range bases {
		for _, suffix := range liveDBCompanionSuffixes {
			p := base + suffix
			if _, seen := paths[p]; seen {
				continue
			}
			paths[p] = struct{}{}
			if info, err := os.Stat(p); err == nil {
				infos = append(infos, info)
			}
		}
	}
	return paths, infos
}

func matchesProtectedPath(p string, protected map[string]struct{}) bool {
	if _, ok := protected[p]; ok {
		return true
	}
	if dirReal, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		if _, ok := protected[filepath.Join(dirReal, filepath.Base(p))]; ok {
			return true
		}
	}
	return false
}

func sameAsAnyFile(target os.FileInfo, protected []os.FileInfo) bool {
	if target == nil {
		return false
	}
	for _, info := range protected {
		if os.SameFile(info, target) {
			return true
		}
	}
	return false
}

// linkNoClobber atomically places stagePath at dstAbs without overwriting an
// existing entry. It prefers os.Link (atomic fail-on-exist) and falls back to
// an Lstat check + os.Rename on filesystems that do not support hard links.
func linkNoClobber(stagePath, dstAbs, displayPath string) error {
	err := os.Link(stagePath, dstAbs)
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("destination %q already exists (use --overwrite to replace)", displayPath)
	}

	// Fallback for filesystems without hard-link support.
	_, lstatErr := os.Lstat(dstAbs)
	if lstatErr == nil {
		return fmt.Errorf("destination %q already exists (use --overwrite to replace)", displayPath)
	}
	if !errors.Is(lstatErr, os.ErrNotExist) {
		return fmt.Errorf("stat destination %q: %w", displayPath, lstatErr)
	}
	if renameErr := os.Rename(stagePath, dstAbs); renameErr != nil {
		return fmt.Errorf("move backup to %q: %w", displayPath, renameErr)
	}
	return nil
}

func syncDirBestEffort(dir string) error {
	//nolint:gosec // G304: dir is the parent directory of the validated destination path
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

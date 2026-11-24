// Package blob manages zstd-compressed raw email files on disk under
// $MAILCUBBY_DATA_DIR/blobs/ and temporary spool files under tmp/.
package blob

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/oklog/ulid/v2"
)

const (
	blobsDirName = "blobs"
	tmpDirName   = "tmp"
	blobExt      = ".eml.zst"
	dirPerm      = 0o700
	filePerm     = 0o600
)

// Entry describes one blob file discovered during Walk.
type Entry struct {
	RelPath   string
	ProjectID int64
	MessageID ulid.ULID
	ModTime   time.Time
	Size      int64
}

// Store manages zstd-compressed raw message blobs and temporary spools.
type Store struct {
	blobsDir string
	tmpDir   string
	encLevel zstd.EncoderLevel
	decPool  sync.Pool

	mu        sync.Mutex
	spoolEnt  io.Reader
	lastSpool time.Time
}

// NewStore initializes blob storage under dataDir using the given zstd level (1..19).
func NewStore(dataDir string, zstdLevel int) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("blob: data dir must not be empty")
	}

	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("blob: resolve data dir %q: %w", dataDir, err)
	}

	blobsDir := filepath.Join(absDir, blobsDirName)
	tmpDir := filepath.Join(absDir, tmpDirName)

	if err := os.MkdirAll(blobsDir, dirPerm); err != nil {
		return nil, fmt.Errorf("blob: create blobs dir: %w", err)
	}
	if err := os.MkdirAll(tmpDir, dirPerm); err != nil {
		return nil, fmt.Errorf("blob: create tmp dir: %w", err)
	}

	s := &Store{
		blobsDir: blobsDir,
		tmpDir:   tmpDir,
		encLevel: zstd.EncoderLevelFromZstd(zstdLevel),
		spoolEnt: ulid.Monotonic(rand.Reader, 0),
	}
	return s, nil
}

// CleanTmp removes any leftover temporary spool files in tmp/ (called at startup, SPEC 11).
func (s *Store) CleanTmp() error {
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return fmt.Errorf("blob: read tmp dir: %w", err)
	}
	var errs []error
	for _, entry := range entries {
		p := filepath.Join(s.tmpDir, entry.Name())
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fmt.Errorf("blob: remove tmp entry %q: %w", entry.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// CleanTmpOlderThan removes temporary entries in tmp/ whose modification time is
// strictly before cutoff (FR-098, ARCHITECTURE 3.12).
func (s *Store) CleanTmpOlderThan(cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return 0, fmt.Errorf("blob: read tmp dir: %w", err)
	}
	var (
		removed int
		errs    []error
	)
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, os.ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("blob: stat tmp entry %q: %w", entry.Name(), infoErr))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		p := filepath.Join(s.tmpDir, entry.Name())
		if rmErr := os.RemoveAll(p); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("blob: remove stale tmp entry %q: %w", entry.Name(), rmErr))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// ListProjectDirs returns the numeric project IDs of top-level directories under blobs/.
func (s *Store) ListProjectDirs() ([]int64, error) {
	entries, err := os.ReadDir(s.blobsDir)
	if err != nil {
		return nil, fmt.Errorf("blob: read blobs dir: %w", err)
	}
	out := make([]int64, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		id, err := strconv.ParseInt(name, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != name {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// TotalDiskBytes sums the compressed on-disk size in bytes of every valid blob under blobs/.
func (s *Store) TotalDiskBytes(ctx context.Context) (int64, error) {
	var total int64
	err := s.Walk(ctx, func(e Entry) error {
		total += e.Size
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// BlobsDir returns the absolute path to the blobs directory.
func (s *Store) BlobsDir() string {
	return s.blobsDir
}

// TmpDir returns the absolute path to the tmp spool directory.
func (s *Store) TmpDir() string {
	return s.tmpDir
}

// Spool represents an in-progress or finalized zstd-compressed temporary file in tmp/.
type Spool struct {
	store     *Store
	path      string
	file      *os.File
	enc       *zstd.Encoder
	hasher    hash.Hash
	size      int64
	sha256Hex string
	closed    bool
	cleaned   bool
}

// Create opens a new temporary spool file under tmp/ with a streaming zstd encoder.
func (s *Store) Create(ctx context.Context) (*Spool, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("blob: create spool: %w", err)
	}

	id, err := s.nextSpoolID()
	if err != nil {
		return nil, fmt.Errorf("blob: generate spool id: %w", err)
	}

	path := filepath.Join(s.tmpDir, id.String()+".tmp")
	//nolint:gosec // G304: path is built from store tmpDir and a freshly generated ULID
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm)
	if err != nil {
		return nil, fmt.Errorf("blob: create spool file: %w", err)
	}

	enc, err := zstd.NewWriter(f,
		zstd.WithEncoderLevel(s.encLevel),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("blob: create zstd encoder: %w", err)
	}

	return &Spool{
		store:  s,
		path:   path,
		file:   f,
		enc:    enc,
		hasher: sha256.New(),
	}, nil
}

// Write compresses p into the spool while tracking uncompressed size and SHA-256.
func (sp *Spool) Write(p []byte) (int, error) {
	if sp.closed {
		return 0, errors.New("blob: write to closed spool")
	}
	n, err := sp.enc.Write(p)
	if n > 0 {
		sp.size += int64(n)
		_, _ = sp.hasher.Write(p[:n])
	}
	if err != nil {
		return n, fmt.Errorf("blob: write spool: %w", err)
	}
	return n, nil
}

// CopyFrom streams all bytes from r into the spool, preserving read errors from r
// (such as smtp.ErrDataTooLarge) via wrapping.
func (sp *Spool) CopyFrom(ctx context.Context, r io.Reader) error {
	if sp.closed {
		return errors.New("blob: copy into closed spool")
	}
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("blob: spool cancelled: %w", err)
		}
		nr, rerr := r.Read(buf)
		if nr > 0 {
			if _, werr := sp.Write(buf[:nr]); werr != nil {
				return werr
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return fmt.Errorf("blob: read source: %w", rerr)
		}
	}
}

// CloseWrite flushes and closes the zstd stream, fsyncs the temp file, and closes
// the write handle so the spool can be reopened or placed.
func (sp *Spool) CloseWrite() error {
	if sp.closed {
		return nil
	}
	sp.closed = true

	var errs []error
	if sp.enc != nil {
		if err := sp.enc.Close(); err != nil {
			errs = append(errs, fmt.Errorf("blob: close zstd encoder: %w", err))
		}
		sp.enc = nil
	}
	if sp.file != nil {
		if err := sp.file.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("blob: sync spool file: %w", err))
		}
		if err := sp.file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("blob: close spool file: %w", err))
		}
		sp.file = nil
	}
	sp.sha256Hex = hex.EncodeToString(sp.hasher.Sum(nil))
	return errors.Join(errs...)
}

// Size returns the uncompressed byte count written to the spool.
func (sp *Spool) Size() int64 {
	return sp.size
}

// SHA256 returns the lowercase hex SHA-256 digest of the uncompressed bytes.
// Valid after CloseWrite.
func (sp *Spool) SHA256() string {
	if sp.sha256Hex != "" {
		return sp.sha256Hex
	}
	return hex.EncodeToString(sp.hasher.Sum(nil))
}

// Open opens the finalized spool file for streaming decompression.
func (sp *Spool) Open() (io.ReadCloser, error) {
	if !sp.closed {
		return nil, errors.New("blob: cannot open spool before CloseWrite")
	}
	return sp.store.openPath(sp.path)
}

// Cleanup closes any open write handles and removes the temporary spool file.
// It is safe to call multiple times.
func (sp *Spool) Cleanup() error {
	if sp.cleaned {
		return nil
	}
	sp.cleaned = true

	var errs []error
	if !sp.closed {
		sp.closed = true
		if sp.enc != nil {
			_ = sp.enc.Close()
			sp.enc = nil
		}
		if sp.file != nil {
			_ = sp.file.Close()
			sp.file = nil
		}
	}
	if err := os.Remove(sp.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("blob: remove spool %q: %w", sp.path, err))
	}
	return errors.Join(errs...)
}

// Place links (falling back to copy) the finalized spool into its canonical blob
// path blobs/<project_id>/<yyyy>/<mm>/<ulid>.eml.zst and fsyncs the target directory.
// It returns the slash-separated path relative to blobs/.
func (s *Store) Place(spool *Spool, projectID int64, id ulid.ULID) (string, error) {
	if spool == nil || !spool.closed {
		return "", errors.New("blob: spool must be finalized with CloseWrite before Place")
	}
	relPath, err := BuildRelPath(projectID, id)
	if err != nil {
		return "", err
	}

	dstPath := filepath.Join(s.blobsDir, filepath.FromSlash(relPath))
	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, dirPerm); err != nil {
		return "", fmt.Errorf("blob: create destination dir: %w", err)
	}

	if err := os.Link(spool.path, dstPath); err != nil {
		if copyErr := copyFileAtomic(spool.path, dstPath); copyErr != nil {
			return "", fmt.Errorf("blob: link (%w) and copy fallback failed: %w", err, copyErr)
		}
	}

	if err := syncDir(dstDir); err != nil {
		return "", fmt.Errorf("blob: sync destination dir: %w", err)
	}
	return relPath, nil
}

// Open validates relPath and returns a streaming zstd-decompressing reader.
func (s *Store) Open(relPath string) (io.ReadCloser, error) {
	if _, _, err := ValidateRelPath(relPath); err != nil {
		return nil, err
	}
	fullPath := filepath.Join(s.blobsDir, filepath.FromSlash(relPath))
	return s.openPath(fullPath)
}

// Remove validates relPath and deletes the blob file. Missing files are not errors (SPEC 11).
func (s *Store) Remove(relPath string) error {
	if _, _, err := ValidateRelPath(relPath); err != nil {
		return err
	}
	fullPath := filepath.Join(s.blobsDir, filepath.FromSlash(relPath))
	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blob: remove %q: %w", relPath, err)
	}
	return nil
}

// RemoveProject removes the entire blobs/<project_id>/ subtree for a deleted project.
func (s *Store) RemoveProject(projectID int64) error {
	if projectID <= 0 {
		return fmt.Errorf("blob: invalid project id %d", projectID)
	}
	projDir := filepath.Join(s.blobsDir, strconv.FormatInt(projectID, 10))
	if err := os.RemoveAll(projDir); err != nil {
		return fmt.Errorf("blob: remove project %d dir: %w", projectID, err)
	}
	return nil
}

// Walk traverses every blob file under blobs/ and invokes fn with its parsed metadata.
func (s *Store) Walk(ctx context.Context, fn func(Entry) error) error {
	return filepath.WalkDir(s.blobsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.blobsDir, path)
		if err != nil {
			return fmt.Errorf("blob: compute relative path: %w", err)
		}
		relSlash := filepath.ToSlash(rel)
		projectID, msgID, err := ValidateRelPath(relSlash)
		if err != nil {
			// Skip non-blob entries cleanly (none should exist under blobs/).
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("blob: stat %q: %w", relSlash, err)
		}
		return fn(Entry{
			RelPath:   relSlash,
			ProjectID: projectID,
			MessageID: msgID,
			ModTime:   info.ModTime().UTC(),
			Size:      info.Size(),
		})
	})
}

// BuildRelPath constructs the canonical relative blob path from projectID and id.
func BuildRelPath(projectID int64, id ulid.ULID) (string, error) {
	if projectID <= 0 {
		return "", fmt.Errorf("blob: invalid project id %d", projectID)
	}
	var zero ulid.ULID
	if id == zero {
		return "", errors.New("blob: message id must not be zero")
	}
	ts := ulid.Time(id.Time()).UTC()
	return fmt.Sprintf("%d/%04d/%02d/%s%s", projectID, ts.Year(), ts.Month(), id.String(), blobExt), nil
}

// ValidateRelPath verifies relPath matches <project_id>/<yyyy>/<mm>/<ulid>.eml.zst
// and contains no path traversal or unexpected characters (Security Rule 9).
func ValidateRelPath(relPath string) (int64, ulid.ULID, error) {
	if relPath == "" || strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "\\") {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid relative path %q", relPath)
	}
	parts := strings.Split(relPath, "/")
	if len(parts) != 4 {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid relative path %q", relPath)
	}

	projStr, yearStr, monthStr, fileStr := parts[0], parts[1], parts[2], parts[3]

	projectID, err := strconv.ParseInt(projStr, 10, 64)
	if err != nil || projectID <= 0 || strconv.FormatInt(projectID, 10) != projStr {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid project segment in %q", relPath)
	}
	if len(yearStr) != 4 || !isAllDigits(yearStr) {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid year segment in %q", relPath)
	}
	if len(monthStr) != 2 || !isAllDigits(monthStr) {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid month segment in %q", relPath)
	}
	month, _ := strconv.Atoi(monthStr)
	if month < 1 || month > 12 {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid month in %q", relPath)
	}
	if !strings.HasSuffix(fileStr, blobExt) {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid extension in %q", relPath)
	}
	idStr := strings.TrimSuffix(fileStr, blobExt)
	id, err := ulid.ParseStrict(idStr)
	if err != nil || id.String() != idStr {
		return 0, ulid.ULID{}, fmt.Errorf("blob: invalid ulid in %q", relPath)
	}
	return projectID, id, nil
}

func (s *Store) openPath(fullPath string) (io.ReadCloser, error) {
	//nolint:gosec // G304: fullPath is validated by ValidateRelPath or owned by Spool
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("blob: open %q: %w", filepath.Base(fullPath), err)
	}

	var dec *zstd.Decoder
	if pooled := s.decPool.Get(); pooled != nil {
		dec = pooled.(*zstd.Decoder)
		if err := dec.Reset(f); err != nil {
			dec.Close()
			_ = f.Close()
			return nil, fmt.Errorf("blob: reset zstd decoder: %w", err)
		}
	} else {
		dec, err = zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("blob: create zstd decoder: %w", err)
		}
	}

	return &pooledReader{
		store: s,
		file:  f,
		dec:   dec,
	}, nil
}

type pooledReader struct {
	store  *Store
	file   *os.File
	dec    *zstd.Decoder
	closed bool
}

func (r *pooledReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("blob: read from closed reader")
	}
	return r.dec.Read(p)
}

func (r *pooledReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	// Detach from underlying file before returning decoder to the pool.
	if err := r.dec.Reset(nil); err == nil {
		r.store.decPool.Put(r.dec)
	} else {
		r.dec.Close()
	}
	r.dec = nil
	if err := r.file.Close(); err != nil {
		return fmt.Errorf("blob: close file: %w", err)
	}
	return nil
}

func (s *Store) nextSpoolID() (ulid.ULID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if now.Before(s.lastSpool) {
		now = s.lastSpool
	}
	s.lastSpool = now
	return ulid.New(ulid.Timestamp(now), s.spoolEnt)
}

func copyFileAtomic(srcPath, dstPath string) error {
	//nolint:gosec // G304: srcPath is owned by Spool
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = src.Close() }()

	tmpPath := dstPath + ".copy"
	//nolint:gosec // G304: tmpPath is derived from validated dstPath
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("create copy target: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copy bytes: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync copy target: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close copy target: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename copy target: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	//nolint:gosec // G304: dir is derived from validated store paths
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

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

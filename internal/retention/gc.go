package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	defaultGCInterval  = time.Hour
	defaultOrphanAge   = time.Hour
	defaultVacuumPages = int64(1000)
)

// GCStats summarizes work performed by one OrphanGC pass.
type GCStats struct {
	RemovedProjectDirs int
	RemovedOrphanBlobs int
	RemovedTmpEntries  int
	Vacuum             store.VacuumStats
}

// OrphanGC periodically removes deleted-project directories, orphan blob files
// older than 1 hour, stale tmp files older than 1 hour, and runs SQLite
// incremental vacuum when the freelist exceeds 10% of pages (FR-098).
type OrphanGC struct {
	store       *store.Store
	blobs       *blob.Store
	clk         clock.Clock
	log         *slog.Logger
	interval    time.Duration
	orphanAge   time.Duration
	vacuumPages int64

	gcMu sync.Mutex
}

// NewOrphanGC constructs an OrphanGC worker backed by st, blobs, clk, and log.
func NewOrphanGC(st *store.Store, blobs *blob.Store, clk clock.Clock, log *slog.Logger) *OrphanGC {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &OrphanGC{
		store:       st,
		blobs:       blobs,
		clk:         clk,
		log:         log,
		interval:    defaultGCInterval,
		orphanAge:   defaultOrphanAge,
		vacuumPages: defaultVacuumPages,
	}
}

// Run executes RunOnce every hour via the injected clock until ctx is cancelled.
func (g *OrphanGC) Run(ctx context.Context) {
	ticker := g.clk.NewTicker(g.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			stats, err := g.RunOnce(ctx)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					g.log.Error("orphan blob GC failed", "error", err)
				}
				continue
			}
			if stats.RemovedProjectDirs > 0 || stats.RemovedOrphanBlobs > 0 || stats.RemovedTmpEntries > 0 || stats.Vacuum.ReclaimedPages > 0 {
				g.log.Info("orphan blob GC completed",
					"removed_project_dirs", stats.RemovedProjectDirs,
					"removed_orphan_blobs", stats.RemovedOrphanBlobs,
					"removed_tmp_entries", stats.RemovedTmpEntries,
					"reclaimed_pages", stats.Vacuum.ReclaimedPages,
				)
			}
		}
	}
}

// RunOnce performs one complete GC pass: deleted project dirs, orphan blobs,
// stale tmp entries, and incremental vacuum (FR-098).
func (g *OrphanGC) RunOnce(ctx context.Context) (GCStats, error) {
	g.gcMu.Lock()
	defer g.gcMu.Unlock()

	var stats GCStats
	now := g.clk.Now().UTC()
	cutoff := now.Add(-g.orphanAge)

	if g.blobs != nil {
		// 1. Remove blob directories belonging to deleted projects.
		// List on-disk directories BEFORE querying DB project IDs so a concurrently
		// created project (with a strictly larger AUTOINCREMENT id) can never be
		// mistaken for a deleted project.
		diskProjIDs, err := g.blobs.ListProjectDirs()
		if err != nil {
			return stats, fmt.Errorf("retention: list blob project dirs: %w", err)
		}
		if len(diskProjIDs) > 0 {
			dbProjIDs, err := g.store.ListProjectIDs(ctx)
			if err != nil {
				return stats, fmt.Errorf("retention: list db project ids: %w", err)
			}
			existing := make(map[int64]struct{}, len(dbProjIDs))
			for _, id := range dbProjIDs {
				existing[id] = struct{}{}
			}
			for _, pid := range diskProjIDs {
				if err := ctx.Err(); err != nil {
					return stats, err
				}
				if _, ok := existing[pid]; ok {
					continue
				}
				// Re-verify deletion against store before deleting directory.
				if _, err := g.store.GetProjectByID(ctx, pid); err == nil {
					continue
				} else if !errors.Is(err, store.ErrNotFound) {
					return stats, fmt.Errorf("retention: verify deleted project %d: %w", pid, err)
				}
				if rmErr := g.blobs.RemoveProject(pid); rmErr != nil {
					g.log.Warn("failed to remove deleted project blob dir", "project_id", pid, "error", rmErr)
					continue
				}
				stats.RemovedProjectDirs++
			}
		}

		// 2. Walk blobs/ and delete orphan files strictly older than 1 hour.
		if err := g.blobs.Walk(ctx, func(entry blob.Entry) error {
			if !entry.ModTime.Before(cutoff) {
				return nil
			}
			exists, err := g.store.MessageExistsByID(ctx, entry.ProjectID, entry.MessageID.String())
			if err != nil {
				return fmt.Errorf("retention: check blob row %s: %w", entry.RelPath, err)
			}
			if exists {
				return nil
			}
			if rmErr := g.blobs.Remove(entry.RelPath); rmErr != nil {
				g.log.Warn("failed to remove orphan blob", "blob_path", entry.RelPath, "error", rmErr)
				return nil
			}
			stats.RemovedOrphanBlobs++
			return nil
		}); err != nil {
			return stats, err
		}

		// 3. Clean stale tmp/ entries older than 1 hour.
		removedTmp, tmpErr := g.blobs.CleanTmpOlderThan(cutoff)
		stats.RemovedTmpEntries = removedTmp
		if tmpErr != nil {
			g.log.Warn("partial failure cleaning stale tmp entries", "error", tmpErr)
		}
	}

	// 4. Incremental vacuum when freelist exceeds 10% of total pages.
	vacStats, err := g.store.IncrementalVacuum(ctx, g.vacuumPages)
	if err != nil {
		return stats, fmt.Errorf("retention: incremental vacuum: %w", err)
	}
	stats.Vacuum = vacStats
	return stats, nil
}

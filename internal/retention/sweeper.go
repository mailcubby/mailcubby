package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	defaultSweepBatchSize = int64(500)
	defaultNudgeDebounce  = 10 * time.Second
)

// SweepStats summarizes messages deleted during a retention sweep pass.
type SweepStats struct {
	DeletedMessages int64
	ProjectsSwept   int
	Batches         int
}

// Sweeper enforces per-project age, count, and byte retention limits (FR-095).
type Sweeper struct {
	store         *store.Store
	blobs         *blob.Store
	bus           *events.Bus
	clk           clock.Clock
	log           *slog.Logger
	cfg           config.Config
	batchSize     int64
	debounceDelay time.Duration

	sweepMu sync.Mutex

	nudgeMu sync.Mutex
	pending map[int64]struct{}
	nudgeCh chan struct{}
}

// NewSweeper constructs a Sweeper wired to st, blobs, bus, clk, log, and cfg.
func NewSweeper(
	st *store.Store,
	blobs *blob.Store,
	bus *events.Bus,
	clk clock.Clock,
	log *slog.Logger,
	cfg config.Config,
) *Sweeper {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Sweeper{
		store:         st,
		blobs:         blobs,
		bus:           bus,
		clk:           clk,
		log:           log,
		cfg:           cfg,
		batchSize:     defaultSweepBatchSize,
		debounceDelay: defaultNudgeDebounce,
		pending:       make(map[int64]struct{}),
		nudgeCh:       make(chan struct{}, 1),
	}
}

// Run executes periodic sweeps every MAILCUBBY_RETENTION_INTERVAL and handles
// debounced nudges (10 s) from ingest until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	interval := s.cfg.RetentionInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := s.clk.NewTicker(interval)
	defer ticker.Stop()

	var (
		debounceTimer clock.Timer
		debounceCh    <-chan time.Time
	)
	stopDebounce := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
			debounceTimer = nil
			debounceCh = nil
		}
	}
	defer stopDebounce()

	armDebounce := func() {
		if debounceTimer == nil {
			debounceTimer = s.clk.NewTimer(s.debounceDelay)
			debounceCh = debounceTimer.C()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C():
			stopDebounce()
			s.clearPending()
			if stats, err := s.SweepAll(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error("retention sweep failed", "error", err)
			} else if stats.DeletedMessages > 0 {
				s.log.Info("retention sweep completed",
					"deleted_messages", stats.DeletedMessages,
					"projects_swept", stats.ProjectsSwept,
					"batches", stats.Batches,
				)
			}

		case <-s.nudgeCh:
			armDebounce()

		case <-debounceCh:
			debounceTimer = nil
			debounceCh = nil
			projectIDs := s.drainPending()
			for _, pid := range projectIDs {
				if ctx.Err() != nil {
					return
				}
				if stats, err := s.SweepProject(ctx, pid); err != nil && !errors.Is(err, context.Canceled) {
					s.log.Error("nudged retention sweep failed", "project_id", pid, "error", err)
				} else if stats.DeletedMessages > 0 {
					s.log.Info("nudged retention sweep completed",
						"project_id", pid,
						"deleted_messages", stats.DeletedMessages,
						"batches", stats.Batches,
					)
				}
			}
			if s.hasPending() {
				armDebounce()
			}
		}
	}
}

// Nudge schedules projectID for a debounced retention sweep (FR-095).
func (s *Sweeper) Nudge(projectID int64) {
	s.nudgeMu.Lock()
	s.pending[projectID] = struct{}{}
	s.nudgeMu.Unlock()

	select {
	case s.nudgeCh <- struct{}{}:
	default:
	}
}

// MaybeNudge checks whether projectID currently exceeds its effective count or
// byte retention limit by more than 10%, and schedules a debounced sweep if so (FR-095).
func (s *Sweeper) MaybeNudge(ctx context.Context, projectID int64) {
	proj, err := s.store.GetProjectByID(ctx, projectID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, context.Canceled) {
			s.log.Warn("retention nudge lookup project failed", "project_id", projectID, "error", err)
		}
		return
	}
	resolved, err := ResolveInstanceSettings(ctx, s.store, s.cfg)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log.Warn("retention nudge load defaults failed", "project_id", projectID, "error", err)
		}
		return
	}
	limits := ResolveProjectLimits(proj.Settings, resolved.Limits())
	if exceedsNudgeThreshold(proj.MessageCount, limits.MaxMessages) ||
		exceedsNudgeThreshold(proj.SizeBytes, limits.MaxBytes) {
		s.Nudge(projectID)
	}
}

// SweepAll enforces retention limits across every project.
func (s *Sweeper) SweepAll(ctx context.Context) (SweepStats, error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()

	resolved, err := ResolveInstanceSettings(ctx, s.store, s.cfg)
	if err != nil {
		return SweepStats{}, err
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return SweepStats{}, fmt.Errorf("retention: list projects: %w", err)
	}

	defaults := resolved.Limits()
	var total SweepStats
	for _, proj := range projects {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res, err := s.sweepProjectLocked(ctx, proj, defaults)
		if err != nil {
			return total, err
		}
		total.DeletedMessages += res.DeletedMessages
		total.Batches += res.Batches
		if res.DeletedMessages > 0 {
			total.ProjectsSwept++
		}
	}
	return total, nil
}

// SweepProject enforces retention limits on a single project.
func (s *Sweeper) SweepProject(ctx context.Context, projectID int64) (SweepStats, error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()

	resolved, err := ResolveInstanceSettings(ctx, s.store, s.cfg)
	if err != nil {
		return SweepStats{}, err
	}
	proj, err := s.store.GetProjectByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SweepStats{}, nil
		}
		return SweepStats{}, fmt.Errorf("retention: get project %d: %w", projectID, err)
	}
	return s.sweepProjectLocked(ctx, proj, resolved.Limits())
}

func (s *Sweeper) sweepProjectLocked(ctx context.Context, proj store.Project, defaults Limits) (SweepStats, error) {
	limits := ResolveProjectLimits(proj.Settings, defaults)
	if limits.MaxAgeSec <= 0 && limits.MaxMessages <= 0 && limits.MaxBytes <= 0 {
		return SweepStats{}, nil
	}

	var stats SweepStats
	now := s.clk.Now().UTC()

	// 1. Max age: delete oldest messages strictly older than now - maxAge in batches of 500.
	if limits.MaxAgeSec > 0 {
		cutoff := now.Add(-time.Duration(limits.MaxAgeSec) * time.Second)
		for {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			targets, err := s.store.ListOldestMessagesBeforeTime(ctx, proj.ID, cutoff, s.batchSize)
			if err != nil {
				return stats, fmt.Errorf("retention: list aged messages for project %d: %w", proj.ID, err)
			}
			if len(targets) == 0 {
				break
			}
			updatedProj, deletedCount, err := s.deleteBatch(ctx, proj, targets, s.clk.Now().UTC())
			if err != nil {
				return stats, err
			}
			proj = updatedProj
			stats.DeletedMessages += deletedCount
			if deletedCount > 0 {
				stats.Batches++
			}
			if deletedCount == 0 {
				break
			}
		}
	}

	// 2. Max messages: delete oldest excess messages in batches of 500.
	if limits.MaxMessages > 0 {
		for proj.MessageCount > limits.MaxMessages {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			batchLimit := min(proj.MessageCount-limits.MaxMessages, s.batchSize)
			targets, err := s.store.ListOldestMessages(ctx, proj.ID, batchLimit)
			if err != nil {
				return stats, fmt.Errorf("retention: list excess count messages for project %d: %w", proj.ID, err)
			}
			if len(targets) == 0 {
				break
			}
			updatedProj, deletedCount, err := s.deleteBatch(ctx, proj, targets, s.clk.Now().UTC())
			if err != nil {
				return stats, err
			}
			proj = updatedProj
			stats.DeletedMessages += deletedCount
			if deletedCount > 0 {
				stats.Batches++
			}
			if deletedCount == 0 {
				break
			}
		}
	}

	// 3. Max bytes: delete oldest messages until size_bytes <= MaxBytes, in batches of 500.
	if limits.MaxBytes > 0 {
		for proj.SizeBytes > limits.MaxBytes {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			candidates, err := s.store.ListOldestMessages(ctx, proj.ID, s.batchSize)
			if err != nil {
				return stats, fmt.Errorf("retention: list excess size messages for project %d: %w", proj.ID, err)
			}
			if len(candidates) == 0 {
				break
			}
			needFree := proj.SizeBytes - limits.MaxBytes
			var (
				freed   int64
				targets []store.DeletedMessage
			)
			for _, c := range candidates {
				targets = append(targets, c)
				freed += c.SizeBytes
				if freed >= needFree {
					break
				}
			}
			updatedProj, deletedCount, err := s.deleteBatch(ctx, proj, targets, s.clk.Now().UTC())
			if err != nil {
				return stats, err
			}
			proj = updatedProj
			stats.DeletedMessages += deletedCount
			if deletedCount > 0 {
				stats.Batches++
			}
			if deletedCount == 0 {
				break
			}
		}
	}

	if stats.DeletedMessages > 0 {
		stats.ProjectsSwept = 1
	}
	return stats, nil
}

func (s *Sweeper) deleteBatch(
	ctx context.Context,
	proj store.Project,
	targets []store.DeletedMessage,
	now time.Time,
) (store.Project, int64, error) {
	var (
		deleted     []store.DeletedMessage
		updatedProj store.Project
	)
	err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		var txErr error
		deleted, updatedProj, txErr = tx.DeleteMessagesAndUpdateCountersTx(ctx, proj.ID, targets, now)
		return txErr
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return proj, 0, nil
		}
		return proj, 0, fmt.Errorf("retention: delete batch for project %d: %w", proj.ID, err)
	}
	if len(deleted) == 0 {
		return updatedProj, 0, nil
	}

	ids := make([]string, 0, len(deleted))
	for _, msg := range deleted {
		ids = append(ids, msg.ID)
		if s.blobs != nil {
			if rmErr := s.blobs.Remove(msg.BlobPath); rmErr != nil {
				s.log.Warn("retention failed to remove blob",
					"project_id", proj.ID,
					"message_id", msg.ID,
					"blob_path", msg.BlobPath,
					"error", rmErr,
				)
			}
		}
	}

	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:      events.TypeMessageDeleted,
			ProjectID: updatedProj.ID,
			Payload: events.MessageDeletedPayload{
				Project: updatedProj.Slug,
				IDs:     ids,
			},
		})
		s.bus.Publish(events.Event{
			Type:      events.TypeProjectUpdated,
			ProjectID: updatedProj.ID,
			Payload: events.ProjectUpdatedPayload{
				Project:       updatedProj.Slug,
				MessageCount:  updatedProj.MessageCount,
				UnreadCount:   updatedProj.UnreadCount,
				SizeBytes:     updatedProj.SizeBytes,
				LastMessageAt: updatedProj.LastMessageAt,
			},
		})
	}

	return updatedProj, int64(len(deleted)), nil
}

func (s *Sweeper) drainPending() []int64 {
	s.nudgeMu.Lock()
	defer s.nudgeMu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := make([]int64, 0, len(s.pending))
	for id := range s.pending {
		out = append(out, id)
	}
	clear(s.pending)
	return out
}

func (s *Sweeper) clearPending() {
	s.nudgeMu.Lock()
	clear(s.pending)
	s.nudgeMu.Unlock()
}

func (s *Sweeper) hasPending() bool {
	s.nudgeMu.Lock()
	defer s.nudgeMu.Unlock()
	return len(s.pending) > 0
}

func exceedsNudgeThreshold(current, limit int64) bool {
	if limit <= 0 || current <= limit {
		return false
	}
	excess := current - limit
	if excess > math.MaxInt64/10 {
		return true
	}
	return excess*10 > limit
}

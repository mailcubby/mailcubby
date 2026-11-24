package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// InstanceProjectTotals aggregates message and storage totals across all projects.
type InstanceProjectTotals struct {
	ProjectCount  int64
	TotalMessages int64
	TotalBytes    int64
}

// VacuumStats reports SQLite page counts and pages reclaimed by IncrementalVacuum.
type VacuumStats struct {
	PageCount      int64
	FreelistCount  int64
	ReclaimedPages int64
}

// GetInstanceProjectTotals returns total project count, message count, and raw stored bytes.
func (s *Store) GetInstanceProjectTotals(ctx context.Context) (InstanceProjectTotals, error) {
	row, err := s.readQ.GetInstanceProjectTotals(ctx)
	if err != nil {
		return InstanceProjectTotals{}, fmt.Errorf("store: get instance project totals: %w", mapError(err))
	}
	return InstanceProjectTotals{
		ProjectCount:  row.ProjectCount,
		TotalMessages: row.TotalMessages,
		TotalBytes:    row.TotalBytes,
	}, nil
}

// ListProjectIDs returns all currently existing project IDs sorted ascending.
func (s *Store) ListProjectIDs(ctx context.Context) ([]int64, error) {
	ids, err := s.readQ.ListProjectIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list project ids: %w", mapError(err))
	}
	return ids, nil
}

// ListOldestMessagesBeforeTime returns up to limit oldest messages in projectID
// with received_at strictly before cutoff (FR-095).
func (s *Store) ListOldestMessagesBeforeTime(ctx context.Context, projectID int64, cutoff time.Time, limit int64) ([]DeletedMessage, error) {
	rows, err := s.readQ.ListOldestMessagesBeforeTime(ctx, sqlc.ListOldestMessagesBeforeTimeParams{
		ProjectID:  projectID,
		ReceivedAt: cutoff.UTC().UnixMilli(),
		Limit:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list oldest messages before %s for project %d: %w", cutoff.UTC().Format(time.RFC3339), projectID, mapError(err))
	}
	out := make([]DeletedMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeletedMessage{
			ID:        r.ID,
			BlobPath:  r.BlobPath,
			SizeBytes: r.SizeBytes,
			WasUnread: r.Seen == 0,
		})
	}
	return out, nil
}

// ListOldestMessages returns up to limit oldest messages in projectID ordered by
// received_at ascending, then id ascending (FR-095).
func (s *Store) ListOldestMessages(ctx context.Context, projectID int64, limit int64) ([]DeletedMessage, error) {
	rows, err := s.readQ.ListOldestMessages(ctx, sqlc.ListOldestMessagesParams{
		ProjectID: projectID,
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list oldest messages for project %d: %w", projectID, mapError(err))
	}
	out := make([]DeletedMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeletedMessage{
			ID:        r.ID,
			BlobPath:  r.BlobPath,
			SizeBytes: r.SizeBytes,
			WasUnread: r.Seen == 0,
		})
	}
	return out, nil
}

// MessageExistsByID reports whether a message with the given ID exists in projectID.
func (s *Store) MessageExistsByID(ctx context.Context, projectID int64, id string) (bool, error) {
	n, err := s.readQ.MessageExistsByID(ctx, sqlc.MessageExistsByIDParams{
		ProjectID: projectID,
		ID:        id,
	})
	if err != nil {
		return false, fmt.Errorf("store: check message %s exists in project %d: %w", id, projectID, mapError(err))
	}
	return n > 0, nil
}

// CountMessagesSince returns the number of messages received across all projects
// at or after since (FR-103).
func (s *Store) CountMessagesSince(ctx context.Context, since time.Time) (int64, error) {
	n, err := s.readQ.CountMessagesSince(ctx, since.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: count messages since %s: %w", since.UTC().Format(time.RFC3339), mapError(err))
	}
	return n, nil
}

// DeleteExpiredSessions deletes sessions past their absolute expiry or idle timeout (FR-099).
func (s *Store) DeleteExpiredSessions(ctx context.Context, now, idleCutoff time.Time) (int64, error) {
	n, err := s.writeQ.DeleteExpiredSessions(ctx, sqlc.DeleteExpiredSessionsParams{
		ExpiresAt:  now.UTC().UnixMilli(),
		LastSeenAt: idleCutoff.UTC().UnixMilli(),
	})
	if err != nil {
		return 0, fmt.Errorf("store: delete expired sessions: %w", mapError(err))
	}
	return n, nil
}

// DeleteExpiredInvites deletes invites whose expiry timestamp has passed (FR-099).
func (s *Store) DeleteExpiredInvites(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.writeQ.DeleteExpiredInvites(ctx, now.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: delete expired invites: %w", mapError(err))
	}
	return n, nil
}

// DeleteExpiredPasswordResets deletes expired or already-used password reset tokens (FR-099).
func (s *Store) DeleteExpiredPasswordResets(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.writeQ.DeleteExpiredPasswordResets(ctx, now.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: delete expired password resets: %w", mapError(err))
	}
	return n, nil
}

// DeleteOldAuditEntries deletes audit log entries strictly older than cutoff (FR-099).
func (s *Store) DeleteOldAuditEntries(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.writeQ.DeleteOldAuditEntries(ctx, cutoff.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: delete old audit entries: %w", mapError(err))
	}
	return n, nil
}

// DBSizeBytes returns the combined on-disk size in bytes of the SQLite database
// file and its active WAL/SHM sidecar files (FR-103).
func (s *Store) DBSizeBytes() (int64, error) {
	var total int64
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return 0, fmt.Errorf("store: stat db file %q: %w", p, err)
		}
		total += info.Size()
	}
	return total, nil
}

// IncrementalVacuum runs PRAGMA incremental_vacuum(pagesPerStep) in a loop while
// the SQLite freelist exceeds 10% of total pages (FR-098, ARCHITECTURE 3.12).
func (s *Store) IncrementalVacuum(ctx context.Context, pagesPerStep int64) (VacuumStats, error) {
	if pagesPerStep <= 0 {
		pagesPerStep = 1000
	}

	readCounts := func() (pageCount, freelistCount int64, err error) {
		if err := s.writer.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
			return 0, 0, fmt.Errorf("store: read PRAGMA page_count: %w", err)
		}
		if err := s.writer.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freelistCount); err != nil {
			return 0, 0, fmt.Errorf("store: read PRAGMA freelist_count: %w", err)
		}
		return pageCount, freelistCount, nil
	}

	pageCount, freelistCount, err := readCounts()
	if err != nil {
		return VacuumStats{}, err
	}

	initialFreelist := freelistCount
	pragmaSQL := fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pagesPerStep)

	for pageCount > 0 && freelistCount*10 > pageCount {
		if err := ctx.Err(); err != nil {
			return VacuumStats{
				PageCount:      pageCount,
				FreelistCount:  freelistCount,
				ReclaimedPages: max(0, initialFreelist-freelistCount),
			}, err
		}

		prevFreelist := freelistCount
		if _, err := s.writer.ExecContext(ctx, pragmaSQL); err != nil {
			return VacuumStats{}, fmt.Errorf("store: execute %s: %w", pragmaSQL, err)
		}

		pageCount, freelistCount, err = readCounts()
		if err != nil {
			return VacuumStats{}, err
		}
		if freelistCount >= prevFreelist {
			break
		}
	}

	return VacuumStats{
		PageCount:      pageCount,
		FreelistCount:  freelistCount,
		ReclaimedPages: max(0, initialFreelist-freelistCount),
	}, nil
}

package search

import (
	"context"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
)

// Messages executes a parsed search query against st's read pool scoped strictly to projectID.
func Messages(ctx context.Context, st *store.Store, projectID int64, q Query, opts BuildOptions) ([]store.Message, error) {
	querySQL, args := Build(projectID, q, opts)
	rows, err := st.ReaderDB().QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search: query messages in project %d: %w", projectID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []store.Message
	for rows.Next() {
		msg, scanErr := store.ScanMessageRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("search: scan message in project %d: %w", projectID, scanErr)
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: iterate messages in project %d: %w", projectID, err)
	}
	if out == nil {
		out = []store.Message{}
	}
	return out, nil
}

// DeleteMessagesByQuery deletes all messages in projectID matching q inside a
// single write transaction, updating project counters and last_message_at (FR-034, FR-076, FR-082).
// It returns ErrNoEffectiveFilter, deleting nothing, when q adds no predicate or
// when q consists only of exclusions (negated text, from, to, subject, tag or
// credential terms) that exclude no message, so a typo or a no-op term can never
// turn a filtered delete into a delete of the whole project.
func DeleteMessagesByQuery(ctx context.Context, st *store.Store, projectID int64, q Query, now time.Time) ([]store.DeletedMessage, store.Project, error) {
	narrows, exclusionsOnly := filterShape(q)
	if !narrows {
		return nil, store.Project{}, ErrNoEffectiveFilter
	}
	selectSQL, args := BuildDeleteSelect(projectID, q)

	var (
		deleted []store.DeletedMessage
		proj    store.Project
	)
	err := st.WithTx(ctx, func(tx *store.Tx) error {
		rows, txErr := tx.SQLTx().QueryContext(ctx, selectSQL, args...)
		if txErr != nil {
			return fmt.Errorf("search: select messages to delete in project %d: %w", projectID, txErr)
		}
		var targets []store.DeletedMessage
		for rows.Next() {
			var (
				pk        int64
				id        string
				blobPath  string
				sizeBytes int64
				seenInt   int64
			)
			if scanErr := rows.Scan(&pk, &id, &blobPath, &sizeBytes, &seenInt); scanErr != nil {
				_ = rows.Close()
				return fmt.Errorf("search: scan delete candidate: %w", scanErr)
			}
			targets = append(targets, store.DeletedMessage{
				ID:        id,
				BlobPath:  blobPath,
				SizeBytes: sizeBytes,
				WasUnread: seenInt == 0,
			})
		}
		if closeErr := rows.Err(); closeErr != nil {
			_ = rows.Close()
			return fmt.Errorf("search: iterate delete candidates: %w", closeErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return fmt.Errorf("search: close delete candidates: %w", closeErr)
		}

		if exclusionsOnly && len(targets) > 0 {
			var total int
			if countErr := tx.SQLTx().QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE project_id = ?", projectID).Scan(&total); countErr != nil {
				return fmt.Errorf("search: count messages in project %d: %w", projectID, countErr)
			}
			if len(targets) == total {
				return ErrNoEffectiveFilter
			}
		}

		deleted, proj, txErr = tx.DeleteMessagesAndUpdateCountersTx(ctx, projectID, targets, now)
		return txErr
	})
	if err != nil {
		return nil, store.Project{}, err
	}
	return deleted, proj, nil
}

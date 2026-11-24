package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

const (
	// MaxTagNameRunes is the maximum allowed rune length of a tag name (SPEC FR-085).
	MaxTagNameRunes = 40
	// DefaultTagColor is the default preset color assigned to auto-created tags.
	DefaultTagColor = "slate"

	tagPKBatchSize = 500
)

// Tag represents a project-scoped tag and its current message count (SPEC FR-085, 8.5).
type Tag struct {
	ID           int64
	ProjectID    int64
	Name         string
	Color        string
	MessageCount int64
	CreatedAt    time.Time
}

// MessageTagUpdate records a message whose tag set changed and its new sorted tag names.
type MessageTagUpdate struct {
	ID   string
	Tags []string
}

// PatchMessageResult holds the outcome of PatchMessage for seen and/or tag changes.
type PatchMessageResult struct {
	Message     Message
	Project     Project
	Tags        []string
	SeenChanged bool
	TagsChanged bool
}

// ValidateTagName verifies name against SPEC FR-085: 1 to 40 runes, each matching [\p{L}\p{N}_.-].
func ValidateTagName(name string) error {
	if name == "" {
		return errors.New("tag name must not be empty")
	}
	if utf8.RuneCountInString(name) > MaxTagNameRunes {
		return fmt.Errorf("tag name must be at most %d characters", MaxTagNameRunes)
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '.' || r == '-' {
			continue
		}
		return errors.New("tag name may contain only letters, numbers, '_', '.', and '-'")
	}
	return nil
}

// ListTags returns all tags in projectID with their message counts, ordered case-insensitively by name.
func (s *Store) ListTags(ctx context.Context, projectID int64) ([]Tag, error) {
	rows, err := s.readQ.ListTagsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list tags for project %d: %w", projectID, mapError(err))
	}
	out := make([]Tag, 0, len(rows))
	for _, r := range rows {
		out = append(out, Tag{
			ID:           r.ID,
			ProjectID:    r.ProjectID,
			Name:         r.Name,
			Color:        r.Color,
			MessageCount: r.MessageCount,
			CreatedAt:    time.UnixMilli(r.CreatedAt).UTC(),
		})
	}
	return out, nil
}

// GetTagByID returns a single tag with its message count scoped strictly to projectID.
func (s *Store) GetTagByID(ctx context.Context, projectID, tagID int64) (Tag, error) {
	r, err := s.readQ.GetTagByID(ctx, sqlc.GetTagByIDParams{
		ProjectID: projectID,
		ID:        tagID,
	})
	if err != nil {
		return Tag{}, fmt.Errorf("store: get tag %d in project %d: %w", tagID, projectID, mapError(err))
	}
	return Tag{
		ID:           r.ID,
		ProjectID:    r.ProjectID,
		Name:         r.Name,
		Color:        r.Color,
		MessageCount: r.MessageCount,
		CreatedAt:    time.UnixMilli(r.CreatedAt).UTC(),
	}, nil
}

// CreateTag creates a new tag in projectID (returns ErrConflict on case-insensitive duplicate).
func (s *Store) CreateTag(ctx context.Context, projectID int64, name, color string, now time.Time) (Tag, error) {
	if color == "" {
		color = DefaultTagColor
	}
	row, err := s.writeQ.CreateTag(ctx, sqlc.CreateTagParams{
		ProjectID: projectID,
		Name:      name,
		Color:     color,
		CreatedAt: now.UTC().UnixMilli(),
	})
	if err != nil {
		return Tag{}, fmt.Errorf("store: create tag %q in project %d: %w", name, projectID, mapError(err))
	}
	return Tag{
		ID:           row.ID,
		ProjectID:    row.ProjectID,
		Name:         row.Name,
		Color:        row.Color,
		MessageCount: 0,
		CreatedAt:    time.UnixMilli(row.CreatedAt).UTC(),
	}, nil
}

// UpdateTag updates a tag's name and/or color scoped strictly to projectID.
// If the tag's name changed, affected messages and their updated tag slices are returned.
func (s *Store) UpdateTag(ctx context.Context, projectID, tagID int64, name *string, color *string) (Tag, []MessageTagUpdate, error) {
	var (
		updated Tag
		updates []MessageTagUpdate
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		cur, txErr := tx.q.GetTagByID(ctx, sqlc.GetTagByIDParams{
			ProjectID: projectID,
			ID:        tagID,
		})
		if txErr != nil {
			return fmt.Errorf("store: get tag %d in project %d: %w", tagID, projectID, mapError(txErr))
		}

		nextName := cur.Name
		if name != nil {
			nextName = *name
		}
		nextColor := cur.Color
		if color != nil {
			nextColor = *color
		}

		row, txErr := tx.q.UpdateTag(ctx, sqlc.UpdateTagParams{
			Name:      nextName,
			Color:     nextColor,
			ProjectID: projectID,
			ID:        tagID,
		})
		if txErr != nil {
			return fmt.Errorf("store: update tag %d in project %d: %w", tagID, projectID, mapError(txErr))
		}

		if nextName != cur.Name {
			msgIDs, listErr := tx.q.ListMessageIDsByTagID(ctx, sqlc.ListMessageIDsByTagIDParams{
				ProjectID: projectID,
				TagID:     tagID,
			})
			if listErr != nil {
				return fmt.Errorf("store: list messages for renamed tag %d: %w", tagID, mapError(listErr))
			}
			for _, msgID := range msgIDs {
				msg, getErr := tx.GetMessageByID(ctx, projectID, msgID)
				if getErr != nil {
					return getErr
				}
				tags, tagErr := listTagNamesTx(ctx, tx.q, projectID, msg.PK)
				if tagErr != nil {
					return tagErr
				}
				updates = append(updates, MessageTagUpdate{ID: msgID, Tags: tags})
			}
		}

		updated = Tag{
			ID:           row.ID,
			ProjectID:    row.ProjectID,
			Name:         row.Name,
			Color:        row.Color,
			MessageCount: cur.MessageCount,
			CreatedAt:    time.UnixMilli(row.CreatedAt).UTC(),
		}
		return nil
	})
	if err != nil {
		return Tag{}, nil, err
	}
	return updated, updates, nil
}

// DeleteTag deletes a tag scoped strictly to projectID (cascading message_tags)
// and returns the messages that lost the tag along with their remaining tag lists.
func (s *Store) DeleteTag(ctx context.Context, projectID, tagID int64) ([]MessageTagUpdate, error) {
	var updates []MessageTagUpdate
	err := s.WithTx(ctx, func(tx *Tx) error {
		msgIDs, txErr := tx.q.ListMessageIDsByTagID(ctx, sqlc.ListMessageIDsByTagIDParams{
			ProjectID: projectID,
			TagID:     tagID,
		})
		if txErr != nil {
			return fmt.Errorf("store: list messages for tag %d: %w", tagID, mapError(txErr))
		}

		affected, txErr := tx.q.DeleteTag(ctx, sqlc.DeleteTagParams{
			ProjectID: projectID,
			ID:        tagID,
		})
		if txErr != nil {
			return fmt.Errorf("store: delete tag %d in project %d: %w", tagID, projectID, mapError(txErr))
		}
		if affected == 0 {
			return fmt.Errorf("store: delete tag %d in project %d: %w", tagID, projectID, ErrNotFound)
		}

		for _, msgID := range msgIDs {
			msg, getErr := tx.GetMessageByID(ctx, projectID, msgID)
			if getErr != nil {
				if errors.Is(getErr, ErrNotFound) {
					continue
				}
				return getErr
			}
			tags, tagErr := listTagNamesTx(ctx, tx.q, projectID, msg.PK)
			if tagErr != nil {
				return tagErr
			}
			updates = append(updates, MessageTagUpdate{ID: msgID, Tags: tags})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updates, nil
}

// EnsureAndAttachTagsTx ensures every tag name in tagNames exists in projectID,
// attaches them to messagePK inside tx, and returns the canonical tag names
// attached to the message in input order (FR-085, FR-086).
func (t *Tx) EnsureAndAttachTagsTx(ctx context.Context, projectID, messagePK int64, tagNames []string, now time.Time) ([]string, error) {
	if len(tagNames) == 0 {
		return []string{}, nil
	}
	canonical := make([]string, 0, len(tagNames))
	seen := make(map[int64]struct{}, len(tagNames))
	for _, raw := range tagNames {
		tag, err := ensureTagTx(ctx, t.q, projectID, raw, DefaultTagColor, now)
		if err != nil {
			return nil, err
		}
		if _, err := t.q.AddMessageTag(ctx, sqlc.AddMessageTagParams{
			MessagePk: messagePK,
			TagID:     tag.ID,
			ProjectID: projectID,
		}); err != nil {
			return nil, fmt.Errorf("store: attach tag %q to message %d: %w", tag.Name, messagePK, mapError(err))
		}
		if _, dup := seen[tag.ID]; !dup {
			seen[tag.ID] = struct{}{}
			canonical = append(canonical, tag.Name)
		}
	}
	return canonical, nil
}

// ListTagsByMessagePK returns the sorted tag names attached to messagePK in projectID.
func (s *Store) ListTagsByMessagePK(ctx context.Context, projectID, messagePK int64) ([]string, error) {
	return listTagNamesTx(ctx, s.readQ, projectID, messagePK)
}

// ListTagsByMessagePKs batch-loads tag names for all given message PKs in projectID.
func (s *Store) ListTagsByMessagePKs(ctx context.Context, projectID int64, pks []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(pks))
	if len(pks) == 0 {
		return out, nil
	}

	for start := 0; start < len(pks); start += tagPKBatchSize {
		end := min(start+tagPKBatchSize, len(pks))
		rows, err := s.readQ.ListTagsByMessagePKs(ctx, sqlc.ListTagsByMessagePKsParams{
			ProjectID:  projectID,
			MessagePks: pks[start:end],
		})
		if err != nil {
			return nil, fmt.Errorf("store: batch list tags in project %d: %w", projectID, mapError(err))
		}
		for _, r := range rows {
			out[r.MessagePk] = append(out[r.MessagePk], r.Name)
		}
	}
	return out, nil
}

// PatchMessage atomically updates a message's seen state and/or replaces its tag set
// inside one write transaction (SPEC 8.4, FR-034, FR-075, FR-085).
func (s *Store) PatchMessage(ctx context.Context, projectID int64, msgID string, seen *bool, tags *[]string, now time.Time) (PatchMessageResult, error) {
	var res PatchMessageResult
	err := s.WithTx(ctx, func(tx *Tx) error {
		msg, txErr := tx.GetMessageByID(ctx, projectID, msgID)
		if txErr != nil {
			return txErr
		}

		if seen != nil && msg.Seen != *seen {
			affected, updErr := tx.q.UpdateMessageSeenIfChanged(ctx, sqlc.UpdateMessageSeenIfChangedParams{
				Seen:      boolToInt64(*seen),
				ProjectID: projectID,
				ID:        msgID,
			})
			if updErr != nil {
				return fmt.Errorf("store: update message %s seen: %w", msgID, mapError(updErr))
			}
			if affected > 0 {
				unreadDelta := int64(1)
				if *seen {
					unreadDelta = -1
				}
				if cntErr := tx.UpdateProjectCounters(ctx, projectID, ProjectCounterDelta{
					UnreadDelta: unreadDelta,
					Now:         now,
				}); cntErr != nil {
					return cntErr
				}
				msg.Seen = *seen
				res.SeenChanged = true
			}
		}

		beforeTags, txErr := listTagNamesTx(ctx, tx.q, projectID, msg.PK)
		if txErr != nil {
			return txErr
		}

		if tags != nil {
			deduped := dedupeCaseInsensitive(*tags)
			if delErr := tx.q.DeleteMessageTagsByMessagePK(ctx, sqlc.DeleteMessageTagsByMessagePKParams{
				ProjectID: projectID,
				MessagePk: msg.PK,
			}); delErr != nil {
				return fmt.Errorf("store: clear message %s tags: %w", msgID, mapError(delErr))
			}
			for _, name := range deduped {
				tag, tagErr := ensureTagTx(ctx, tx.q, projectID, name, DefaultTagColor, now)
				if tagErr != nil {
					return tagErr
				}
				if _, addErr := tx.q.AddMessageTag(ctx, sqlc.AddMessageTagParams{
					MessagePk: msg.PK,
					TagID:     tag.ID,
					ProjectID: projectID,
				}); addErr != nil {
					return fmt.Errorf("store: attach tag %q to message %s: %w", tag.Name, msgID, mapError(addErr))
				}
			}
			afterTags, listErr := listTagNamesTx(ctx, tx.q, projectID, msg.PK)
			if listErr != nil {
				return listErr
			}
			res.Tags = afterTags
			res.TagsChanged = !slices.Equal(beforeTags, afterTags)
		} else {
			res.Tags = beforeTags
		}

		proj, txErr := tx.GetProjectByID(ctx, projectID)
		if txErr != nil {
			return txErr
		}
		res.Message = msg
		res.Project = proj
		return nil
	})
	if err != nil {
		return PatchMessageResult{}, err
	}
	return res, nil
}

// BulkUpdateMessageTags adds or removes tagNames on all matching message IDs in projectID
// in a single transaction, returning updates only for messages whose tags changed (SPEC 8.4).
func (s *Store) BulkUpdateMessageTags(ctx context.Context, projectID int64, ids []string, add bool, tagNames []string, now time.Time) ([]MessageTagUpdate, error) {
	uniqueIDs := dedupeStrings(ids)
	uniqueTags := dedupeCaseInsensitive(tagNames)
	if len(uniqueIDs) == 0 || len(uniqueTags) == 0 {
		return nil, nil
	}

	var updates []MessageTagUpdate
	err := s.WithTx(ctx, func(tx *Tx) error {
		tagIDs := make([]int64, 0, len(uniqueTags))
		if add {
			for _, name := range uniqueTags {
				tag, txErr := ensureTagTx(ctx, tx.q, projectID, name, DefaultTagColor, now)
				if txErr != nil {
					return txErr
				}
				tagIDs = append(tagIDs, tag.ID)
			}
		} else {
			for _, name := range uniqueTags {
				tag, txErr := tx.q.GetTagByName(ctx, sqlc.GetTagByNameParams{
					ProjectID: projectID,
					Name:      name,
				})
				if txErr != nil {
					if errors.Is(mapError(txErr), ErrNotFound) {
						continue
					}
					return fmt.Errorf("store: lookup tag %q in project %d: %w", name, projectID, mapError(txErr))
				}
				tagIDs = append(tagIDs, tag.ID)
			}
		}

		if len(tagIDs) == 0 {
			return nil
		}

		for _, id := range uniqueIDs {
			msg, txErr := tx.GetMessageByID(ctx, projectID, id)
			if txErr != nil {
				if errors.Is(txErr, ErrNotFound) {
					continue
				}
				return txErr
			}

			msgChanged := false
			for _, tagID := range tagIDs {
				if add {
					affected, addErr := tx.q.AddMessageTag(ctx, sqlc.AddMessageTagParams{
						MessagePk: msg.PK,
						TagID:     tagID,
						ProjectID: projectID,
					})
					if addErr != nil {
						return fmt.Errorf("store: bulk add tag %d to message %s: %w", tagID, id, mapError(addErr))
					}
					if affected > 0 {
						msgChanged = true
					}
				} else {
					affected, rmErr := tx.q.RemoveMessageTag(ctx, sqlc.RemoveMessageTagParams{
						ProjectID: projectID,
						MessagePk: msg.PK,
						TagID:     tagID,
					})
					if rmErr != nil {
						return fmt.Errorf("store: bulk remove tag %d from message %s: %w", tagID, id, mapError(rmErr))
					}
					if affected > 0 {
						msgChanged = true
					}
				}
			}

			if msgChanged {
				tags, listErr := listTagNamesTx(ctx, tx.q, projectID, msg.PK)
				if listErr != nil {
					return listErr
				}
				updates = append(updates, MessageTagUpdate{ID: msg.ID, Tags: tags})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updates, nil
}

func ensureTagTx(ctx context.Context, q *sqlc.Queries, projectID int64, name, color string, now time.Time) (sqlc.Tag, error) {
	if err := q.EnsureTagInsert(ctx, sqlc.EnsureTagInsertParams{
		ProjectID: projectID,
		Name:      name,
		Color:     color,
		CreatedAt: now.UTC().UnixMilli(),
	}); err != nil {
		return sqlc.Tag{}, fmt.Errorf("store: ensure tag %q in project %d: %w", name, projectID, mapError(err))
	}
	tag, err := q.GetTagByName(ctx, sqlc.GetTagByNameParams{
		ProjectID: projectID,
		Name:      name,
	})
	if err != nil {
		return sqlc.Tag{}, fmt.Errorf("store: get ensured tag %q in project %d: %w", name, projectID, mapError(err))
	}
	return tag, nil
}

func listTagNamesTx(ctx context.Context, q *sqlc.Queries, projectID, messagePK int64) ([]string, error) {
	names, err := q.ListTagNamesByMessagePK(ctx, sqlc.ListTagNamesByMessagePKParams{
		ProjectID: projectID,
		MessagePk: messagePK,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list tags for message %d in project %d: %w", messagePK, projectID, mapError(err))
	}
	if names == nil {
		return []string{}, nil
	}
	return names, nil
}

func dedupeCaseInsensitive(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		key := strings.ToLower(s)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// Address represents a parsed email display name and address pair.
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Message represents a stored email message row.
type Message struct {
	PK               int64
	ID               string
	ProjectID        int64
	SMTPCredentialID *int64
	ReceivedAt       time.Time
	Subject          string
	FromName         string
	FromAddr         string
	To               []Address
	Cc               []Address
	Bcc              []Address
	ReplyTo          []Address
	EnvelopeFrom     string
	EnvelopeTo       []string
	MessageIDHeader  *string
	DateHeader       *time.Time
	Snippet          string
	HasHTML          bool
	HasText          bool
	TextGenerated    bool
	AttachmentCount  int64
	SizeBytes        int64
	BlobPath         string
	Seen             bool
	RemoteIP         *string
	HELO             *string
	AuthUser         *string
	TLS              bool
	ParseWarnings    []string
}

// Attachment represents a stored attachment metadata row.
type Attachment struct {
	ID          int64
	MessagePK   int64
	ProjectID   int64
	PartPath    string
	Filename    string
	ContentType string
	SizeBytes   int64
	ContentID   *string
	Disposition string
	SHA256      string
}

// InsertMessageParams specifies the fields to insert a new message row.
type InsertMessageParams struct {
	ID               string
	ProjectID        int64
	SMTPCredentialID *int64
	ReceivedAt       time.Time
	Subject          string
	FromName         string
	FromAddr         string
	To               []Address
	Cc               []Address
	Bcc              []Address
	ReplyTo          []Address
	EnvelopeFrom     string
	EnvelopeTo       []string
	MessageIDHeader  *string
	DateHeader       *time.Time
	Snippet          string
	HasHTML          bool
	HasText          bool
	TextGenerated    bool
	AttachmentCount  int64
	SizeBytes        int64
	BlobPath         string
	Seen             bool
	RemoteIP         *string
	HELO             *string
	AuthUser         *string
	TLS              bool
	ParseWarnings    []string
}

// InsertAttachmentParams specifies the fields to insert an attachment metadata row.
type InsertAttachmentParams struct {
	MessagePK   int64
	ProjectID   int64
	PartPath    string
	Filename    string
	ContentType string
	SizeBytes   int64
	ContentID   *string
	Disposition string
	SHA256      string
}

// InsertMessage inserts a message row inside tx and returns the stored Message (including PK).
func (t *Tx) InsertMessage(ctx context.Context, p InsertMessageParams) (Message, error) {
	return insertMessage(ctx, t.q, p)
}

// InsertMessage inserts a message row and returns the stored Message.
func (s *Store) InsertMessage(ctx context.Context, p InsertMessageParams) (Message, error) {
	return insertMessage(ctx, s.writeQ, p)
}

func insertMessage(ctx context.Context, q *sqlc.Queries, p InsertMessageParams) (Message, error) {
	toJSON, err := marshalAddresses(p.To)
	if err != nil {
		return Message{}, fmt.Errorf("store: marshal to: %w", err)
	}
	ccJSON, err := marshalAddresses(p.Cc)
	if err != nil {
		return Message{}, fmt.Errorf("store: marshal cc: %w", err)
	}
	bccJSON, err := marshalAddresses(p.Bcc)
	if err != nil {
		return Message{}, fmt.Errorf("store: marshal bcc: %w", err)
	}
	replyToJSON, err := marshalAddresses(p.ReplyTo)
	if err != nil {
		return Message{}, fmt.Errorf("store: marshal reply_to: %w", err)
	}
	envToJSON, err := marshalStringSlice(p.EnvelopeTo)
	if err != nil {
		return Message{}, fmt.Errorf("store: marshal envelope_to: %w", err)
	}
	var warningsNull sql.NullString
	if len(p.ParseWarnings) > 0 {
		w, err := json.Marshal(p.ParseWarnings)
		if err != nil {
			return Message{}, fmt.Errorf("store: marshal parse_warnings: %w", err)
		}
		warningsNull = sql.NullString{String: string(w), Valid: true}
	}

	row, err := q.InsertMessage(ctx, sqlc.InsertMessageParams{
		ID:                p.ID,
		ProjectID:         p.ProjectID,
		SmtpCredentialID:  nullInt64Ptr(p.SMTPCredentialID),
		ReceivedAt:        p.ReceivedAt.UTC().UnixMilli(),
		Subject:           p.Subject,
		FromName:          p.FromName,
		FromAddr:          p.FromAddr,
		ToJson:            toJSON,
		CcJson:            ccJSON,
		BccJson:           bccJSON,
		ReplyToJson:       replyToJSON,
		EnvelopeFrom:      p.EnvelopeFrom,
		EnvelopeToJson:    envToJSON,
		MessageIDHeader:   nullStringPtr(p.MessageIDHeader),
		DateHeader:        nullTimeMsPtr(p.DateHeader),
		Snippet:           p.Snippet,
		HasHtml:           boolToInt64(p.HasHTML),
		HasText:           boolToInt64(p.HasText),
		TextGenerated:     boolToInt64(p.TextGenerated),
		AttachmentCount:   p.AttachmentCount,
		SizeBytes:         p.SizeBytes,
		BlobPath:          p.BlobPath,
		Seen:              boolToInt64(p.Seen),
		RemoteIp:          nullStringPtr(p.RemoteIP),
		Helo:              nullStringPtr(p.HELO),
		AuthUser:          nullStringPtr(p.AuthUser),
		Tls:               boolToInt64(p.TLS),
		ParseWarningsJson: warningsNull,
	})
	if err != nil {
		return Message{}, fmt.Errorf("store: insert message %s: %w", p.ID, mapError(err))
	}
	return messageFromRow(row)
}

// GetMessageByID fetches a single message scoped strictly to projectID.
func (s *Store) GetMessageByID(ctx context.Context, projectID int64, id string) (Message, error) {
	return getMessageByID(ctx, s.readQ, projectID, id)
}

// GetMessageByID fetches a single message scoped strictly to projectID inside tx.
func (t *Tx) GetMessageByID(ctx context.Context, projectID int64, id string) (Message, error) {
	return getMessageByID(ctx, t.q, projectID, id)
}

func getMessageByID(ctx context.Context, q *sqlc.Queries, projectID int64, id string) (Message, error) {
	row, err := q.GetMessageByID(ctx, sqlc.GetMessageByIDParams{
		ProjectID: projectID,
		ID:        id,
	})
	if err != nil {
		return Message{}, fmt.Errorf("store: get message %s in project %d: %w", id, projectID, mapError(err))
	}
	return messageFromRow(row)
}

// ListMessages returns up to limit newest messages in projectID ordered by ID descending.
func (s *Store) ListMessages(ctx context.Context, projectID int64, limit int64) ([]Message, error) {
	return s.ListMessagesPage(ctx, projectID, "", limit)
}

// ListMessagesPage returns up to limit messages in projectID ordered by ID descending.
// If beforeID is non-empty, only messages with ID strictly less than beforeID are returned.
func (s *Store) ListMessagesPage(ctx context.Context, projectID int64, beforeID string, limit int64) ([]Message, error) {
	var (
		rows []sqlc.Message
		err  error
	)
	if beforeID == "" {
		rows, err = s.readQ.ListMessages(ctx, sqlc.ListMessagesParams{
			ProjectID: projectID,
			Limit:     limit,
		})
	} else {
		rows, err = s.readQ.ListMessagesBeforeCursor(ctx, sqlc.ListMessagesBeforeCursorParams{
			ProjectID: projectID,
			ID:        beforeID,
			Limit:     limit,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("store: list messages in project %d: %w", projectID, mapError(err))
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		msg, err := messageFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// ProjectSparkline returns 24 hourly message counts for projectID ending at the
// UTC hour containing now (index 0 is oldest, index 23 is the current hour; FR-035).
func (s *Store) ProjectSparkline(ctx context.Context, projectID int64, now time.Time) ([24]int64, error) {
	var out [24]int64
	currentHour := now.UTC().UnixMilli() / 3600000
	startHour := currentHour - 23
	sinceMs := startHour * 3600000

	rows, err := s.readQ.GetProjectSparkline(ctx, sqlc.GetProjectSparklineParams{
		ProjectID:  projectID,
		ReceivedAt: sinceMs,
	})
	if err != nil {
		return out, fmt.Errorf("store: sparkline for project %d: %w", projectID, mapError(err))
	}
	for _, r := range rows {
		idx := r.HourBucket - startHour
		if idx >= 0 && idx < 24 {
			out[idx] = r.MessageCount
		}
	}
	return out, nil
}

// GetAttachmentByID fetches one attachment metadata row scoped strictly to projectID and messagePK.
func (s *Store) GetAttachmentByID(ctx context.Context, projectID, messagePK, attachmentID int64) (Attachment, error) {
	row, err := s.readQ.GetAttachmentByID(ctx, sqlc.GetAttachmentByIDParams{
		ProjectID: projectID,
		MessagePk: messagePK,
		ID:        attachmentID,
	})
	if err != nil {
		return Attachment{}, fmt.Errorf("store: get attachment %d for message %d in project %d: %w", attachmentID, messagePK, projectID, mapError(err))
	}
	return attachmentFromRow(row), nil
}

// DeletedMessage captures metadata for a message removed from the database so
// callers can clean up its blob file and emit realtime deletion events.
type DeletedMessage struct {
	ID        string
	BlobPath  string
	SizeBytes int64
	WasUnread bool
}

// SetMessageSeen updates a single message's seen state and maintains the project's
// unread_count counter atomically (FR-034, FR-075). The bool return value is true
// only when the message's seen flag actually changed.
func (s *Store) SetMessageSeen(ctx context.Context, projectID int64, id string, seen bool, now time.Time) (Message, Project, bool, error) {
	var (
		msg     Message
		proj    Project
		changed bool
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		var txErr error
		msg, txErr = tx.GetMessageByID(ctx, projectID, id)
		if txErr != nil {
			return txErr
		}
		if msg.Seen == seen {
			proj, txErr = tx.GetProjectByID(ctx, projectID)
			return txErr
		}

		affected, txErr := tx.q.UpdateMessageSeenIfChanged(ctx, sqlc.UpdateMessageSeenIfChangedParams{
			Seen:      boolToInt64(seen),
			ProjectID: projectID,
			ID:        id,
		})
		if txErr != nil {
			return fmt.Errorf("store: update message %s seen: %w", id, mapError(txErr))
		}
		if affected > 0 {
			unreadDelta := int64(1)
			if seen {
				unreadDelta = -1
			}
			if txErr := tx.UpdateProjectCounters(ctx, projectID, ProjectCounterDelta{
				UnreadDelta: unreadDelta,
				Now:         now,
			}); txErr != nil {
				return txErr
			}
			msg.Seen = seen
			changed = true
		}
		proj, txErr = tx.GetProjectByID(ctx, projectID)
		return txErr
	})
	if err != nil {
		return Message{}, Project{}, false, err
	}
	return msg, proj, changed, nil
}

// DeleteMessage deletes a single message scoped to projectID and updates project
// counters and last_message_at in the same transaction (FR-034, FR-076).
func (s *Store) DeleteMessage(ctx context.Context, projectID int64, id string, now time.Time) (DeletedMessage, Project, error) {
	var (
		deleted DeletedMessage
		proj    Project
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		msg, txErr := tx.GetMessageByID(ctx, projectID, id)
		if txErr != nil {
			return txErr
		}
		affected, txErr := tx.q.DeleteMessageByID(ctx, sqlc.DeleteMessageByIDParams{
			ProjectID: projectID,
			ID:        id,
		})
		if txErr != nil {
			return fmt.Errorf("store: delete message %s in project %d: %w", id, projectID, mapError(txErr))
		}
		if affected == 0 {
			return fmt.Errorf("store: delete message %s in project %d: %w", id, projectID, ErrNotFound)
		}

		lastMsg, txErr := latestMessageReceivedAtNull(ctx, tx.q, projectID)
		if txErr != nil {
			return txErr
		}
		unreadDelta := int64(0)
		if !msg.Seen {
			unreadDelta = -1
		}
		projRow, txErr := tx.q.UpdateProjectCountersAndLastMessageAt(ctx, sqlc.UpdateProjectCountersAndLastMessageAtParams{
			MessageDelta:  -1,
			UnreadDelta:   unreadDelta,
			SizeDelta:     -msg.SizeBytes,
			LastMessageAt: lastMsg,
			UpdatedAt:     now.UTC().UnixMilli(),
			ID:            projectID,
		})
		if txErr != nil {
			return fmt.Errorf("store: update counters after deleting message %s: %w", id, mapError(txErr))
		}
		deleted = DeletedMessage{
			ID:        msg.ID,
			BlobPath:  msg.BlobPath,
			SizeBytes: msg.SizeBytes,
			WasUnread: !msg.Seen,
		}
		proj = projectFromRow(projRow)
		return nil
	})
	if err != nil {
		return DeletedMessage{}, Project{}, err
	}
	return deleted, proj, nil
}

// BulkSetMessagesSeen updates seen state for all matching IDs in projectID in one
// transaction and adjusts unread_count accordingly (FR-034, FR-075).
// Returns the IDs whose seen flag actually changed and the updated Project.
func (s *Store) BulkSetMessagesSeen(ctx context.Context, projectID int64, ids []string, seen bool, now time.Time) ([]string, Project, error) {
	uniqueIDs := dedupeStrings(ids)
	var (
		changedIDs []string
		proj       Project
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		seenVal := boolToInt64(seen)
		for _, id := range uniqueIDs {
			affected, txErr := tx.q.UpdateMessageSeenIfChanged(ctx, sqlc.UpdateMessageSeenIfChangedParams{
				Seen:      seenVal,
				ProjectID: projectID,
				ID:        id,
			})
			if txErr != nil {
				return fmt.Errorf("store: bulk update message %s seen: %w", id, mapError(txErr))
			}
			if affected > 0 {
				changedIDs = append(changedIDs, id)
			}
		}
		if len(changedIDs) > 0 {
			unreadDelta := int64(len(changedIDs))
			if seen {
				unreadDelta = -unreadDelta
			}
			if txErr := tx.UpdateProjectCounters(ctx, projectID, ProjectCounterDelta{
				UnreadDelta: unreadDelta,
				Now:         now,
			}); txErr != nil {
				return txErr
			}
		}
		var txErr error
		proj, txErr = tx.GetProjectByID(ctx, projectID)
		return txErr
	})
	if err != nil {
		return nil, Project{}, err
	}
	return changedIDs, proj, nil
}

// BulkDeleteMessages deletes all matching message IDs in projectID in one transaction,
// updating project counters and last_message_at (FR-034, FR-076).
func (s *Store) BulkDeleteMessages(ctx context.Context, projectID int64, ids []string, now time.Time) ([]DeletedMessage, Project, error) {
	uniqueIDs := dedupeStrings(ids)
	var (
		deleted []DeletedMessage
		proj    Project
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		var (
			unreadDelta int64
			sizeDelta   int64
		)
		for _, id := range uniqueIDs {
			msg, txErr := tx.GetMessageByID(ctx, projectID, id)
			if txErr != nil {
				if errors.Is(txErr, ErrNotFound) {
					continue
				}
				return txErr
			}
			affected, txErr := tx.q.DeleteMessageByID(ctx, sqlc.DeleteMessageByIDParams{
				ProjectID: projectID,
				ID:        id,
			})
			if txErr != nil {
				return fmt.Errorf("store: bulk delete message %s: %w", id, mapError(txErr))
			}
			if affected == 0 {
				continue
			}
			if !msg.Seen {
				unreadDelta--
			}
			sizeDelta -= msg.SizeBytes
			deleted = append(deleted, DeletedMessage{
				ID:        msg.ID,
				BlobPath:  msg.BlobPath,
				SizeBytes: msg.SizeBytes,
				WasUnread: !msg.Seen,
			})
		}

		if len(deleted) == 0 {
			var txErr error
			proj, txErr = tx.GetProjectByID(ctx, projectID)
			return txErr
		}

		lastMsg, txErr := latestMessageReceivedAtNull(ctx, tx.q, projectID)
		if txErr != nil {
			return txErr
		}
		projRow, txErr := tx.q.UpdateProjectCountersAndLastMessageAt(ctx, sqlc.UpdateProjectCountersAndLastMessageAtParams{
			MessageDelta:  -int64(len(deleted)),
			UnreadDelta:   unreadDelta,
			SizeDelta:     sizeDelta,
			LastMessageAt: lastMsg,
			UpdatedAt:     now.UTC().UnixMilli(),
			ID:            projectID,
		})
		if txErr != nil {
			return fmt.Errorf("store: update counters after bulk delete: %w", mapError(txErr))
		}
		proj = projectFromRow(projRow)
		return nil
	})
	if err != nil {
		return nil, Project{}, err
	}
	return deleted, proj, nil
}

// DeleteAllMessages deletes every message in projectID in a single transaction,
// resets project counters to zero, and returns the affected count and blob paths
// for post-commit best-effort cleanup (FR-034, FR-076).
func (s *Store) DeleteAllMessages(ctx context.Context, projectID int64, now time.Time) (int64, []string, Project, error) {
	var (
		affected  int64
		blobPaths []string
		proj      Project
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		var txErr error
		blobPaths, txErr = tx.q.ListMessageBlobPathsByProject(ctx, projectID)
		if txErr != nil {
			return fmt.Errorf("store: list blob paths for project %d: %w", projectID, mapError(txErr))
		}
		affected, txErr = tx.q.DeleteAllMessagesByProject(ctx, projectID)
		if txErr != nil {
			return fmt.Errorf("store: delete all messages in project %d: %w", projectID, mapError(txErr))
		}
		projRow, txErr := tx.q.ResetProjectCounters(ctx, sqlc.ResetProjectCountersParams{
			UpdatedAt: now.UTC().UnixMilli(),
			ID:        projectID,
		})
		if txErr != nil {
			return fmt.Errorf("store: reset counters for project %d: %w", projectID, mapError(txErr))
		}
		proj = projectFromRow(projRow)
		return nil
	})
	if err != nil {
		return 0, nil, Project{}, err
	}
	return affected, blobPaths, proj, nil
}

// GetLatestMessageAfterID returns the newest message in projectID whose ULID is
// strictly greater than afterID, or ErrNotFound if none exists (FR-078).
func (s *Store) GetLatestMessageAfterID(ctx context.Context, projectID int64, afterID string) (Message, error) {
	row, err := s.readQ.GetLatestMessageAfterID(ctx, sqlc.GetLatestMessageAfterIDParams{
		ProjectID: projectID,
		ID:        afterID,
	})
	if err != nil {
		return Message{}, fmt.Errorf("store: get latest message after %s in project %d: %w", afterID, projectID, mapError(err))
	}
	return messageFromRow(row)
}

// GetLatestMessageSinceTime returns the newest message in projectID received at
// or after since, or ErrNotFound if none exists (FR-078).
func (s *Store) GetLatestMessageSinceTime(ctx context.Context, projectID int64, since time.Time) (Message, error) {
	row, err := s.readQ.GetLatestMessageSinceTime(ctx, sqlc.GetLatestMessageSinceTimeParams{
		ProjectID:  projectID,
		ReceivedAt: since.UTC().UnixMilli(),
	})
	if err != nil {
		return Message{}, fmt.Errorf("store: get latest message since %s in project %d: %w", since.UTC().Format(time.RFC3339), projectID, mapError(err))
	}
	return messageFromRow(row)
}

func latestMessageReceivedAtNull(ctx context.Context, q *sqlc.Queries, projectID int64) (sql.NullInt64, error) {
	ms, err := q.GetLatestMessageReceivedAt(ctx, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.NullInt64{}, nil
		}
		return sql.NullInt64{}, fmt.Errorf("store: get latest received_at for project %d: %w", projectID, mapError(err))
	}
	return sql.NullInt64{Int64: ms, Valid: true}, nil
}

func dedupeStrings(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// InsertAttachment inserts an attachment metadata row inside tx.
func (t *Tx) InsertAttachment(ctx context.Context, p InsertAttachmentParams) (Attachment, error) {
	return insertAttachment(ctx, t.q, p)
}

// InsertAttachment inserts an attachment metadata row.
func (s *Store) InsertAttachment(ctx context.Context, p InsertAttachmentParams) (Attachment, error) {
	return insertAttachment(ctx, s.writeQ, p)
}

func insertAttachment(ctx context.Context, q *sqlc.Queries, p InsertAttachmentParams) (Attachment, error) {
	row, err := q.InsertAttachment(ctx, sqlc.InsertAttachmentParams{
		MessagePk:   p.MessagePK,
		ProjectID:   p.ProjectID,
		PartPath:    p.PartPath,
		Filename:    p.Filename,
		ContentType: p.ContentType,
		SizeBytes:   p.SizeBytes,
		ContentID:   nullStringPtr(p.ContentID),
		Disposition: p.Disposition,
		Sha256:      p.SHA256,
	})
	if err != nil {
		return Attachment{}, fmt.Errorf("store: insert attachment %q for message %d: %w", p.Filename, p.MessagePK, mapError(err))
	}
	return attachmentFromRow(row), nil
}

// ListAttachmentsByMessagePK returns all attachments for messagePK scoped to projectID.
func (s *Store) ListAttachmentsByMessagePK(ctx context.Context, projectID, messagePK int64) ([]Attachment, error) {
	rows, err := s.readQ.ListAttachmentsByMessagePK(ctx, sqlc.ListAttachmentsByMessagePKParams{
		ProjectID: projectID,
		MessagePk: messagePK,
	})
	if err != nil {
		return nil, fmt.Errorf("store: list attachments for message %d in project %d: %w", messagePK, projectID, mapError(err))
	}
	out := make([]Attachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, attachmentFromRow(r))
	}
	return out, nil
}

// ScanMessageRow scans a row with all 29 messages table columns (in schema order)
// into a Message. Used by internal/search for dynamic FTS5 queries.
func ScanMessageRow(rows *sql.Rows) (Message, error) {
	var r sqlc.Message
	if err := rows.Scan(
		&r.Pk,
		&r.ID,
		&r.ProjectID,
		&r.SmtpCredentialID,
		&r.ReceivedAt,
		&r.Subject,
		&r.FromName,
		&r.FromAddr,
		&r.ToJson,
		&r.CcJson,
		&r.BccJson,
		&r.ReplyToJson,
		&r.EnvelopeFrom,
		&r.EnvelopeToJson,
		&r.MessageIDHeader,
		&r.DateHeader,
		&r.Snippet,
		&r.HasHtml,
		&r.HasText,
		&r.TextGenerated,
		&r.AttachmentCount,
		&r.SizeBytes,
		&r.BlobPath,
		&r.Seen,
		&r.RemoteIp,
		&r.Helo,
		&r.AuthUser,
		&r.Tls,
		&r.ParseWarningsJson,
	); err != nil {
		return Message{}, fmt.Errorf("store: scan message row: %w", err)
	}
	return messageFromRow(r)
}

// DeleteMessagesAndUpdateCountersTx deletes the given messages by ID inside tx
// and updates project counters and last_message_at atomically (FR-034, FR-076).
func (t *Tx) DeleteMessagesAndUpdateCountersTx(ctx context.Context, projectID int64, targets []DeletedMessage, now time.Time) ([]DeletedMessage, Project, error) {
	var (
		deleted     []DeletedMessage
		unreadDelta int64
		sizeDelta   int64
	)
	for _, msg := range targets {
		affected, err := t.q.DeleteMessageByID(ctx, sqlc.DeleteMessageByIDParams{
			ProjectID: projectID,
			ID:        msg.ID,
		})
		if err != nil {
			return nil, Project{}, fmt.Errorf("store: delete message %s: %w", msg.ID, mapError(err))
		}
		if affected == 0 {
			continue
		}
		if msg.WasUnread {
			unreadDelta--
		}
		sizeDelta -= msg.SizeBytes
		deleted = append(deleted, msg)
	}

	if len(deleted) == 0 {
		proj, err := t.GetProjectByID(ctx, projectID)
		return nil, proj, err
	}

	lastMsg, err := latestMessageReceivedAtNull(ctx, t.q, projectID)
	if err != nil {
		return nil, Project{}, err
	}
	projRow, err := t.q.UpdateProjectCountersAndLastMessageAt(ctx, sqlc.UpdateProjectCountersAndLastMessageAtParams{
		MessageDelta:  -int64(len(deleted)),
		UnreadDelta:   unreadDelta,
		SizeDelta:     sizeDelta,
		LastMessageAt: lastMsg,
		UpdatedAt:     now.UTC().UnixMilli(),
		ID:            projectID,
	})
	if err != nil {
		return nil, Project{}, fmt.Errorf("store: update counters after query delete: %w", mapError(err))
	}
	return deleted, projectFromRow(projRow), nil
}

func messageFromRow(r sqlc.Message) (Message, error) {
	to, err := unmarshalAddresses(r.ToJson)
	if err != nil {
		return Message{}, fmt.Errorf("store: unmarshal to_json for message %s: %w", r.ID, err)
	}
	cc, err := unmarshalAddresses(r.CcJson)
	if err != nil {
		return Message{}, fmt.Errorf("store: unmarshal cc_json for message %s: %w", r.ID, err)
	}
	bcc, err := unmarshalAddresses(r.BccJson)
	if err != nil {
		return Message{}, fmt.Errorf("store: unmarshal bcc_json for message %s: %w", r.ID, err)
	}
	replyTo, err := unmarshalAddresses(r.ReplyToJson)
	if err != nil {
		return Message{}, fmt.Errorf("store: unmarshal reply_to_json for message %s: %w", r.ID, err)
	}
	var envTo []string
	if err := json.Unmarshal([]byte(r.EnvelopeToJson), &envTo); err != nil {
		return Message{}, fmt.Errorf("store: unmarshal envelope_to_json for message %s: %w", r.ID, err)
	}
	if envTo == nil {
		envTo = []string{}
	}

	var warnings []string
	if r.ParseWarningsJson.Valid && r.ParseWarningsJson.String != "" {
		if err := json.Unmarshal([]byte(r.ParseWarningsJson.String), &warnings); err != nil {
			return Message{}, fmt.Errorf("store: unmarshal parse_warnings_json for message %s: %w", r.ID, err)
		}
	}

	return Message{
		PK:               r.Pk,
		ID:               r.ID,
		ProjectID:        r.ProjectID,
		SMTPCredentialID: int64PtrFromNull(r.SmtpCredentialID),
		ReceivedAt:       time.UnixMilli(r.ReceivedAt).UTC(),
		Subject:          r.Subject,
		FromName:         r.FromName,
		FromAddr:         r.FromAddr,
		To:               to,
		Cc:               cc,
		Bcc:              bcc,
		ReplyTo:          replyTo,
		EnvelopeFrom:     r.EnvelopeFrom,
		EnvelopeTo:       envTo,
		MessageIDHeader:  stringPtrFromNull(r.MessageIDHeader),
		DateHeader:       timePtrFromNullMs(r.DateHeader),
		Snippet:          r.Snippet,
		HasHTML:          r.HasHtml != 0,
		HasText:          r.HasText != 0,
		TextGenerated:    r.TextGenerated != 0,
		AttachmentCount:  r.AttachmentCount,
		SizeBytes:        r.SizeBytes,
		BlobPath:         r.BlobPath,
		Seen:             r.Seen != 0,
		RemoteIP:         stringPtrFromNull(r.RemoteIp),
		HELO:             stringPtrFromNull(r.Helo),
		AuthUser:         stringPtrFromNull(r.AuthUser),
		TLS:              r.Tls != 0,
		ParseWarnings:    warnings,
	}, nil
}

func attachmentFromRow(r sqlc.Attachment) Attachment {
	return Attachment{
		ID:          r.ID,
		MessagePK:   r.MessagePk,
		ProjectID:   r.ProjectID,
		PartPath:    r.PartPath,
		Filename:    r.Filename,
		ContentType: r.ContentType,
		SizeBytes:   r.SizeBytes,
		ContentID:   stringPtrFromNull(r.ContentID),
		Disposition: r.Disposition,
		SHA256:      r.Sha256,
	}
}

func marshalAddresses(addrs []Address) (string, error) {
	if len(addrs) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(addrs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalAddresses(raw string) ([]Address, error) {
	var out []Address
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Address{}
	}
	return out, nil
}

func marshalStringSlice(items []string) (string, error) {
	if len(items) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func nullStringPtr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func stringPtrFromNull(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullTimeMsPtr(p *time.Time) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: p.UTC().UnixMilli(), Valid: true}
}

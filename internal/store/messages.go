package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	rows, err := s.readQ.ListMessages(ctx, sqlc.ListMessagesParams{
		ProjectID: projectID,
		Limit:     limit,
	})
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

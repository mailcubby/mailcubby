// Package ingest implements the email ingestion pipeline: streaming zstd spool,
// bounded-concurrency MIME parsing, blob placement, transactional SQLite + FTS5
// commit, and realtime event publication (ARCHITECTURE 3.4).
package ingest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/sync/semaphore"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
)

// TxStore is the minimal store interface needed by Ingestor.
type TxStore interface {
	WithTx(ctx context.Context, fn func(tx *store.Tx) error) error
}

// Publisher receives realtime events after a message transaction commits.
type Publisher interface {
	Publish(e events.Event)
}

// Envelope holds SMTP session metadata stored alongside each message (FR-006).
type Envelope struct {
	MailFrom string
	RcptTo   []string
	RemoteIP string
	HELO     string
	TLS      bool
	AuthUser string
}

// Target describes one destination project resolved for an SMTP message.
type Target struct {
	ProjectID       int64
	ProjectSlug     string
	CredentialID    *int64
	CredentialLabel string
	PlusTags        []string
	PlusAddressTags bool
}

// MessageRef is returned for each stored message copy created by Ingest.
type MessageRef struct {
	PK          int64
	ID          ulid.ULID
	ProjectID   int64
	ProjectSlug string
	BlobPath    string
	SizeBytes   int64
	SHA256      string
	Tags        []string
	Summary     events.MessageSummary
}

// Ingestor executes the message ingestion pipeline.
type Ingestor struct {
	store    TxStore
	blobs    *blob.Store
	clk      clock.Clock
	bus      Publisher
	log      *slog.Logger
	parseSem *semaphore.Weighted

	// Phase 10 hooks (no-ops until Phase 10):
	// TODO(phase-10): wire instance quota check, per-project rate limit, and retention nudge.
	quotaCheck     func(ctx context.Context, sizeBytes int64, targets []Target) error
	retentionNudge func(projectID int64)

	ulidMu  sync.Mutex
	entropy io.Reader
	lastMS  uint64
}

// New constructs an Ingestor with bounded parse concurrency (2 * GOMAXPROCS).
func New(st TxStore, blobs *blob.Store, clk clock.Clock, bus Publisher, log *slog.Logger) *Ingestor {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	maxParse := int64(max(2, 2*runtime.GOMAXPROCS(0)))
	return &Ingestor{
		store:    st,
		blobs:    blobs,
		clk:      clk,
		bus:      bus,
		log:      log,
		parseSem: semaphore.NewWeighted(maxParse),
		entropy:  ulid.Monotonic(rand.Reader, 0),
	}
}

// Ingest runs pipeline steps 1 through 7 (ARCHITECTURE 3.4) for all target projects.
func (in *Ingestor) Ingest(ctx context.Context, env Envelope, targets []Target, r io.Reader) ([]MessageRef, error) {
	deduped := deduplicateTargets(targets)
	if len(deduped) == 0 {
		return nil, errors.New("ingest: at least one target project is required")
	}

	// Step 1: Spool raw DATA through zstd while counting bytes and computing SHA-256.
	spool, err := in.blobs.Create(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingest: create spool: %w", err)
	}
	defer func() {
		if cleanErr := spool.Cleanup(); cleanErr != nil {
			in.log.Warn("failed to cleanup spool", "error", cleanErr)
		}
	}()

	if err := spool.CopyFrom(ctx, r); err != nil {
		return nil, fmt.Errorf("ingest: spool message: %w", err)
	}
	if err := spool.CloseWrite(); err != nil {
		return nil, fmt.Errorf("ingest: finalize spool: %w", err)
	}

	// Step 2: Quota / rate-limit check hook (no-op until Phase 10).
	if in.quotaCheck != nil {
		if err := in.quotaCheck(ctx, spool.Size(), deduped); err != nil {
			return nil, err
		}
	}

	// Step 3: Parse under bounded concurrency semaphore.
	parsed, err := in.parseSpool(ctx, spool, env.RcptTo)
	if err != nil {
		return nil, err
	}

	// Step 4: Build FTS index document.
	ftsDoc := buildSearchDocument(parsed)

	receivedAt := in.clk.Now().UTC()
	rawSize := spool.Size()
	rawSHA := spool.SHA256()

	refs := make([]MessageRef, 0, len(deduped))
	for _, target := range deduped {
		ref, err := in.commitTarget(ctx, spool, env, target, parsed, ftsDoc, receivedAt, rawSize, rawSHA)
		if err != nil {
			return refs, err
		}
		refs = append(refs, ref)

		// Step 7: Publish message.created after commit succeeds (FR-012).
		if in.bus != nil {
			in.bus.Publish(events.Event{
				Type:      events.TypeMessageCreated,
				ProjectID: target.ProjectID,
				Payload: events.MessageCreatedPayload{
					Project: target.ProjectSlug,
					Message: ref.Summary,
				},
			})
		}

		// Step 8: Retention nudge hook (no-op until Phase 10).
		if in.retentionNudge != nil {
			in.retentionNudge(target.ProjectID)
		}
	}

	return refs, nil
}

func (in *Ingestor) parseSpool(ctx context.Context, spool *blob.Spool, rcptTo []string) (ParsedMessage, error) {
	if err := in.parseSem.Acquire(ctx, 1); err != nil {
		return ParsedMessage{}, fmt.Errorf("ingest: acquire parse slot: %w", err)
	}
	defer in.parseSem.Release(1)

	rc, err := spool.Open()
	if err != nil {
		return ParsedMessage{}, fmt.Errorf("ingest: open spool for parse: %w", err)
	}
	defer func() { _ = rc.Close() }()

	return ParseMessage(rc, rcptTo), nil
}

func (in *Ingestor) commitTarget(
	ctx context.Context,
	spool *blob.Spool,
	env Envelope,
	target Target,
	parsed ParsedMessage,
	ftsDoc search.Document,
	receivedAt time.Time,
	rawSize int64,
	rawSHA string,
) (MessageRef, error) {
	msgID, err := in.nextULID(receivedAt)
	if err != nil {
		return MessageRef{}, fmt.Errorf("ingest: generate message id: %w", err)
	}

	// Step 5: Place blob (link or copy + dir fsync) before DB commit (FR-010).
	relPath, err := in.blobs.Place(spool, target.ProjectID, msgID)
	if err != nil {
		return MessageRef{}, fmt.Errorf("ingest: place blob for project %d: %w", target.ProjectID, err)
	}

	// Combine X-Tags and plus-address tags (FR-086).
	combinedRawTags := make([]string, 0, len(parsed.XTags)+len(target.PlusTags))
	combinedRawTags = append(combinedRawTags, parsed.XTags...)
	if target.PlusAddressTags {
		combinedRawTags = append(combinedRawTags, target.PlusTags...)
	}
	extractedTags := sanitizeAndDedupeTags(combinedRawTags)
	if extractedTags == nil {
		extractedTags = []string{}
	}

	var (
		storedMsg store.Message
		savedTags []string
	)
	// Step 6: Commit message, attachments, tags, FTS row, and project counters in one tx.
	txErr := in.store.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		storedMsg, err = tx.InsertMessage(ctx, store.InsertMessageParams{
			ID:               msgID.String(),
			ProjectID:        target.ProjectID,
			SMTPCredentialID: target.CredentialID,
			ReceivedAt:       receivedAt,
			Subject:          parsed.Subject,
			FromName:         parsed.FromName,
			FromAddr:         parsed.FromAddr,
			To:               parsed.To,
			Cc:               parsed.Cc,
			Bcc:              parsed.Bcc,
			ReplyTo:          parsed.ReplyTo,
			EnvelopeFrom:     env.MailFrom,
			EnvelopeTo:       env.RcptTo,
			MessageIDHeader:  nonEmptyPtr(parsed.MessageIDHeader),
			DateHeader:       parsed.DateHeader,
			Snippet:          parsed.Snippet,
			HasHTML:          parsed.HasHTML,
			HasText:          parsed.HasText,
			TextGenerated:    parsed.TextGenerated,
			AttachmentCount:  int64(len(parsed.Attachments)),
			SizeBytes:        rawSize,
			BlobPath:         relPath,
			Seen:             false,
			RemoteIP:         nonEmptyPtr(env.RemoteIP),
			HELO:             nonEmptyPtr(env.HELO),
			AuthUser:         nonEmptyPtr(env.AuthUser),
			TLS:              env.TLS,
			ParseWarnings:    parsed.ParseWarnings,
		})
		if err != nil {
			return err
		}

		for _, att := range parsed.Attachments {
			if _, err := tx.InsertAttachment(ctx, store.InsertAttachmentParams{
				MessagePK:   storedMsg.PK,
				ProjectID:   target.ProjectID,
				PartPath:    att.PartPath,
				Filename:    att.Filename,
				ContentType: att.ContentType,
				SizeBytes:   att.SizeBytes,
				ContentID:   nonEmptyPtr(att.ContentID),
				Disposition: att.Disposition,
				SHA256:      att.SHA256,
			}); err != nil {
				return err
			}
		}

		savedTags, err = tx.EnsureAndAttachTagsTx(ctx, target.ProjectID, storedMsg.PK, extractedTags, receivedAt)
		if err != nil {
			return err
		}

		if err := search.IndexTx(ctx, tx, storedMsg.PK, ftsDoc); err != nil {
			return err
		}

		return tx.UpdateProjectCounters(ctx, target.ProjectID, store.ProjectCounterDelta{
			MessageDelta:  1,
			UnreadDelta:   1,
			SizeDelta:     rawSize,
			LastMessageAt: &receivedAt,
			Now:           receivedAt,
		})
	})
	if txErr != nil {
		// FR-010: remove blob if transaction fails.
		if rmErr := in.blobs.Remove(relPath); rmErr != nil {
			in.log.Warn("failed to remove blob after db error", "blob_path", relPath, "error", rmErr)
		}
		return MessageRef{}, fmt.Errorf("ingest: commit project %d: %w", target.ProjectID, txErr)
	}
	if savedTags == nil {
		savedTags = []string{}
	}

	summary := events.MessageSummary{
		ID:         msgID.String(),
		ReceivedAt: receivedAt,
		From: events.Address{
			Name:    parsed.FromName,
			Address: parsed.FromAddr,
		},
		To:              toEventAddresses(parsed.To),
		Cc:              toEventAddresses(parsed.Cc),
		Subject:         parsed.Subject,
		Snippet:         parsed.Snippet,
		SizeBytes:       rawSize,
		Seen:            false,
		AttachmentCount: int64(len(parsed.Attachments)),
		HasHTML:         parsed.HasHTML,
		HasText:         parsed.HasText,
		Tags:            savedTags,
		CredentialLabel: target.CredentialLabel,
	}

	return MessageRef{
		PK:          storedMsg.PK,
		ID:          msgID,
		ProjectID:   target.ProjectID,
		ProjectSlug: target.ProjectSlug,
		BlobPath:    relPath,
		SizeBytes:   rawSize,
		SHA256:      rawSHA,
		Tags:        savedTags,
		Summary:     summary,
	}, nil
}

func (in *Ingestor) nextULID(ts time.Time) (ulid.ULID, error) {
	in.ulidMu.Lock()
	defer in.ulidMu.Unlock()
	ms := ulid.Timestamp(ts.UTC())
	if ms < in.lastMS {
		ms = in.lastMS
	}
	in.lastMS = ms
	return ulid.New(ms, in.entropy)
}

func deduplicateTargets(targets []Target) []Target {
	if len(targets) <= 1 {
		return targets
	}
	idxByID := make(map[int64]int, len(targets))
	out := make([]Target, 0, len(targets))
	for _, t := range targets {
		if idx, exists := idxByID[t.ProjectID]; exists {
			out[idx].PlusTags = append(out[idx].PlusTags, t.PlusTags...)
			if t.PlusAddressTags {
				out[idx].PlusAddressTags = true
			}
			continue
		}
		idxByID[t.ProjectID] = len(out)
		cp := t
		cp.PlusTags = append([]string(nil), t.PlusTags...)
		out = append(out, cp)
	}
	return out
}

func buildSearchDocument(p ParsedMessage) search.Document {
	fromParts := make([]string, 0, 2)
	if p.FromName != "" {
		fromParts = append(fromParts, p.FromName)
	}
	if p.FromAddr != "" {
		fromParts = append(fromParts, p.FromAddr)
	}

	toParts := make([]string, 0, len(p.To)+len(p.Cc)+len(p.Bcc))
	appendAddrs := func(list []store.Address) {
		for _, a := range list {
			if a.Name != "" {
				toParts = append(toParts, a.Name)
			}
			if a.Address != "" {
				toParts = append(toParts, a.Address)
			}
		}
	}
	appendAddrs(p.To)
	appendAddrs(p.Cc)
	appendAddrs(p.Bcc)

	attNames := make([]string, 0, len(p.Attachments))
	for _, a := range p.Attachments {
		if a.Filename != "" {
			attNames = append(attNames, a.Filename)
		}
	}

	return search.Document{
		Subject:     p.Subject,
		FromText:    strings.Join(fromParts, " "),
		ToText:      strings.Join(toParts, " "),
		Body:        p.TextBody,
		Attachments: strings.Join(attNames, " "),
	}
}

func toEventAddresses(addrs []store.Address) []events.Address {
	out := make([]events.Address, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, events.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

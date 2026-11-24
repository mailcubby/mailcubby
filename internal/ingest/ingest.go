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

// Sentinel errors returned by ingest quota and rate-limit checks (FR-096, FR-097).
var (
	// ErrQuotaExceeded indicates MAILCUBBY_INSTANCE_MAX_BYTES would be exceeded (SMTP 452 4.3.1).
	ErrQuotaExceeded = errors.New("ingest: instance storage quota exceeded")
	// ErrRateLimited indicates MAILCUBBY_SMTP_RATE_LIMIT was exceeded for a project (SMTP 450 4.7.1).
	ErrRateLimited = errors.New("ingest: project rate limit exceeded")
)

// Ingestor executes the message ingestion pipeline.
type Ingestor struct {
	store    TxStore
	blobs    *blob.Store
	clk      clock.Clock
	bus      Publisher
	log      *slog.Logger
	parseSem *semaphore.Weighted

	quotaCheck     func(ctx context.Context, sizeBytes int64, targets []Target) error
	retentionNudge func(ctx context.Context, projectID int64)

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

// SetQuotaCheck configures the step 2 instance-quota and per-project rate-limit hook (FR-096, FR-097).
// Must be called before Ingest is invoked concurrently.
func (in *Ingestor) SetQuotaCheck(fn func(ctx context.Context, sizeBytes int64, targets []Target) error) {
	in.quotaCheck = fn
}

// SetRetentionNudge configures the post-commit retention nudge hook (FR-095, ARCHITECTURE 3.4).
// Must be called before Ingest is invoked concurrently.
func (in *Ingestor) SetRetentionNudge(fn func(ctx context.Context, projectID int64)) {
	in.retentionNudge = fn
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

	type preparedTarget struct {
		target        Target
		msgID         ulid.ULID
		relPath       string
		extractedTags []string
		warnings      []string
	}

	// Step 5: Place blobs for every target project before starting the DB transaction (FR-010).
	// If placing any blob fails, clean up all blobs already placed.
	prepared := make([]preparedTarget, 0, len(deduped))
	cleanupPlaced := func() {
		for _, pt := range prepared {
			if rmErr := in.blobs.Remove(pt.relPath); rmErr != nil {
				in.log.Warn("failed to remove blob after ingest error", "blob_path", pt.relPath, "error", rmErr)
			}
		}
	}

	for _, target := range deduped {
		msgID, err := in.nextULID(receivedAt)
		if err != nil {
			cleanupPlaced()
			return nil, fmt.Errorf("ingest: generate message id: %w", err)
		}
		relPath, err := in.blobs.Place(spool, target.ProjectID, msgID)
		if err != nil {
			cleanupPlaced()
			return nil, fmt.Errorf("ingest: place blob for project %d: %w", target.ProjectID, err)
		}

		// Combine X-Tags and plus-address tags (FR-086), capped at MaxAutoTagsPerMessage.
		combinedRawTags := make([]string, 0, len(parsed.XTags)+len(target.PlusTags))
		combinedRawTags = append(combinedRawTags, parsed.XTags...)
		if target.PlusAddressTags {
			combinedRawTags = append(combinedRawTags, target.PlusTags...)
		}
		extractedTags, truncated := sanitizeAndDedupeTags(combinedRawTags, MaxAutoTagsPerMessage)
		if extractedTags == nil {
			extractedTags = []string{}
		}
		warnings := parsed.ParseWarnings
		if truncated {
			truncMsg := fmt.Sprintf("auto-tags truncated to %d per message", MaxAutoTagsPerMessage)
			if !containsString(warnings, truncMsg) {
				warnings = append(append([]string(nil), warnings...), truncMsg)
			}
		}

		prepared = append(prepared, preparedTarget{
			target:        target,
			msgID:         msgID,
			relPath:       relPath,
			extractedTags: extractedTags,
			warnings:      warnings,
		})
	}

	// Step 6: Commit all target projects atomically in a single SQLite write transaction
	// so a failure on a later target never leaves duplicates in earlier projects on SMTP 451 retry.
	refs := make([]MessageRef, len(prepared))
	txErr := in.store.WithTx(ctx, func(tx *store.Tx) error {
		for i, pt := range prepared {
			storedMsg, err := tx.InsertMessage(ctx, store.InsertMessageParams{
				ID:               pt.msgID.String(),
				ProjectID:        pt.target.ProjectID,
				SMTPCredentialID: pt.target.CredentialID,
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
				BlobPath:         pt.relPath,
				Seen:             false,
				RemoteIP:         nonEmptyPtr(env.RemoteIP),
				HELO:             nonEmptyPtr(env.HELO),
				AuthUser:         nonEmptyPtr(env.AuthUser),
				TLS:              env.TLS,
				ParseWarnings:    pt.warnings,
			})
			if err != nil {
				return fmt.Errorf("project %d: %w", pt.target.ProjectID, err)
			}

			for _, att := range parsed.Attachments {
				if _, err := tx.InsertAttachment(ctx, store.InsertAttachmentParams{
					MessagePK:   storedMsg.PK,
					ProjectID:   pt.target.ProjectID,
					PartPath:    att.PartPath,
					Filename:    att.Filename,
					ContentType: att.ContentType,
					SizeBytes:   att.SizeBytes,
					ContentID:   nonEmptyPtr(att.ContentID),
					Disposition: att.Disposition,
					SHA256:      att.SHA256,
				}); err != nil {
					return fmt.Errorf("project %d attachment: %w", pt.target.ProjectID, err)
				}
			}

			savedTags, err := tx.EnsureAndAttachTagsTx(ctx, pt.target.ProjectID, storedMsg.PK, pt.extractedTags, receivedAt)
			if err != nil {
				return fmt.Errorf("project %d tags: %w", pt.target.ProjectID, err)
			}
			if savedTags == nil {
				savedTags = []string{}
			}

			if err := search.IndexTx(ctx, tx, storedMsg.PK, ftsDoc); err != nil {
				return fmt.Errorf("project %d fts: %w", pt.target.ProjectID, err)
			}

			if err := tx.UpdateProjectCounters(ctx, pt.target.ProjectID, store.ProjectCounterDelta{
				MessageDelta:  1,
				UnreadDelta:   1,
				SizeDelta:     rawSize,
				LastMessageAt: &receivedAt,
				Now:           receivedAt,
			}); err != nil {
				return fmt.Errorf("project %d counters: %w", pt.target.ProjectID, err)
			}

			refs[i] = MessageRef{
				PK:          storedMsg.PK,
				ID:          pt.msgID,
				ProjectID:   pt.target.ProjectID,
				ProjectSlug: pt.target.ProjectSlug,
				BlobPath:    pt.relPath,
				SizeBytes:   rawSize,
				SHA256:      rawSHA,
				Tags:        savedTags,
				Summary: events.MessageSummary{
					ID:         pt.msgID.String(),
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
					CredentialLabel: pt.target.CredentialLabel,
				},
			}
		}
		return nil
	})
	if txErr != nil {
		// FR-010: remove all placed blobs if the transaction fails.
		cleanupPlaced()
		return nil, fmt.Errorf("ingest: commit: %w", txErr)
	}

	// Step 7 & 8: Publish message.created and signal retention nudges after commit (FR-012, FR-095).
	for _, ref := range refs {
		if in.bus != nil {
			in.bus.Publish(events.Event{
				Type:      events.TypeMessageCreated,
				ProjectID: ref.ProjectID,
				Payload: events.MessageCreatedPayload{
					Project: ref.ProjectSlug,
					Message: ref.Summary,
				},
			})
		}
		if in.retentionNudge != nil {
			in.retentionNudge(ctx, ref.ProjectID)
		}
	}

	return refs, nil
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
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

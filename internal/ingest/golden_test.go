package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

var updateGolden = flag.Bool("update", false, "regenerate golden files in testdata/mime")

type goldenSummary struct {
	Subject         string                    `json:"subject"`
	FromName        string                    `json:"from_name"`
	FromAddr        string                    `json:"from_addr"`
	To              []store.Address           `json:"to"`
	Cc              []store.Address           `json:"cc"`
	Bcc             []store.Address           `json:"bcc"`
	ReplyTo         []store.Address           `json:"reply_to"`
	MessageIDHeader string                    `json:"message_id_header,omitempty"`
	DateHeader      *time.Time                `json:"date_header,omitempty"`
	Snippet         string                    `json:"snippet"`
	HasHTML         bool                      `json:"has_html"`
	HasText         bool                      `json:"has_text"`
	TextGenerated   bool                      `json:"text_generated"`
	AttachmentCount int64                     `json:"attachment_count"`
	Attachments     []ingest.ParsedAttachment `json:"attachments"`
	ParseWarnings   []string                  `json:"parse_warnings,omitempty"`
	ExtractedTags   []string                  `json:"extracted_tags,omitempty"`
}

func TestMIMEGoldenFixtures(t *testing.T) {
	// FR-007, FR-008, FR-009: every MIME fixture parses cleanly into its golden
	// summary and stores an FTS5 index row in SQLite.
	fixtureDir := filepath.Join("..", "..", "testdata", "mime")
	matches, err := filepath.Glob(filepath.Join(fixtureDir, "*.eml"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(matches) < 15 {
		t.Fatalf("expected at least 15 MIME fixtures in %s, found %d", fixtureDir, len(matches))
	}

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	fixedTime := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "golden",
		Name:     "Golden",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      fixedTime,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, nil, nil)

	for _, emlPath := range matches {
		name := filepath.Base(emlPath)
		t.Run(name, func(t *testing.T) {
			//nolint:gosec // G304: fixture paths come from repo testdata/mime/*.eml glob
			raw, err := os.ReadFile(emlPath)
			if err != nil {
				t.Fatalf("read %s: %v", emlPath, err)
			}

			refs, err := ing.Ingest(
				ctx,
				ingest.Envelope{
					MailFrom: "sender@example.test",
					RcptTo:   []string{"envelope-only@example.test"},
					RemoteIP: "127.0.0.1",
					HELO:     "test.local",
				},
				[]ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug, PlusAddressTags: true}},
				bytes.NewReader(raw),
			)
			if err != nil {
				t.Fatalf("Ingest(%s): %v", name, err)
			}
			if len(refs) != 1 {
				t.Fatalf("expected 1 ref, got %d", len(refs))
			}
			ref := refs[0]

			msg, err := st.GetMessageByID(ctx, proj.ID, ref.ID.String())
			if err != nil {
				t.Fatalf("GetMessageByID: %v", err)
			}
			atts, err := st.ListAttachmentsByMessagePK(ctx, proj.ID, msg.PK)
			if err != nil {
				t.Fatalf("ListAttachmentsByMessagePK: %v", err)
			}

			// Verify every stored message has an FTS row for its PK.
			var ftsExists int
			err = st.WithTx(ctx, func(tx *store.Tx) error {
				return tx.SQLTx().QueryRowContext(
					ctx,
					`SELECT COUNT(*) FROM messages_fts WHERE rowid = ?`,
					msg.PK,
				).Scan(&ftsExists)
			})
			if err != nil {
				t.Fatalf("query messages_fts rowid=%d: %v", msg.PK, err)
			}
			if ftsExists != 1 {
				t.Fatalf("expected 1 FTS row for message pk=%d, got %d", msg.PK, ftsExists)
			}

			goldenAtts := make([]ingest.ParsedAttachment, 0, len(atts))
			for _, a := range atts {
				cid := ""
				if a.ContentID != nil {
					cid = *a.ContentID
				}
				goldenAtts = append(goldenAtts, ingest.ParsedAttachment{
					PartPath:    a.PartPath,
					Filename:    a.Filename,
					ContentType: a.ContentType,
					SizeBytes:   a.SizeBytes,
					ContentID:   cid,
					Disposition: a.Disposition,
					SHA256:      a.SHA256,
				})
			}

			msgIDHdr := ""
			if msg.MessageIDHeader != nil {
				msgIDHdr = *msg.MessageIDHeader
			}

			summary := goldenSummary{
				Subject:         msg.Subject,
				FromName:        msg.FromName,
				FromAddr:        msg.FromAddr,
				To:              msg.To,
				Cc:              msg.Cc,
				Bcc:             msg.Bcc,
				ReplyTo:         msg.ReplyTo,
				MessageIDHeader: msgIDHdr,
				DateHeader:      msg.DateHeader,
				Snippet:         msg.Snippet,
				HasHTML:         msg.HasHTML,
				HasText:         msg.HasText,
				TextGenerated:   msg.TextGenerated,
				AttachmentCount: msg.AttachmentCount,
				Attachments:     goldenAtts,
				ParseWarnings:   msg.ParseWarnings,
				ExtractedTags:   ref.Tags,
			}

			gotJSON, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				t.Fatalf("marshal golden: %v", err)
			}
			gotJSON = append(gotJSON, '\n')

			goldenPath := strings.TrimSuffix(emlPath, ".eml") + ".golden.json"
			if *updateGolden {
				if err := os.WriteFile(goldenPath, gotJSON, 0o600); err != nil {
					t.Fatalf("write golden %s: %v", goldenPath, err)
				}
				return
			}

			//nolint:gosec // G304: golden paths correspond to repo testdata/mime/*.eml
			wantJSON, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s (run with -update to generate): %v", goldenPath, err)
			}
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("golden mismatch for %s:\ngot:\n%s\nwant:\n%s", name, string(gotJSON), string(wantJSON))
			}
		})
	}
}

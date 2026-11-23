package search_test

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestIndexTxAndDiacriticsAndDeleteTrigger(t *testing.T) {
	t.Parallel()

	// FR-080: unicode61 remove_diacritics 2 indexes subject/from/to/body/attachments,
	// and deleting the message removes its FTS row via trigger.
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "fts-test",
		Name:     "FTS Test",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var msgPK int64
	longBody := strings.Repeat("ção ", 80_000) // > 256 KiB multi-byte UTF-8
	err = st.WithTx(ctx, func(tx *store.Tx) error {
		msg, err := tx.InsertMessage(ctx, store.InsertMessageParams{
			ID:           "01JXXXXXXXXXXXXXXXXXXXXXX2",
			ProjectID:    proj.ID,
			ReceivedAt:   now,
			Subject:      "Fatura de João",
			FromName:     "João Silva",
			FromAddr:     "joao@example.test",
			EnvelopeFrom: "joao@example.test",
			EnvelopeTo:   []string{"cliente@example.test"},
			HasText:      true,
			SizeBytes:    100,
			BlobPath:     "1/2026/11/01JXXXXXXXXXXXXXXXXXXXXXX2.eml.zst",
		})
		if err != nil {
			return err
		}
		msgPK = msg.PK
		return search.IndexTx(ctx, tx, msg.PK, search.Document{
			Subject:     "Fatura de João",
			FromText:    "João Silva joao@example.test",
			ToText:      "cliente@example.test",
			Body:        longBody,
			Attachments: "recibo.pdf",
		})
	})
	if err != nil {
		t.Fatalf("WithTx insert+index: %v", err)
	}

	// Verify diacritic-insensitive match for "joao" and "fatura" and attachment "recibo".
	for _, q := range []string{"joao", "fatura", "recibo", "cao"} {
		var foundPK int64
		err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.SQLTx().QueryRowContext(
				ctx,
				`SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?`,
				q,
			).Scan(&foundPK)
		})
		if err != nil {
			t.Fatalf("FTS match %q failed: %v", q, err)
		}
		if foundPK != msgPK {
			t.Fatalf("FTS match %q: got rowid %d, want %d", q, foundPK, msgPK)
		}
	}

	// Deleting the project cascades to messages and triggers FTS cleanup.
	if err := st.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	var count int
	err = st.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SQLTx().QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`,
			"joao",
		).Scan(&count)
	})
	if err != nil {
		t.Fatalf("FTS post-delete query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 FTS matches after project delete, got %d", count)
	}
}

func TestTruncateUTF8PreservesValidity(t *testing.T) {
	t.Parallel()

	// Build a string > 256 KiB of 3-byte UTF-8 runes and verify IndexTx succeeds cleanly.
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "utf8-test",
		Name:     "UTF8 Test",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	body := strings.Repeat("€", search.MaxBodyIndexBytes) // 3 * 256 KiB
	if !utf8.ValidString(body) {
		t.Fatal("test setup invalid utf8")
	}

	err = st.WithTx(ctx, func(tx *store.Tx) error {
		msg, err := tx.InsertMessage(ctx, store.InsertMessageParams{
			ID:           "01JXXXXXXXXXXXXXXXXXXXXXX3",
			ProjectID:    proj.ID,
			ReceivedAt:   now,
			Subject:      "Euro test",
			EnvelopeFrom: "a@example.test",
			EnvelopeTo:   []string{"b@example.test"},
			HasText:      true,
			SizeBytes:    100,
			BlobPath:     "1/2026/11/01JXXXXXXXXXXXXXXXXXXXXXX3.eml.zst",
		})
		if err != nil {
			return err
		}
		return search.IndexTx(ctx, tx, msg.PK, search.Document{
			Subject: "Euro test",
			Body:    body,
		})
	})
	if err != nil {
		t.Fatalf("IndexTx large multibyte UTF-8: %v", err)
	}
}

// Package search manages full-text search indexing (FTS5) and (from Phase 5)
// query parsing and dynamic SQL building.
package search

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/store"
)

// MaxBodyIndexBytes is the maximum number of UTF-8 bytes indexed from a message
// text body into messages_fts (SPEC FR-080).
const MaxBodyIndexBytes = 256 * 1024

// Document holds the extracted text fields indexed for one message in messages_fts.
type Document struct {
	Subject     string
	FromText    string
	ToText      string
	Body        string
	Attachments string
}

// IndexTx inserts a message's FTS5 row inside the caller's write transaction.
// Deletions are handled automatically by the messages_fts_delete SQL trigger.
func IndexTx(ctx context.Context, tx *store.Tx, pk int64, doc Document) error {
	body := truncateUTF8Bytes(doc.Body, MaxBodyIndexBytes)
	_, err := tx.SQLTx().ExecContext(
		ctx,
		`INSERT INTO messages_fts (rowid, subject, from_text, to_text, body, attachments) VALUES (?, ?, ?, ?, ?, ?)`,
		pk,
		doc.Subject,
		doc.FromText,
		doc.ToText,
		body,
		doc.Attachments,
	)
	if err != nil {
		return fmt.Errorf("search: index message pk %d: %w", pk, err)
	}
	return nil
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

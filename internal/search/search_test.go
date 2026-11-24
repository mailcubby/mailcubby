package search_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestParseQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		wantTerms []search.Term
		wantErr   bool
	}{
		{
			name:      "empty query",
			input:     "   \t\n ",
			wantTerms: nil,
		},
		{
			name:  "single bare word gets prefix",
			input: "joao",
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "joao", Prefix: true},
			},
		},
		{
			name:  "multiple bare words only last gets prefix",
			input: "order inv",
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "order", Prefix: false},
				{Kind: search.TermText, Value: "inv", Prefix: true},
			},
		},
		{
			name:  "quoted phrase does not get prefix",
			input: `"order 12"`,
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "order 12", Quoted: true, Prefix: false},
			},
		},
		{
			name:  "negated bare word does not get prefix",
			input: "-spam",
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "spam", Negated: true, Prefix: false},
			},
		},
		{
			name:  "combined operators from acceptance criteria",
			input: `from:alice subject:"order 12" has:attachment -tag:spam after:2026-11-01`,
			wantTerms: []search.Term{
				{Kind: search.TermFrom, Value: "alice"},
				{Kind: search.TermSubject, Value: "order 12", Quoted: true},
				{Kind: search.TermHasAttachment, Value: "attachment"},
				{Kind: search.TermTag, Value: "spam", Negated: true},
				{Kind: search.TermAfter, Value: "2026-11-01", Time: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		{
			name:  "unknown operator treated as plain text",
			input: "foo:bar https://example.com",
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "foo:bar"},
				{Kind: search.TermText, Value: "https://example.com", Prefix: true},
			},
		},
		{
			name:  "unknown enum value for has/is treated as plain text",
			input: "has:magic is:archived",
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "has:magic"},
				{Kind: search.TermText, Value: "is:archived", Prefix: true},
			},
		},
		{
			name:  "empty operator value treated as plain text",
			input: "from:",
			wantTerms: []search.Term{
				{Kind: search.TermText, Value: "from:", Prefix: true},
			},
		},
		{
			name:  "rfc3339 date in before",
			input: "before:2026-11-01T15:30:00Z",
			wantTerms: []search.Term{
				{Kind: search.TermBefore, Value: "2026-11-01T15:30:00Z", Time: time.Date(2026, 11, 1, 15, 30, 0, 0, time.UTC)},
			},
		},
		{
			name:    "invalid before date returns error",
			input:   "before:not-a-date",
			wantErr: true,
		},
		{
			name:    "invalid after date returns error",
			input:   "after:2026-99-99",
			wantErr: true,
		},
		{
			name:    "query over max bytes returns error",
			input:   strings.Repeat("a", search.MaxQueryBytes+1),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := search.Parse(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) expected error, got nil", tc.input)
				}
				if !errors.Is(err, search.ErrInvalidQuery) {
					t.Fatalf("Parse(%q) error = %v, want ErrInvalidQuery", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.input, err)
			}
			if len(got.Terms) != len(tc.wantTerms) {
				t.Fatalf("Parse(%q) got %d terms (%+v), want %d (%+v)", tc.input, len(got.Terms), got.Terms, len(tc.wantTerms), tc.wantTerms)
			}
			for i := range tc.wantTerms {
				if got.Terms[i] != tc.wantTerms[i] {
					t.Errorf("term[%d] = %+v, want %+v", i, got.Terms[i], tc.wantTerms[i])
				}
			}
		})
	}
}

func TestSearchExecutionAndOperators(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := testutil.NewStore(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	projA, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "shop",
		Name:     "Shop",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	projB, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "other",
		Name:     "Other",
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}

	type seedSpec struct {
		projectID       int64
		id              string
		receivedAt      time.Time
		subject         string
		fromName        string
		fromAddr        string
		to              []store.Address
		body            string
		attachmentsText string
		attachmentCount int64
		hasHTML         bool
		hasText         bool
		seen            bool
		tags            []string
	}

	makeID := func(ts time.Time, b byte) string {
		var ent [10]byte
		ent[9] = b
		id := ulid.MustNew(ulid.Timestamp(ts), strings.NewReader(string(ent[:])))
		return id.String()
	}

	idJoao := makeID(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), 1)
	idOrderMatch := makeID(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), 2)
	idOrderSpam := makeID(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC), 3)
	idOrderOld := makeID(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), 4)
	idOrderNoAtt := makeID(time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC), 5)
	idOtherProject := makeID(time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC), 6)

	seeds := []seedSpec{
		{
			projectID:  projA.ID,
			id:         idJoao,
			receivedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
			subject:    "Olá João, sua FATURA chegou",
			fromName:   "Faturação",
			fromAddr:   "billing@shop.test",
			to:         []store.Address{{Name: "João Silva", Address: "joao@cliente.test"}},
			body:       "Segue a sua Fatura mensal.",
			hasHTML:    false,
			hasText:    true,
			seen:       true,
			tags:       []string{"invoice"},
		},
		{
			projectID:       projA.ID,
			id:              idOrderMatch,
			receivedAt:      time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC),
			subject:         "Confirmation for order 12 ready",
			fromName:        "Alice Wonderland",
			fromAddr:        "alice@shop.test",
			to:              []store.Address{{Name: "Bob", Address: "bob@example.test"}},
			body:            "Thank you for purchasing widget alpha.",
			attachmentsText: "receipt.pdf",
			attachmentCount: 1,
			hasHTML:         true,
			hasText:         true,
			seen:            false,
			tags:            []string{"invoice", "urgent"},
		},
		{
			projectID:       projA.ID,
			id:              idOrderSpam,
			receivedAt:      time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC),
			subject:         "Confirmation for order 12 spammy",
			fromName:        "Alice",
			fromAddr:        "alice@shop.test",
			to:              []store.Address{{Address: "bob@example.test"}},
			body:            "Spam copy of order 12.",
			attachmentsText: "spam.pdf",
			attachmentCount: 1,
			hasHTML:         true,
			hasText:         false,
			seen:            false,
			tags:            []string{"spam"},
		},
		{
			projectID:       projA.ID,
			id:              idOrderOld,
			receivedAt:      time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
			subject:         "Old order 12",
			fromName:        "Alice",
			fromAddr:        "alice@shop.test",
			to:              []store.Address{{Address: "bob@example.test"}},
			body:            "Too old.",
			attachmentsText: "old.pdf",
			attachmentCount: 1,
			hasHTML:         true,
			hasText:         true,
			seen:            true,
		},
		{
			projectID:       projA.ID,
			id:              idOrderNoAtt,
			receivedAt:      time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC),
			subject:         "order 12 without attachment",
			fromName:        "Alice",
			fromAddr:        "alice@shop.test",
			to:              []store.Address{{Address: "carol@example.test"}},
			body:            "No files here.",
			attachmentCount: 0,
			hasHTML:         false,
			hasText:         true,
			seen:            false,
		},
		{
			projectID:       projB.ID,
			id:              idOtherProject,
			receivedAt:      time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
			subject:         "João order 12 in other project",
			fromName:        "Alice",
			fromAddr:        "alice@other.test",
			to:              []store.Address{{Address: "bob@example.test"}},
			body:            "Must never leak across projects.",
			attachmentsText: "secret.pdf",
			attachmentCount: 1,
			hasHTML:         true,
			hasText:         true,
			seen:            false,
		},
	}

	for _, s := range seeds {
		err := st.WithTx(ctx, func(tx *store.Tx) error {
			msg, txErr := tx.InsertMessage(ctx, store.InsertMessageParams{
				ID:              s.id,
				ProjectID:       s.projectID,
				ReceivedAt:      s.receivedAt,
				Subject:         s.subject,
				FromName:        s.fromName,
				FromAddr:        s.fromAddr,
				To:              s.to,
				EnvelopeFrom:    s.fromAddr,
				EnvelopeTo:      []string{"dest@example.test"},
				Snippet:         s.body,
				HasHTML:         s.hasHTML,
				HasText:         s.hasText,
				AttachmentCount: s.attachmentCount,
				SizeBytes:       128,
				BlobPath:        "1/2026/11/" + s.id + ".eml.zst",
				Seen:            s.seen,
			})
			if txErr != nil {
				return txErr
			}

			var toParts []string
			for _, a := range s.to {
				toParts = append(toParts, a.Name, a.Address)
			}
			if txErr := search.IndexTx(ctx, tx, msg.PK, search.Document{
				Subject:     s.subject,
				FromText:    s.fromName + " " + s.fromAddr,
				ToText:      strings.Join(toParts, " "),
				Body:        s.body,
				Attachments: s.attachmentsText,
			}); txErr != nil {
				return txErr
			}

			for _, tagName := range s.tags {
				_, txErr := tx.SQLTx().ExecContext(
					ctx,
					`INSERT INTO tags (project_id, name, color, created_at) VALUES (?, ?, 'slate', ?) ON CONFLICT(project_id, name) DO NOTHING`,
					s.projectID, tagName, now.UnixMilli(),
				)
				if txErr != nil {
					return txErr
				}
				var tagID int64
				if txErr := tx.SQLTx().QueryRowContext(
					ctx,
					`SELECT id FROM tags WHERE project_id = ? AND name = ? COLLATE NOCASE`,
					s.projectID, tagName,
				).Scan(&tagID); txErr != nil {
					return txErr
				}
				if _, txErr := tx.SQLTx().ExecContext(
					ctx,
					`INSERT INTO message_tags (message_pk, tag_id, project_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
					msg.PK, tagID, s.projectID,
				); txErr != nil {
					return txErr
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("seed message %s: %v", s.id, err)
		}
	}

	cases := []struct {
		name    string
		query   string
		opts    search.BuildOptions
		wantIDs []string
	}{
		{
			// FR-080 diacritic folding
			name:    "diacritic insensitive joao finds João",
			query:   "joao",
			wantIDs: []string{idJoao},
		},
		{
			// FR-080 case insensitive
			name:    "case insensitive fatura",
			query:   "fatura",
			wantIDs: []string{idJoao},
		},
		{
			// Phase 5 acceptance criterion
			name:    "combined operators acceptance query",
			query:   `from:alice subject:"order 12" has:attachment -tag:spam after:2026-11-01`,
			wantIDs: []string{idOrderMatch},
		},
		{
			name:    "prefix match on last bare word",
			query:   "widg",
			wantIDs: []string{idOrderMatch},
		},
		{
			name:    "to operator matches recipient",
			query:   "to:carol",
			wantIDs: []string{idOrderNoAtt},
		},
		{
			name:    "is:unread and has:html",
			query:   "is:unread has:html",
			wantIDs: []string{idOrderSpam, idOrderMatch},
		},
		{
			name:    "is:read and has:text",
			query:   "is:read has:text",
			wantIDs: []string{idJoao, idOrderOld},
		},
		{
			name:    "before date",
			query:   "before:2026-11-01",
			wantIDs: []string{idOrderOld},
		},
		{
			name:    "standalone negated FTS term",
			query:   "-spammy -joao",
			wantIDs: []string{idOrderNoAtt, idOrderMatch, idOrderOld},
		},
		{
			name:    "tag filter via opts case-insensitive",
			query:   "",
			opts:    search.BuildOptions{Tag: "URGENT"},
			wantIDs: []string{idOrderMatch},
		},
		{
			name:    "attachment filename indexed in FTS",
			query:   "receipt.pdf",
			wantIDs: []string{idOrderMatch},
		},
		{
			name:    "punctuation-only query returns zero rows without SQL error",
			query:   "??? ---",
			wantIDs: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := search.Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.query, err)
			}
			msgs, err := search.Messages(ctx, st, projA.ID, parsed, tc.opts)
			if err != nil {
				t.Fatalf("Messages(%q): %v", tc.query, err)
			}
			gotIDs := make([]string, 0, len(msgs))
			for _, m := range msgs {
				gotIDs = append(gotIDs, m.ID)
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("query %q returned %v, want %v", tc.query, gotIDs, tc.wantIDs)
			}
			for i := range tc.wantIDs {
				if gotIDs[i] != tc.wantIDs[i] {
					t.Errorf("query %q result[%d] = %s, want %s", tc.query, i, gotIDs[i], tc.wantIDs[i])
				}
			}
		})
	}
}

func TestBuilderNeverInterpolatesUserInput(t *testing.T) {
	t.Parallel()

	sentinel := "USER_SENTINEL_ZZZ999"
	inputs := []string{
		sentinel,
		`"` + sentinel + `"`,
		`from:` + sentinel,
		`to:` + sentinel,
		`subject:"` + sentinel + `"`,
		`tag:` + sentinel,
		`-tag:` + sentinel,
		`-` + sentinel,
		`unknown:` + sentinel,
		`'; DROP TABLE messages; -- ` + sentinel,
	}

	for _, raw := range inputs {
		q, err := search.Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		sqlStr, args := search.Build(42, q, search.BuildOptions{Tag: sentinel, BeforeID: sentinel, Limit: 25})
		if strings.Contains(sqlStr, sentinel) {
			t.Fatalf("SQL leaked user input for %q:\n%s", raw, sqlStr)
		}
		if strings.Contains(sqlStr, "DROP TABLE") {
			t.Fatalf("SQL leaked injection text for %q:\n%s", raw, sqlStr)
		}
		if len(args) == 0 {
			t.Fatalf("expected bind args for %q", raw)
		}
	}
}

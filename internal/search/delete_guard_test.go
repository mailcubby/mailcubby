package search_test

import (
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// TestDeleteMessagesByQueryRejectsNoOpFilters verifies that a filtered
// delete-all never degrades into deleting the whole project when its terms do
// not narrow the selection (FR-076, FR-081, FR-082).
func TestDeleteMessagesByQueryRejectsNoOpFilters(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := testutil.NewStore(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug: "shop", Name: "Shop", Color: "blue", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	cred, err := st.CreateSMTPCredential(ctx, store.CreateSMTPCredentialParams{
		ProjectID: proj.ID, Label: "staging", Username: "shop-stage001", PasswordEnc: []byte{1}, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}

	seed := func(i int, subject, fromAddr string, tags []string) {
		t.Helper()
		at := now.Add(time.Duration(i) * time.Second)
		id := ulid.MustNew(ulid.Timestamp(at), ulid.DefaultEntropy()).String()
		credID := cred.ID
		err := st.WithTx(ctx, func(tx *store.Tx) error {
			msg, txErr := tx.InsertMessage(ctx, store.InsertMessageParams{
				ID: id, ProjectID: proj.ID, SMTPCredentialID: &credID, ReceivedAt: at, Subject: subject,
				FromAddr: fromAddr, EnvelopeFrom: fromAddr, EnvelopeTo: []string{"x@y.test"},
				HasText: true, SizeBytes: 10, BlobPath: "1/2026/11/" + id + ".eml.zst",
			})
			if txErr != nil {
				return txErr
			}
			if _, txErr := tx.EnsureAndAttachTagsTx(ctx, proj.ID, msg.PK, tags, at); txErr != nil {
				return txErr
			}
			return search.IndexTx(ctx, tx, msg.PK, search.Document{Subject: subject, FromText: fromAddr})
		})
		if err != nil {
			t.Fatalf("seed %q: %v", subject, err)
		}
	}
	seed(1, "hello one", "alice@shop.test", []string{"invoice"})
	seed(2, "hello two", "bob@shop.test", nil)
	seed(3, "alphaᦰbeta", "carol@shop.test", nil)

	count := func() int {
		t.Helper()
		msgs, err := search.Messages(ctx, st, proj.ID, search.Query{}, search.BuildOptions{Limit: 100})
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		return len(msgs)
	}

	// U+19B0 is a separator for unicode61, so "beta" is its own token (FR-080).
	q, err := search.Parse("beta")
	if err != nil {
		t.Fatalf("Parse(beta): %v", err)
	}
	if msgs, err := search.Messages(ctx, st, proj.ID, q, search.BuildOptions{Limit: 10}); err != nil || len(msgs) != 1 {
		t.Fatalf("search beta = %d results (err %v), want 1", len(msgs), err)
	}

	rejected := []string{
		`-"!"`,
		`-!`,
		`-*`,
		`-"!" -from:?`,
		"-ᦰ",                            // Go letter, unicode61 separator: no token.
		"-\U0001E030",                   // tokenized, but excludes nothing here.
		`-credential:nope`,              // unknown credential excludes nothing.
		`-tag:nope`,                     // unknown tag excludes nothing.
		`-from:nobody -subject:missing`, // exclusions that exclude nothing.
	}
	for _, raw := range rejected {
		q, err := search.Parse(raw)
		if err != nil || q.IsEmpty() {
			t.Fatalf("Parse(%q) = %+v, %v; want non-empty query", raw, q, err)
		}
		deleted, _, err := search.DeleteMessagesByQuery(ctx, st, proj.ID, q, nil, now)
		if !errors.Is(err, search.ErrNoEffectiveFilter) {
			t.Fatalf("DeleteMessagesByQuery(%q) err = %v, want ErrNoEffectiveFilter", raw, err)
		}
		if len(deleted) != 0 {
			t.Fatalf("DeleteMessagesByQuery(%q) deleted %d messages, want 0", raw, len(deleted))
		}
		if got := count(); got != 3 {
			t.Fatalf("after DeleteMessagesByQuery(%q) %d messages remain, want 3", raw, got)
		}
	}

	// A positive term without tokens matches nothing and deletes nothing.
	q, _ = search.Parse(`"!"`)
	deleted, _, err := search.DeleteMessagesByQuery(ctx, st, proj.ID, q, nil, now)
	if err != nil || len(deleted) != 0 {
		t.Fatalf(`DeleteMessagesByQuery("!") = %d, %v; want 0, nil`, len(deleted), err)
	}

	// Exclusions that do exclude something still work.
	q, _ = search.Parse(`-from:alice -"!"`)
	deleted, _, err = search.DeleteMessagesByQuery(ctx, st, proj.ID, q, nil, now)
	if err != nil {
		t.Fatalf("DeleteMessagesByQuery(-from:alice): %v", err)
	}
	if remaining := count(); len(deleted) != 2 || remaining != 1 {
		t.Fatalf("DeleteMessagesByQuery(-from:alice) deleted %d, %d remain; want 2, 1", len(deleted), remaining)
	}
}

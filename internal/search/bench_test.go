package search_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

const benchSearchMessageCount = 100_000

// BenchmarkSearchTwoWords100k measures NFR-004: two-word FTS5 search p95 latency
// under 150 ms on a project containing 100,000 indexed messages.
func BenchmarkSearchTwoWords100k(b *testing.B) {
	ctx := b.Context()
	st := testutil.NewStore(b)
	baseTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "bench-search",
		Name:     "Bench Search",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      baseTime,
	})
	if err != nil {
		b.Fatalf("CreateProject: %v", err)
	}

	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		msgStmt, txErr := tx.SQLTx().PrepareContext(ctx, `
			INSERT INTO messages (
				id, project_id, received_at, subject, from_name, from_addr,
				to_json, cc_json, bcc_json, reply_to_json, envelope_from, envelope_to_json,
				snippet, has_html, has_text, text_generated, attachment_count, size_bytes, blob_path, seen, tls
			) VALUES (?, ?, ?, ?, ?, ?, '[]', '[]', '[]', '[]', ?, '[]', ?, 1, 1, 0, 0, 1024, ?, 0, 0)
		`)
		if txErr != nil {
			return txErr
		}
		defer func() { _ = msgStmt.Close() }()

		ftsStmt, txErr := tx.SQLTx().PrepareContext(ctx, `
			INSERT INTO messages_fts (rowid, subject, from_text, to_text, body, attachments)
			VALUES (?, ?, ?, ?, ?, '')
		`)
		if txErr != nil {
			return txErr
		}
		defer func() { _ = ftsStmt.Close() }()

		var ent [10]byte
		for i := range benchSearchMessageCount {
			ts := baseTime.Add(time.Duration(i) * time.Second)
			ent[6] = byte(i >> 24)
			ent[7] = byte(i >> 16)
			ent[8] = byte(i >> 8)
			ent[9] = byte(i)
			var id ulid.ULID
			if err := id.SetTime(ulid.Timestamp(ts)); err != nil {
				return err
			}
			copy(id[6:], ent[:])
			idStr := id.String()

			var subject, body string
			switch {
			case i%100 == 0:
				subject = fmt.Sprintf("Invoice confirmation #%d", i)
				body = "Your invoice confirmation is ready for download from our billing portal."
			case i%10 == 0:
				subject = fmt.Sprintf("Monthly invoice #%d", i)
				body = "Attached is your monthly billing summary for review."
			default:
				subject = fmt.Sprintf("Account notification #%d", i)
				body = "Routine status update from the application notification worker."
			}

			res, execErr := msgStmt.ExecContext(
				ctx,
				idStr,
				proj.ID,
				ts.UnixMilli(),
				subject,
				"Billing Desk",
				"billing@shop.test",
				"billing@shop.test",
				body,
				"1/2026/11/"+idStr+".eml.zst",
			)
			if execErr != nil {
				return execErr
			}
			pk, idErr := res.LastInsertId()
			if idErr != nil {
				return idErr
			}
			if _, ftsErr := ftsStmt.ExecContext(
				ctx,
				pk,
				subject,
				"Billing Desk billing@shop.test",
				"customer@example.test",
				body,
			); ftsErr != nil {
				return ftsErr
			}
		}
		return nil
	}); err != nil {
		b.Fatalf("seed 100k messages + FTS: %v", err)
	}

	parsed, err := search.Parse("invoice confirmation")
	if err != nil {
		b.Fatalf("Parse: %v", err)
	}

	durations := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		msgs, searchErr := search.Messages(ctx, st, proj.ID, parsed, search.BuildOptions{Limit: 50})
		if searchErr != nil {
			b.Fatalf("search.Messages: %v", searchErr)
		}
		if len(msgs) != 50 {
			b.Fatalf("expected 50 search results, got %d", len(msgs))
		}
		durations = append(durations, time.Since(start))
	}
	b.StopTimer()

	slices.Sort(durations)
	if len(durations) > 0 {
		p50 := durations[(len(durations)-1)*50/100]
		p95 := durations[(len(durations)-1)*95/100]
		b.ReportMetric(float64(p50)/float64(time.Millisecond), "p50-ms/op")
		b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms/op")
	}
}

package store_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

const benchMessageCount = 100_000

// BenchmarkListMessagesFirstPage100k measures NFR-003: first page (50 rows)
// p95 latency under 50 ms on a project containing 100,000 messages.
func BenchmarkListMessagesFirstPage100k(b *testing.B) {
	ctx := b.Context()
	st := testutil.NewStore(b)
	baseTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "bench-list",
		Name:     "Bench List",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      baseTime,
	})
	if err != nil {
		b.Fatalf("CreateProject: %v", err)
	}

	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		stmt, txErr := tx.SQLTx().PrepareContext(ctx, `
			INSERT INTO messages (
				id, project_id, received_at, subject, from_name, from_addr,
				to_json, cc_json, bcc_json, reply_to_json, envelope_from, envelope_to_json,
				snippet, has_html, has_text, text_generated, attachment_count, size_bytes, blob_path, seen, tls
			) VALUES (?, ?, ?, ?, ?, ?, '[]', '[]', '[]', '[]', ?, '[]', ?, 1, 1, 0, 0, 1024, ?, 0, 0)
		`)
		if txErr != nil {
			return txErr
		}
		defer func() { _ = stmt.Close() }()

		var ent [10]byte
		for i := range benchMessageCount {
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

			if _, execErr := stmt.ExecContext(
				ctx,
				idStr,
				proj.ID,
				ts.UnixMilli(),
				fmt.Sprintf("Order #%d status update", i),
				"Shop Bot",
				"orders@shop.test",
				"orders@shop.test",
				"Your order has been updated and is ready for dispatch.",
				"1/2026/11/"+idStr+".eml.zst",
			); execErr != nil {
				return execErr
			}
		}
		return nil
	}); err != nil {
		b.Fatalf("seed 100k messages: %v", err)
	}

	durations := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		msgs, listErr := st.ListMessagesPage(ctx, proj.ID, "", 50)
		if listErr != nil {
			b.Fatalf("ListMessagesPage: %v", listErr)
		}
		if len(msgs) != 50 {
			b.Fatalf("expected 50 messages, got %d", len(msgs))
		}
		pks := make([]int64, len(msgs))
		for i, m := range msgs {
			pks[i] = m.PK
		}
		if _, tagErr := st.ListTagsByMessagePKs(ctx, proj.ID, pks); tagErr != nil {
			b.Fatalf("ListTagsByMessagePKs: %v", tagErr)
		}
		durations = append(durations, time.Since(start))
	}
	b.StopTimer()

	reportPercentiles(b, durations)
}

func reportPercentiles(b *testing.B, durations []time.Duration) {
	b.Helper()
	if len(durations) == 0 {
		return
	}
	slices.Sort(durations)
	p50 := durations[(len(durations)-1)*50/100]
	p95 := durations[(len(durations)-1)*95/100]
	b.ReportMetric(float64(p50)/float64(time.Millisecond), "p50-ms/op")
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms/op")
}

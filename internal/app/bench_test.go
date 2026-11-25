package app_test

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/store"
)

const benchStartupMessageCount = 1_000_000

// BenchmarkStartupReady1M measures NFR-007: time from app.Open + app.Serve
// until GET /readyz returns 200 on a data directory containing 1,000,000
// messages (verifying startup performs no full-table scans and finishes < 1 s).
func BenchmarkStartupReady1M(b *testing.B) {
	ctx := b.Context()
	dataDir := b.TempDir()
	dbPath := filepath.Join(dataDir, "app.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}

	baseTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug:     "bench-1m",
		Name:     "Bench 1M",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      baseTime,
	})
	if err != nil {
		_ = st.Close()
		b.Fatalf("CreateProject: %v", err)
	}

	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		stmt, txErr := tx.SQLTx().PrepareContext(ctx, `
			INSERT INTO messages (
				id, project_id, received_at, subject, from_name, from_addr,
				to_json, cc_json, bcc_json, reply_to_json, envelope_from, envelope_to_json,
				snippet, has_html, has_text, text_generated, attachment_count, size_bytes, blob_path, seen, tls
			) VALUES (?, ?, ?, 'Benchmark subject', 'Bot', 'bot@example.test',
				'[]', '[]', '[]', '[]', 'bot@example.test', '[]',
				'Snippet text', 0, 1, 0, 0, 1024, ?, 0, 0)
		`)
		if txErr != nil {
			return txErr
		}
		defer func() { _ = stmt.Close() }()

		var ent [10]byte
		for i := range benchStartupMessageCount {
			ts := baseTime.Add(time.Duration(i) * time.Millisecond)
			ent[6] = byte(i >> 24)
			ent[7] = byte(i >> 16)
			ent[8] = byte(i >> 8)
			ent[9] = byte(i)
			var id ulid.ULID
			if setErr := id.SetTime(ulid.Timestamp(ts)); setErr != nil {
				return setErr
			}
			copy(id[6:], ent[:])
			idStr := id.String()
			if _, execErr := stmt.ExecContext(
				ctx,
				idStr,
				proj.ID,
				ts.UnixMilli(),
				"1/2026/11/"+idStr+".eml.zst",
			); execErr != nil {
				return execErr
			}
		}
		if _, upErr := tx.SQLTx().ExecContext(
			ctx,
			`UPDATE projects SET message_count = ?, unread_count = ?, size_bytes = ? WHERE id = ?`,
			benchStartupMessageCount,
			benchStartupMessageCount,
			int64(benchStartupMessageCount)*1024,
			proj.ID,
		); upErr != nil {
			return upErr
		}
		return nil
	}); err != nil {
		_ = st.Close()
		b.Fatalf("seed 1M messages: %v", err)
	}
	if err := st.Close(); err != nil {
		b.Fatalf("close seeded store: %v", err)
	}

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dataDir, true
		case "MAILCUBBY_SMTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_LOG_LEVEL":
			return "error", true
		default:
			return "", false
		}
	})
	if err != nil {
		b.Fatalf("config.Load: %v", err)
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	var totalReady time.Duration

	b.ResetTimer()
	for range b.N {
		runCtx, cancel := context.WithCancel(ctx)
		start := time.Now()
		a, openErr := app.Open(runCtx, cfg, app.Options{
			Clock:   clock.NewReal(),
			Version: "bench",
		})
		if openErr != nil {
			cancel()
			b.Fatalf("app.Open: %v", openErr)
		}

		serveDone := make(chan error, 1)
		go func() {
			serveDone <- a.Serve(runCtx)
		}()

		readyURL := fmt.Sprintf("http://%s/readyz", a.HTTPAddr())
		deadline := time.Now().Add(5 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			req, reqErr := http.NewRequestWithContext(runCtx, http.MethodGet, readyURL, nil)
			if reqErr != nil {
				cancel()
				b.Fatalf("NewRequest: %v", reqErr)
			}
			resp, doErr := httpClient.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					ready = true
					break
				}
			}
			time.Sleep(time.Millisecond)
		}
		elapsed := time.Since(start)
		totalReady += elapsed

		b.StopTimer()
		cancel()
		if serveErr := <-serveDone; serveErr != nil {
			b.Fatalf("app.Serve shutdown: %v", serveErr)
		}
		if !ready {
			b.Fatalf("NFR-007: /readyz did not return 200 within 5s")
		}
		if elapsed >= time.Second {
			b.Fatalf("NFR-007: startup to ready took %s, want < 1s", elapsed)
		}
		b.StartTimer()
	}
	b.StopTimer()

	if b.N > 0 {
		avgMs := float64(totalReady) / float64(b.N) / float64(time.Millisecond)
		b.ReportMetric(avgMs, "ready-ms/op")
	}
}

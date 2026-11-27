package api_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/smtp"
	"slices"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// BenchmarkSSELatencyNFR002 measures NFR-002: p95 latency from SMTP DATA
// completion to SSE message.created delivery (target < 250 ms).
func BenchmarkSSELatencyNFR002(b *testing.B) {
	srv := testutil.StartServer(b, func(cfg *config.Config) {
		cfg.SMTPRateLimit = config.RateLimit{}
	})
	base := "http://" + srv.HTTPAddr

	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/events", nil)
	if err != nil {
		b.Fatalf("NewRequest: %v", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		b.Fatalf("GET /api/v1/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b.Fatalf("GET /api/v1/events status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	if _, err := readSSEFrame(b, reader); err != nil {
		b.Fatalf("read initial retry frame: %v", err)
	}

	client, err := smtp.Dial(srv.SMTPAddr)
	if err != nil {
		b.Fatalf("smtp.Dial: %v", err)
	}
	if err := client.Hello("bench.local"); err != nil {
		_ = client.Close()
		b.Fatalf("smtp.Hello: %v", err)
	}
	b.Cleanup(func() { _ = client.Quit() })

	durations := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := range b.N {
		raw := []byte(fmt.Sprintf("From: bench@example.test\r\nTo: dev@example.test\r\nSubject: Latency %d\r\n\r\nHello %d\r\n", i, i))
		if err := client.Mail("bench@example.test"); err != nil {
			b.Fatalf("iter %d MAIL: %v", i, err)
		}
		if err := client.Rcpt("dev@example.test"); err != nil {
			b.Fatalf("iter %d RCPT: %v", i, err)
		}
		wc, err := client.Data()
		if err != nil {
			b.Fatalf("iter %d DATA: %v", i, err)
		}
		if _, err := wc.Write(raw); err != nil {
			_ = wc.Close()
			b.Fatalf("iter %d Write: %v", i, err)
		}

		// Measure strictly from end of DATA (terminating dot + commit) to SSE event delivery.
		start := time.Now()
		if err := wc.Close(); err != nil {
			b.Fatalf("iter %d Close DATA: %v", i, err)
		}
		f, err := readSSEFrame(b, reader)
		if err != nil {
			b.Fatalf("iter %d read SSE frame: %v", i, err)
		}
		if f.event != "message.created" {
			b.Fatalf("iter %d got event %q, want message.created", i, f.event)
		}
		durations = append(durations, time.Since(start))
	}
	b.StopTimer()

	if len(durations) == 0 {
		return
	}
	slices.Sort(durations)
	p50 := durations[(len(durations)-1)*50/100]
	p95 := durations[(len(durations)*95+99)/100-1]
	b.ReportMetric(float64(p50)/float64(time.Millisecond), "p50-ms/op")
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms/op")
}

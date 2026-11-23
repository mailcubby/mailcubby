//go:build !race

package api_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestIntegrationSSELatencyNFR002(t *testing.T) {
	// NFR-002: p95 latency from SMTP DATA completion to SSE message.created under 250 ms over 20 sends.
	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	reader := bufio.NewReader(resp.Body)
	if _, err := readSSEFrame(t, reader); err != nil {
		t.Fatalf("read initial retry frame: %v", err)
	}

	const sends = 20
	latencies := make([]time.Duration, 0, sends)
	for i := range sends {
		raw := []byte(fmt.Sprintf("From: bench@example.test\r\nTo: dev@example.test\r\nSubject: Latency %d\r\n\r\nHello %d\r\n", i, i))
		start := time.Now()
		testutil.SendSMTP(t, srv.SMTPAddr, "bench@example.test", []string{"dev@example.test"}, raw)

		f, err := readSSEFrame(t, reader)
		if err != nil {
			t.Fatalf("send %d: read SSE frame: %v", i, err)
		}
		if f.event != "message.created" {
			t.Fatalf("send %d: got event %q, want message.created", i, f.event)
		}
		latencies = append(latencies, time.Since(start))
	}

	slices.Sort(latencies)
	// Nearest-rank p95 index for 20 samples: index 18 (19th of 20).
	p95 := latencies[(len(latencies)*95+99)/100-1]
	t.Logf("NFR-002 SSE latency over %d sends: min=%s median=%s p95=%s max=%s",
		sends, latencies[0], latencies[len(latencies)/2], p95, latencies[len(latencies)-1])

	if p95 >= 250*time.Millisecond {
		t.Fatalf("NFR-002 p95 SSE latency = %s, want < 250ms", p95)
	}
}

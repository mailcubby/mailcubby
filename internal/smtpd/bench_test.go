package smtpd

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
)

// BenchmarkSMTPIngest measures NFR-001: sustained SMTP ingest throughput using
// 10 parallel net/smtp clients sending 10 KiB messages (target >= 200 msgs/s).
func BenchmarkSMTPIngest(b *testing.B) {
	ctx := b.Context()
	dir := b.TempDir()

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_SMTP_RATE_LIMIT":
			return "0", true
		default:
			return "", false
		}
	})
	if err != nil {
		b.Fatalf("config.Load: %v", err)
	}

	st, err := store.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })

	blobs, err := blob.NewStore(dir, cfg.ZstdLevel)
	if err != nil {
		b.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewReal()
	bus := events.NewBus()
	b.Cleanup(bus.Close)

	if _, _, err := st.EnsureProject(ctx, store.CreateProjectParams{
		Slug:     cfg.DefaultProject,
		Name:     cfg.DefaultProject,
		Color:    "slate",
		Settings: store.DefaultProjectSettings(),
		Now:      clk.Now(),
	}); err != nil {
		b.Fatalf("EnsureProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, bus, nil)
	srv := New(cfg, st, ing, clk, nil)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ln)
	}()
	b.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		if serveErr := <-serveDone; serveErr != nil {
			b.Errorf("smtp serve: %v", serveErr)
		}
	})

	addr := ln.Addr().String()

	// Build a ~10 KiB multipart-like RFC 5322 message (SPEC NFR-001).
	header := "From: Bench Sender <sender@bench.test>\r\n" +
		"To: Dev Inbox <dev@bench.test>\r\n" +
		"Subject: NFR-001 throughput benchmark message\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n"
	line := "The quick brown fox jumps over the lazy dog while benchmarking Mailcubby SMTP ingest.\r\n"
	repeats := max(1, (10*1024-len(header))/len(line))
	payload := []byte(header + strings.Repeat(line, repeats))

	const numClients = 10
	clients := make([]*smtp.Client, numClients)
	for i := range numClients {
		c, dialErr := smtp.Dial(addr)
		if dialErr != nil {
			b.Fatalf("smtp.Dial worker %d: %v", i, dialErr)
		}
		if heloErr := c.Hello("bench.local"); heloErr != nil {
			_ = c.Close()
			b.Fatalf("Hello worker %d: %v", i, heloErr)
		}
		clients[i] = c
	}
	b.Cleanup(func() {
		for _, c := range clients {
			if c != nil {
				_ = c.Quit()
			}
		}
	})

	var nextIdx atomic.Int64
	errCh := make(chan error, numClients)

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	start := time.Now()

	var wg sync.WaitGroup
	for w := range numClients {
		wg.Add(1)
		go func(c *smtp.Client, workerID int) {
			defer wg.Done()
			for {
				idx := int(nextIdx.Add(1) - 1)
				if idx >= b.N {
					return
				}
				if mailErr := c.Mail("sender@bench.test"); mailErr != nil {
					errCh <- fmt.Errorf("worker %d MAIL: %w", workerID, mailErr)
					return
				}
				if rcptErr := c.Rcpt("dev@bench.test"); rcptErr != nil {
					errCh <- fmt.Errorf("worker %d RCPT: %w", workerID, rcptErr)
					return
				}
				wc, dataErr := c.Data()
				if dataErr != nil {
					errCh <- fmt.Errorf("worker %d DATA: %w", workerID, dataErr)
					return
				}
				if _, writeErr := wc.Write(payload); writeErr != nil {
					_ = wc.Close()
					errCh <- fmt.Errorf("worker %d Write: %w", workerID, writeErr)
					return
				}
				if closeErr := wc.Close(); closeErr != nil {
					errCh <- fmt.Errorf("worker %d Close DATA: %w", workerID, closeErr)
					return
				}
			}
		}(clients[w], w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	b.StopTimer()

	close(errCh)
	for sendErr := range errCh {
		b.Fatalf("SMTP ingest benchmark failed: %v", sendErr)
	}

	if elapsed > 0 && b.N > 0 {
		msgsPerSec := float64(b.N) / elapsed.Seconds()
		b.ReportMetric(msgsPerSec, "msgs/s")
	}
}

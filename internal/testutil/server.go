package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
)

// Server holds a running Mailcubby instance bound to ephemeral loopback ports
// with a temp data directory scoped to the test.
type Server struct {
	App      *app.App
	SMTPAddr string
	HTTPAddr string
	Store    *store.Store
	Blobs    *blob.Store
	Bus      *events.Bus
	Ingestor *ingest.Ingestor
	Clock    *clock.Fake
}

// StartServer starts a full Mailcubby instance using cfg on random loopback
// ports with a fresh temporary data directory, and registers graceful shutdown
// via t.Cleanup. Zero-value cfg uses local mode defaults.
func StartServer(t testing.TB, mutate func(*config.Config)) *Server {
	t.Helper()

	dataDir := t.TempDir()
	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dataDir, true
		case "MAILCUBBY_SMTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("testutil.StartServer: config.Load: %v", err)
	}
	if mutate != nil {
		mutate(&cfg)
	}

	fakeClk := clock.NewFake(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())

	a, err := app.Open(ctx, cfg, app.Options{
		Clock:   fakeClk,
		Version: "test",
	})
	if err != nil {
		cancel()
		t.Fatalf("testutil.StartServer: app.Open: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- a.Serve(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("testutil.StartServer: shutdown error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("testutil.StartServer: timed out waiting for shutdown")
		}
	})

	return &Server{
		App:      a,
		SMTPAddr: a.SMTPAddr(),
		HTTPAddr: a.HTTPAddr(),
		Store:    a.Store(),
		Blobs:    a.Blobs(),
		Bus:      a.Bus(),
		Ingestor: a.Ingestor(),
		Clock:    fakeClk,
	}
}

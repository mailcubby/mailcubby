package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
)

func TestAppOpenServeAndGracefulShutdown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create a stale tmp file to confirm CleanTmp runs on startup (SPEC 11).
	tmpDir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	staleFile := filepath.Join(tmpDir, "orphan.tmp")
	if err := os.WriteFile(staleFile, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_SMTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.Open(ctx, cfg, app.Options{
		Clock:   clock.NewFake(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)),
		Version: "test",
	})
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}

	// Default project created at startup (SPEC 5.1).
	if _, err := a.Store().GetProjectBySlug(ctx, "default"); err != nil {
		t.Fatalf("default project not created: %v", err)
	}
	// Stale tmp file cleaned.
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale tmp file removed, stat err = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- a.Serve(ctx)
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for graceful shutdown")
	}
}

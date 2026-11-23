package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/web"
)

func TestVersionSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %q)", code, stderr.String())
	}

	got := strings.TrimSpace(stdout.String())
	want := formatVersion(version, commit, date)
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, field := range []string{version, commit, date} {
		if !strings.Contains(got, field) {
			t.Errorf("version output %q missing %q", got, field)
		}
	}
}

func TestFormatVersion(t *testing.T) {
	t.Parallel()

	got := formatVersion("v0.1.0", "abc1234", "2026-11-23T00:00:00Z")
	for _, want := range []string{"v0.1.0", "abc1234", "2026-11-23T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatVersion output %q missing %q", got, want)
		}
	}
}

func TestUnknownSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"nope"}, &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected error message in stderr, got %q", stderr.String())
	}
}

func TestServeHTTPShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := serveHTTP(ctx, "127.0.0.1:0", web.Handler(), logger); err != nil {
		t.Fatalf("serveHTTP returned unexpected error: %v", err)
	}
}

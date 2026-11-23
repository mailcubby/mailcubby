package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/secrets"
)

func TestVersionSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
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
	code := run([]string{"nope"}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected error message in stderr, got %q", stderr.String())
	}
}

func TestAppRunImmediateShutdown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.Open(ctx, cfg, app.Options{Logger: logger, Version: "test"})
	if err != nil {
		cancel()
		t.Fatalf("app.Open returned unexpected error: %v", err)
	}
	cancel()
	if err := a.Serve(ctx); err != nil {
		t.Fatalf("a.Serve returned unexpected error: %v", err)
	}
}

func TestServeRejectsServerMode(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MAILCUBBY_MODE":     "server",
		"MAILCUBBY_BASE_URL": "https://mail.example.com",
		"MAILCUBBY_APP_KEY":  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve"}, strings.NewReader(""), &stdout, &stderr, func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "server mode is not implemented yet") {
		t.Fatalf("expected server mode error, got %q", stderr.String())
	}
}

func TestServeInvalidConfig(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MAILCUBBY_SMTP_MAX_MESSAGE_BYTES": "abc",
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve"}, strings.NewReader(""), &stdout, &stderr, func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "MAILCUBBY_SMTP_MAX_MESSAGE_BYTES") {
		t.Fatalf("expected error naming variable, got %q", stderr.String())
	}
}

func TestKeygenSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"keygen"}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %q)", code, stderr.String())
	}
	if _, err := secrets.ParseAppKey(stdout.String()); err != nil {
		t.Fatalf("keygen output %q is not a valid app key: %v", stdout.String(), err)
	}
}

func TestMigrateStatusSubcommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"migrate", "status"}, strings.NewReader(""), &stdout, &stderr, func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return dir, true
		}
		return "", false
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "00001_init.sql") {
		t.Fatalf("expected migration listing, got %q", stdout.String())
	}
}

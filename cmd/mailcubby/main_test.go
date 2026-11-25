package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
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

func TestServeServerModeRequiresTLSOrAllowInsecure(t *testing.T) {
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
	if !strings.Contains(stderr.String(), "MAILCUBBY_SMTP_TLS_CERT") {
		t.Fatalf("expected TLS validation error, got %q", stderr.String())
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

func TestHealthcheckSubcommandInvalidArgs(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"healthcheck", "extra"}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
}

// SPEC 5: serve exits non-zero when a data dir with users is opened without MAILCUBBY_MODE.
func TestServeRefusesImplicitLocalModeWithUsers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := st.CreateUser(t.Context(), store.CreateUserParams{Email: "owner@example.com", Name: "Owner", Now: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"serve"}, strings.NewReader(""), &stdout, &stderr, func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_SMTP_ADDR", "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		default:
			return "", false
		}
	})
	if code == 0 {
		t.Fatal("serve exited 0, want non-zero")
	}
	if !strings.Contains(stdout.String()+stderr.String(), "MAILCUBBY_MODE") {
		t.Fatalf("output does not name MAILCUBBY_MODE:\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
}

func TestServeHelp(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"--help", "-h", "-help"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run([]string{"serve", arg}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
			}
			for _, want := range []string{"Usage: mailcubby serve", "--help", "MAILCUBBY_MODE", "MAILCUBBY_DATA_DIR", "MAILCUBBY_HTTP_ADDR", "MAILCUBBY_SMTP_ADDR"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help output missing %q:\n%s", want, stdout.String())
				}
			}
		})
	}
}

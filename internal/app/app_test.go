package app_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
)

const testServerAppKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

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

func TestAppShutdownDoesNotWaitForIdleSMTPConnection(t *testing.T) {
	t.Parallel()

	// ARCHITECTURE 3.1: shutdown waits only for in-flight DATA. An idle pooled
	// SMTP connection (after EHLO) must not hold shutdown for the 30 s grace
	// period (Docker sends SIGKILL after 10 s).
	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return t.TempDir(), true
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
	defer cancel()
	a, err := app.Open(ctx, cfg, app.Options{
		Clock:   clock.NewFake(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)),
		Version: "test",
	})
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()

	c, err := smtp.Dial(a.SMTPAddr())
	if err != nil {
		t.Fatalf("smtp.Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello("client.example"); err != nil {
		t.Fatalf("EHLO: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for an idle SMTP connection")
	}
	if err := c.Noop(); err == nil {
		t.Fatal("idle SMTP connection still open after shutdown")
	}
}

func TestAppServerModeSetupTokenAndRotation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "http://127.0.0.1:8025", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKey, true
		case "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH":
			return "true", true
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

	// First startup logs setup token and stores its SHA-256 (FR-040).
	var logBuf1 bytes.Buffer
	a1, err := app.Open(t.Context(), cfg, app.Options{
		Logger: slog.New(slog.NewTextHandler(&logBuf1, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("first app.Open: %v", err)
	}
	tok1 := extractLoggedSetupToken(t, logBuf1.String())
	setting1, err := a1.Store().GetSetting(t.Context(), "setup_token_hash")
	if err != nil {
		t.Fatalf("GetSetting(setup_token_hash): %v", err)
	}
	var hash1 string
	if err := json.Unmarshal([]byte(setting1.Value), &hash1); err != nil {
		t.Fatalf("unmarshal hash1: %v", err)
	}
	if hash1 != auth.HashToken(tok1) {
		t.Fatalf("stored hash1 mismatch")
	}
	if err := a1.Close(); err != nil {
		t.Fatalf("a1.Close: %v", err)
	}

	// Second startup while still pending rotates the token (old token invalid).
	var logBuf2 bytes.Buffer
	a2, err := app.Open(t.Context(), cfg, app.Options{
		Logger: slog.New(slog.NewTextHandler(&logBuf2, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("second app.Open: %v", err)
	}
	defer func() { _ = a2.Close() }()

	tok2 := extractLoggedSetupToken(t, logBuf2.String())
	if tok2 == tok1 {
		t.Fatalf("expected setup token to rotate on restart")
	}
	setting2, err := a2.Store().GetSetting(t.Context(), "setup_token_hash")
	if err != nil {
		t.Fatalf("GetSetting(setup_token_hash) 2: %v", err)
	}
	var hash2 string
	if err := json.Unmarshal([]byte(setting2.Value), &hash2); err != nil {
		t.Fatalf("unmarshal hash2: %v", err)
	}
	if hash2 != auth.HashToken(tok2) {
		t.Fatalf("stored hash2 mismatch")
	}
}

func TestAppServerModeHeadlessAdminBootstrapAndSMTP530(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "http://127.0.0.1:8025", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKey, true
		case "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH":
			return "true", true
		case "MAILCUBBY_ADMIN_EMAIL":
			return "admin@example.test", true
		case "MAILCUBBY_ADMIN_PASSWORD":
			return "super-secret-pass", true
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
	a, err := app.Open(ctx, cfg, app.Options{Version: "test"})
	if err != nil {
		cancel()
		t.Fatalf("app.Open: %v", err)
	}

	// Admin user created and setup_complete audited (FR-040).
	u, err := a.Store().GetUserByEmail(ctx, "admin@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if !u.IsAdmin || !u.CanCreateProjects || u.PasswordHash == nil {
		t.Fatalf("unexpected bootstrapped admin: %+v", u)
	}
	entries, err := a.Store().ListAuditEntries(ctx, 10)
	if err != nil || len(entries) != 1 || entries[0].Action != "setup.complete" {
		t.Fatalf("unexpected audit entries: %+v, err=%v", entries, err)
	}

	// Unauthenticated SMTP in server mode is rejected with 530.
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()

	c, err := smtp.Dial(a.SMTPAddr())
	if err != nil {
		cancel()
		t.Fatalf("smtp.Dial: %v", err)
	}
	if err := c.Hello("localhost"); err != nil {
		_ = c.Close()
		cancel()
		t.Fatalf("EHLO: %v", err)
	}
	mailErr := c.Mail("sender@example.test")
	_ = c.Quit()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve error: %v", err)
	}

	if mailErr == nil || !strings.Contains(mailErr.Error(), "530") {
		t.Fatalf("unauthenticated MAIL FROM err = %v, want 530", mailErr)
	}
}

func TestAppServerModeTLSCertLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Missing cert fails startup naming the env var and file path (ARCHITECTURE 9.3).
	badCfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "https://mail.example.test", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKey, true
		case "MAILCUBBY_SMTP_TLS_CERT":
			return filepath.Join(dir, "missing.crt"), true
		case "MAILCUBBY_SMTP_TLS_KEY":
			return filepath.Join(dir, "missing.key"), true
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
	_, openErr := app.Open(t.Context(), badCfg, app.Options{})
	if openErr == nil || !strings.Contains(openErr.Error(), "MAILCUBBY_SMTP_TLS_CERT") || !strings.Contains(openErr.Error(), "missing.crt") {
		t.Fatalf("app.Open with missing cert err = %v, want error naming MAILCUBBY_SMTP_TLS_CERT and file", openErr)
	}

	// Valid self-signed cert loads cleanly.
	certPath, keyPath := writeSelfSignedCert(t, dir)
	goodCfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "https://mail.example.test", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKey, true
		case "MAILCUBBY_SMTP_TLS_CERT":
			return certPath, true
		case "MAILCUBBY_SMTP_TLS_KEY":
			return keyPath, true
		case "MAILCUBBY_SMTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load good: %v", err)
	}
	a, err := app.Open(t.Context(), goodCfg, app.Options{})
	if err != nil {
		t.Fatalf("app.Open with valid TLS cert: %v", err)
	}
	_ = a.Close()
}

func extractLoggedSetupToken(t *testing.T, logOutput string) string {
	t.Helper()
	const marker = "setup token: "
	idx := strings.Index(logOutput, marker)
	if idx < 0 {
		t.Fatalf("log output missing %q: %s", marker, logOutput)
	}
	rest := logOutput[idx+len(marker):]
	// TextHandler quotes msg values containing spaces, so stop at quote, space, or newline.
	end := strings.IndexAny(rest, "\" \r\n")
	if end < 0 {
		end = len(rest)
	}
	tok := strings.TrimSpace(rest[:end])
	if len(tok) < 32 {
		t.Fatalf("extracted setup token %q too short from %q", tok, logOutput)
	}
	return tok
}

func writeSelfSignedCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}

	certPath = filepath.Join(dir, "smtp.crt")
	keyPath = filepath.Join(dir, "smtp.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

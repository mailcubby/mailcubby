package app_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/tlsutil"
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

// TestAppSMTPSListenerAndCertReload verifies FR-002 (implicit TLS listener
// via MAILCUBBY_SMTPS_ADDR) and Phase 12 live certificate reload in App.Serve.
func TestAppSMTPSListenerAndCertReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	t0 := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)
	certPath, keyPath := writeSelfSignedCertWithSerial(t, dir, "first.mailcubby.test", 10, t0)

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "local", true
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_SMTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_SMTPS_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		case "MAILCUBBY_SMTP_TLS_CERT":
			return certPath, true
		case "MAILCUBBY_SMTP_TLS_KEY":
			return keyPath, true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	clk := clock.NewFake(t0)
	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.Open(ctx, cfg, app.Options{Clock: clk, Version: "test"})
	if err != nil {
		cancel()
		t.Fatalf("app.Open: %v", err)
	}
	if a.SMTPSAddr() == "" {
		cancel()
		_ = a.Close()
		t.Fatal("expected non-empty SMTPSAddr when MAILCUBBY_SMTPS_ADDR is configured")
	}

	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()

	// Wait for background workers (including TLS reloader) to register tickers.
	waitUntil := time.Now().Add(2 * time.Second)
	for clk.TickerCount() < 4 && time.Now().Before(waitUntil) {
		runtime.Gosched()
	}

	// Send a message over implicit TLS (FR-002) and verify cert serial 10.
	dialer := &tls.Dialer{
		Config: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // test self-signed cert
		},
	}
	rawConn, err := dialer.DialContext(t.Context(), "tcp", a.SMTPSAddr())
	if err != nil {
		cancel()
		t.Fatalf("tls.Dial(%s): %v", a.SMTPSAddr(), err)
	}
	conn := rawConn.(*tls.Conn)
	if got := conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64(); got != 10 {
		_ = conn.Close()
		cancel()
		t.Fatalf("initial SMTPS cert serial = %d, want 10", got)
	}
	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		_ = conn.Close()
		cancel()
		t.Fatalf("smtp.NewClient: %v", err)
	}
	if err := client.Mail("from@example.test"); err != nil {
		t.Fatalf("MAIL: %v", err)
	}
	if err := client.Rcpt("to@example.test"); err != nil {
		t.Fatalf("RCPT: %v", err)
	}
	w, err := client.Data()
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}
	_, _ = w.Write([]byte("Subject: App SMTPS\r\n\r\nBody\r\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("DATA close: %v", err)
	}
	_ = client.Quit()

	// Replace certificate files on disk and advance fake clock past 30s poll interval.
	writeSelfSignedCertWithSerial(t, dir, "second.mailcubby.test", 20, t0.Add(time.Minute))
	clk.Advance(tlsutil.DefaultPollInterval)

	// Poll via STARTTLS until the new cert is served without restart.
	var gotSerial int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, dialErr := smtp.Dial(a.SMTPAddr())
		if dialErr != nil {
			t.Fatalf("smtp.Dial: %v", dialErr)
		}
		if err := c.StartTLS(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // test self-signed cert
		}); err != nil {
			_ = c.Close()
			t.Fatalf("StartTLS: %v", err)
		}
		state, _ := c.TLSConnectionState()
		gotSerial = state.PeerCertificates[0].SerialNumber.Int64()
		_ = c.Quit()
		if gotSerial == 20 {
			break
		}
		runtime.Gosched()
	}
	if gotSerial != 20 {
		cancel()
		t.Fatalf("STARTTLS cert serial after reload = %d, want 20", gotSerial)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for graceful shutdown")
	}
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
	return writeSelfSignedCertWithSerial(t, dir, "localhost", 1, time.Time{})
}

func writeSelfSignedCertWithSerial(t *testing.T, dir, cn string, serial int64, mtime time.Time) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", cn},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
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
	if !mtime.IsZero() {
		if err := os.Chtimes(certPath, mtime, mtime); err != nil {
			t.Fatalf("chtimes cert: %v", err)
		}
		if err := os.Chtimes(keyPath, mtime, mtime); err != nil {
			t.Fatalf("chtimes key: %v", err)
		}
	}
	return certPath, keyPath
}

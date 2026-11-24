package smtpd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/tlsutil"
)

func writeTestCertPair(t *testing.T, certPath, keyPath, cn string, serial int64, mtime time.Time) {
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
}

// TestImplicitTLSAndLiveCertReload verifies FR-002 (implicit TLS listener) and
// Phase 12 hot certificate reloading across both STARTTLS and implicit TLS
// without restarting the SMTP server.
func TestImplicitTLSAndLiveCertReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "smtp.crt")
	keyPath := filepath.Join(dir, "smtp.key")
	t0 := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	writeTestCertPair(t, certPath, keyPath, "cert-v1.example.test", 111, t0)

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "https://mail.example.test", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKeyB64, true
		case "MAILCUBBY_SMTP_TLS_CERT":
			return certPath, true
		case "MAILCUBBY_SMTP_TLS_KEY":
			return keyPath, true
		case "MAILCUBBY_SMTPS_ADDR":
			return "127.0.0.1:0", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	st := openTestStore(t)
	blobs, err := blob.NewStore(dir, cfg.ZstdLevel)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewFake(t0)
	reloader, err := tlsutil.NewReloader(certPath, keyPath, tlsutil.DefaultPollInterval, clk, nil)
	if err != nil {
		t.Fatalf("tlsutil.NewReloader: %v", err)
	}

	ing := ingest.New(st, blobs, clk, nil, nil)
	srv := New(cfg, st, ing, clk, nil)
	srv.SetTLSConfig(reloader.TLSConfig())

	cipher, err := secrets.NewSMTPCredentialCipher(cfg.AppKey)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher: %v", err)
	}
	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "shop",
		Name:     "Shop",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      t0,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	sealed, err := cipher.Seal([]byte("secret-password-123456"), []byte("shop-abcd1234"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := st.CreateSMTPCredential(t.Context(), store.CreateSMTPCredentialParams{
		ProjectID:   proj.ID,
		Label:       "staging",
		Username:    "shop-abcd1234",
		PasswordEnc: sealed,
		Now:         t0,
	}); err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}

	var lc net.ListenConfig
	smtpLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp: %v", err)
	}
	smtpsLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		_ = smtpLn.Close()
		t.Fatalf("listen smtps: %v", err)
	}

	reloadCtx, stopReload := context.WithCancel(context.Background())
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		reloader.Run(reloadCtx)
	}()

	smtpDone := make(chan error, 1)
	go func() { smtpDone <- srv.Serve(smtpLn) }()
	smtpsDone := make(chan error, 1)
	go func() { smtpsDone <- srv.ServeTLS(smtpsLn) }()

	t.Cleanup(func() {
		stopReload()
		<-reloadDone
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if err := <-smtpDone; err != nil {
			t.Errorf("Serve: %v", err)
		}
		if err := <-smtpsDone; err != nil {
			t.Errorf("ServeTLS: %v", err)
		}
	})

	// Wait for reloader ticker registration on fake clock.
	waitDeadline := time.Now().Add(2 * time.Second)
	for clk.TickerCount() == 0 && time.Now().Before(waitDeadline) {
		runtime.Gosched()
	}
	if clk.TickerCount() == 0 {
		t.Fatal("reloader did not register ticker")
	}

	// 1. Implicit TLS (FR-002) serves cert v1 and accepts AUTH without STARTTLS.
	serial, cn := sendOverImplicitTLS(t, smtpsLn.Addr().String(), "shop-abcd1234", "secret-password-123456")
	if serial != 111 || cn != "cert-v1.example.test" {
		t.Fatalf("initial SMTPS cert = (serial=%d, cn=%q), want (111, cert-v1.example.test)", serial, cn)
	}

	// Verify message stored with tls=true.
	msgs, err := st.ListMessages(t.Context(), proj.ID, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = %d (err=%v), want 1", len(msgs), err)
	}
	if !msgs[0].TLS {
		t.Fatalf("stored implicit TLS message tls=%v, want true", msgs[0].TLS)
	}

	// 2. STARTTLS also serves cert v1 initially.
	serial, cn = inspectSTARTTLSCert(t, smtpLn.Addr().String())
	if serial != 111 || cn != "cert-v1.example.test" {
		t.Fatalf("initial STARTTLS cert = (serial=%d, cn=%q), want (111, cert-v1.example.test)", serial, cn)
	}

	// 3. Replace cert files on disk and advance fake clock past 30s poll interval.
	writeTestCertPair(t, certPath, keyPath, "cert-v2.example.test", 222, t0.Add(time.Minute))
	clk.Advance(tlsutil.DefaultPollInterval)

	// Wait for reloader to swap certificate.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cert, _ := reloader.Get(nil)
		if cert != nil && cert.Leaf != nil && cert.Leaf.SerialNumber.Int64() == 222 {
			break
		}
		runtime.Gosched()
	}

	// Both STARTTLS and implicit TLS now serve cert v2 without restart.
	serial, cn = inspectSTARTTLSCert(t, smtpLn.Addr().String())
	if serial != 222 || cn != "cert-v2.example.test" {
		t.Fatalf("reloaded STARTTLS cert = (serial=%d, cn=%q), want (222, cert-v2.example.test)", serial, cn)
	}
	serial, cn = sendOverImplicitTLS(t, smtpsLn.Addr().String(), "shop-abcd1234", "secret-password-123456")
	if serial != 222 || cn != "cert-v2.example.test" {
		t.Fatalf("reloaded SMTPS cert = (serial=%d, cn=%q), want (222, cert-v2.example.test)", serial, cn)
	}
}

// TestServeTLSShutdownDoesNotWaitForIdleConnection verifies that graceful
// shutdown closes idle implicit TLS connections immediately.
func TestServeTLSShutdownDoesNotWaitForIdleConnection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "smtp.crt")
	keyPath := filepath.Join(dir, "smtp.key")
	writeTestCertPair(t, certPath, keyPath, "idle.example.test", 7, time.Now())

	reloader, err := tlsutil.NewReloader(certPath, keyPath, tlsutil.DefaultPollInterval, nil, nil)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	srv := NewWithLookup(cfg, &fakeLookup{projects: map[string]store.Project{"default": {ID: 1, Slug: "default"}}}, stubIngestor{}, nil)
	srv.SetTLSConfig(reloader.TLSConfig())

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ServeTLS(ln) }()

	// Open an idle implicit TLS SMTP connection (EHLO only).
	dialer := &tls.Dialer{
		Config: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // test self-signed cert
		},
	}
	conn, err := dialer.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		t.Fatalf("smtp.NewClient: %v", err)
	}
	if err := client.Hello("client.example"); err != nil {
		t.Fatalf("EHLO: %v", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("ServeTLS: %v", err)
	}
}

func sendOverImplicitTLS(t *testing.T, addr, user, pass string) (serial int64, cn string) {
	t.Helper()
	dialer := &tls.Dialer{
		Config: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         "localhost",
			InsecureSkipVerify: true, //nolint:gosec // test self-signed cert
		},
	}
	rawConn, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("tls.Dial(%s): %v", addr, err)
	}
	conn := rawConn.(*tls.Conn)
	defer func() { _ = conn.Close() }()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		t.Fatal("implicit TLS connection has no peer certificate")
	}
	leaf := state.PeerCertificates[0]

	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		t.Fatalf("smtp.NewClient: %v", err)
	}
	if err := client.Auth(smtp.PlainAuth("", user, pass, "localhost")); err != nil {
		t.Fatalf("AUTH PLAIN over implicit TLS: %v", err)
	}
	if err := client.Mail("sender@example.test"); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}
	if err := client.Rcpt("dest@example.test"); err != nil {
		t.Fatalf("RCPT TO: %v", err)
	}
	w, err := client.Data()
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}
	if _, err := w.Write([]byte("From: sender@example.test\r\nTo: dest@example.test\r\nSubject: SMTPS\r\n\r\nHello implicit TLS\r\n")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close data: %v", err)
	}
	_ = client.Quit()

	return leaf.SerialNumber.Int64(), leaf.Subject.CommonName
}

func inspectSTARTTLSCert(t *testing.T, addr string) (serial int64, cn string) {
	t.Helper()
	client, err := smtp.Dial(addr)
	if err != nil {
		t.Fatalf("smtp.Dial(%s): %v", addr, err)
	}
	defer func() { _ = client.Close() }()
	if err := client.StartTLS(&tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         "localhost",
		InsecureSkipVerify: true, //nolint:gosec // test self-signed cert
	}); err != nil {
		t.Fatalf("StartTLS: %v", err)
	}
	state, ok := client.TLSConnectionState()
	if !ok || len(state.PeerCertificates) == 0 {
		t.Fatal("STARTTLS connection missing peer certificate")
	}
	leaf := state.PeerCertificates[0]
	_ = client.Quit()
	return leaf.SerialNumber.Int64(), leaf.Subject.CommonName
}

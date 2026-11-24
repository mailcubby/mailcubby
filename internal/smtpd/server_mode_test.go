package smtpd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/textproto"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
)

const testServerAppKeyB64 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

type testSMTPServer struct {
	addr   string
	store  *store.Store
	blobs  *blob.Store
	clk    *clock.Fake
	cipher *secrets.Cipher
	srv    *Server
}

func startServerModeSMTP(t *testing.T, mutate func(*config.Config)) *testSMTPServer {
	t.Helper()

	dataDir := t.TempDir()
	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dataDir, true
		case "MAILCUBBY_BASE_URL":
			return "http://mail.example.test:8025", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKeyB64, true
		case "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH":
			return "true", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load(server): %v", err)
	}
	if mutate != nil {
		mutate(&cfg)
	}

	st := openTestStore(t)
	blobs, err := blob.NewStore(dataDir, cfg.ZstdLevel)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC))
	ing := ingest.New(st, blobs, clk, nil, nil)
	srv := New(cfg, st, ing, clk, nil)

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if serveErr := <-done; serveErr != nil {
			t.Errorf("srv.Serve: %v", serveErr)
		}
	})

	cipher, err := secrets.NewSMTPCredentialCipher(cfg.AppKey)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher: %v", err)
	}
	return &testSMTPServer{
		addr:   ln.Addr().String(),
		store:  st,
		blobs:  blobs,
		clk:    clk,
		cipher: cipher,
		srv:    srv,
	}
}

func seedProjectAndCred(t *testing.T, ts *testSMTPServer, slug, label, username, password string, routingEnabled bool) (store.Project, store.SMTPCredential) {
	t.Helper()
	settings := store.DefaultProjectSettings()
	settings.RecipientRoutingEnabled = routingEnabled
	proj, err := ts.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: slug, Name: slug, Color: "blue", Settings: settings, Now: ts.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", slug, err)
	}
	sealed, err := ts.cipher.Seal([]byte(password), []byte(username))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	cred, err := ts.store.CreateSMTPCredential(t.Context(), store.CreateSMTPCredentialParams{
		ProjectID:   proj.ID,
		Label:       label,
		Username:    username,
		PasswordEnc: sealed,
		Now:         ts.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}
	return proj, cred
}

func generateSelfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
}

func waitForTimerCount(t *testing.T, clk *clock.Fake, wantAtLeast int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if clk.TimerCount() >= wantAtLeast {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("timed out waiting for fake clock TimerCount >= %d (got %d)", wantAtLeast, clk.TimerCount())
}

func sendAuthPlainRaw(addr, username, password string) (int, string, error) {
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)
	if _, _, err := tp.ReadResponse(220); err != nil {
		return 0, "", err
	}
	if err := tp.PrintfLine("EHLO client.test"); err != nil {
		return 0, "", err
	}
	if _, _, err := tp.ReadResponse(250); err != nil {
		return 0, "", err
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	if err := tp.PrintfLine("AUTH PLAIN %s", b64); err != nil {
		return 0, "", err
	}
	code, msg, err := tp.ReadResponse(0)
	_ = tp.PrintfLine("QUIT")
	return code, msg, err
}

// FR-021, FR-061, FR-062, FR-033: Server-mode AUTH PLAIN/LOGIN stores
// smtp_credential_id, throttles last_used_at to once per minute, and rejects
// disabled credentials and deleted projects with 535 5.7.8.
func TestServerMode_AuthAttributionLastUsedDisabledAndDeleted(t *testing.T) {
	t.Parallel()

	ts := startServerModeSMTP(t, nil)
	proj, cred := seedProjectAndCred(t, ts, "shop", "staging", "shop-abcd1234", "SuperSecretPassword12345", false)

	// Send via AUTH PLAIN.
	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", ts.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tp := textproto.NewConn(conn)
	_, _, _ = tp.ReadResponse(220)
	_ = tp.PrintfLine("EHLO app.test")
	_, _, _ = tp.ReadResponse(250)
	b64 := base64.StdEncoding.EncodeToString([]byte("\x00" + cred.Username + "\x00SuperSecretPassword12345"))
	_ = tp.PrintfLine("AUTH PLAIN %s", b64)
	if _, _, err := tp.ReadResponse(235); err != nil {
		t.Fatalf("AUTH PLAIN failed: %v", err)
	}
	_ = tp.PrintfLine("MAIL FROM:<app@shop.test>")
	if _, _, err := tp.ReadResponse(250); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}
	_ = tp.PrintfLine("RCPT TO:<customer@example.test>")
	if _, _, err := tp.ReadResponse(250); err != nil {
		t.Fatalf("RCPT TO: %v", err)
	}
	_ = tp.PrintfLine("DATA")
	_, _, _ = tp.ReadResponse(354)
	dw := tp.DotWriter()
	_, _ = dw.Write([]byte("Subject: Order #1\r\nFrom: app@shop.test\r\n\r\nHello\r\n"))
	_ = dw.Close()
	if _, _, err := tp.ReadResponse(250); err != nil {
		t.Fatalf("DATA: %v", err)
	}
	_ = tp.PrintfLine("QUIT")
	_ = conn.Close()

	// Verify smtp_credential_id and auth_user on stored message (FR-021).
	msgs, err := ts.store.ListMessages(t.Context(), proj.ID, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = (%d, %v), want 1", len(msgs), err)
	}
	if msgs[0].SMTPCredentialID == nil || *msgs[0].SMTPCredentialID != cred.ID {
		t.Fatalf("SMTPCredentialID = %v, want %d", msgs[0].SMTPCredentialID, cred.ID)
	}
	if msgs[0].AuthUser == nil || *msgs[0].AuthUser != cred.Username {
		t.Fatalf("AuthUser = %v, want %q", msgs[0].AuthUser, cred.Username)
	}

	// Verify last_used_at was set at t0 and throttled within 1 minute (FR-062).
	t0 := ts.clk.Now().UTC()
	gotCred, err := ts.store.GetSMTPCredentialByID(t.Context(), proj.ID, cred.ID)
	if err != nil || gotCred.LastUsedAt == nil || !gotCred.LastUsedAt.Equal(t0) {
		t.Fatalf("LastUsedAt after 1st AUTH = %v (err=%v), want %v", gotCred.LastUsedAt, err, t0)
	}

	ts.clk.Advance(30 * time.Second)
	if code, msg, err := sendAuthPlainRaw(ts.addr, cred.Username, "SuperSecretPassword12345"); err != nil || code != 235 {
		t.Fatalf("2nd AUTH = (%d, %q, %v), want 235", code, msg, err)
	}
	gotCred, _ = ts.store.GetSMTPCredentialByID(t.Context(), proj.ID, cred.ID)
	if !gotCred.LastUsedAt.Equal(t0) {
		t.Fatalf("LastUsedAt updated too early: got %v, want %v", gotCred.LastUsedAt, t0)
	}

	ts.clk.Advance(31 * time.Second)
	t1 := ts.clk.Now().UTC()
	if code, msg, err := sendAuthPlainRaw(ts.addr, cred.Username, "SuperSecretPassword12345"); err != nil || code != 235 {
		t.Fatalf("3rd AUTH = (%d, %q, %v), want 235", code, msg, err)
	}
	gotCred, _ = ts.store.GetSMTPCredentialByID(t.Context(), proj.ID, cred.ID)
	if !gotCred.LastUsedAt.Equal(t1) {
		t.Fatalf("LastUsedAt did not update after 1 min: got %v, want %v", gotCred.LastUsedAt, t1)
	}

	// Disabled credential fails with 535 5.7.8 (FR-062).
	disabledAt := ts.clk.Now().UTC()
	if _, err := ts.store.UpdateSMTPCredential(t.Context(), proj.ID, cred.ID, cred.Label, &disabledAt); err != nil {
		t.Fatalf("disable credential: %v", err)
	}
	done := make(chan struct {
		code int
		msg  string
		err  error
	}, 1)
	go func() {
		c, m, e := sendAuthPlainRaw(ts.addr, cred.Username, "SuperSecretPassword12345")
		done <- struct {
			code int
			msg  string
			err  error
		}{c, m, e}
	}()
	waitForTimerCount(t, ts.clk, 1)
	ts.clk.Advance(time.Second)
	res := <-done
	if res.code != 535 || !strings.Contains(res.msg, "5.7.8 Authentication credentials invalid") {
		t.Fatalf("disabled credential AUTH = (%d, %q, %v), want 535 5.7.8 Authentication credentials invalid", res.code, res.msg, res.err)
	}

	// Re-enable, then delete project -> 535 5.7.8 immediately (FR-033).
	if _, err := ts.store.UpdateSMTPCredential(t.Context(), proj.ID, cred.ID, cred.Label, nil); err != nil {
		t.Fatalf("re-enable credential: %v", err)
	}
	if err := ts.store.DeleteProject(t.Context(), proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	go func() {
		c, m, e := sendAuthPlainRaw(ts.addr, cred.Username, "SuperSecretPassword12345")
		done <- struct {
			code int
			msg  string
			err  error
		}{c, m, e}
	}()
	waitForTimerCount(t, ts.clk, 1)
	ts.clk.Advance(time.Second)
	res = <-done
	if res.code != 535 || !strings.Contains(res.msg, "5.7.8 Authentication credentials invalid") {
		t.Fatalf("deleted project AUTH = (%d, %q, %v), want 535 5.7.8 Authentication credentials invalid", res.code, res.msg, res.err)
	}
}

// SPEC 12.4: Wrong password -> 535 5.7.8 after 1 s delay; 6th attempt within
// 15 min -> 454 4.7.0 even with the right password; smtp.auth_lockout audited
// once; after window passes, right password succeeds (fake clock).
func TestServerMode_BruteForceLockoutAndDelay(t *testing.T) {
	t.Parallel()

	ts := startServerModeSMTP(t, nil)
	proj, cred := seedProjectAndCred(t, ts, "shop", "staging", "shop-lock1234", "RightPassword12345678901", false)

	// 5 wrong-password attempts each wait 1 s on the fake clock and reply 535 5.7.8.
	for i := 1; i <= 5; i++ {
		resCh := make(chan struct {
			code int
			msg  string
			err  error
		}, 1)
		go func() {
			c, m, e := sendAuthPlainRaw(ts.addr, cred.Username, "WrongPassword00000000000")
			resCh <- struct {
				code int
				msg  string
				err  error
			}{c, m, e}
		}()

		waitForTimerCount(t, ts.clk, 1)
		// Verify reply has NOT completed before 1 s elapses.
		select {
		case r := <-resCh:
			t.Fatalf("attempt %d returned early without 1 s delay: (%d, %q)", i, r.code, r.msg)
		default:
		}

		ts.clk.Advance(time.Second)
		r := <-resCh
		if r.code != 535 || !strings.Contains(r.msg, "5.7.8 Authentication credentials invalid") {
			t.Fatalf("attempt %d = (%d, %q, %v), want 535 5.7.8 Authentication credentials invalid", i, r.code, r.msg, r.err)
		}
	}

	// 6th attempt within 15 min fails immediately with 454 4.7.0 even with the RIGHT password.
	code, msg, err := sendAuthPlainRaw(ts.addr, cred.Username, "RightPassword12345678901")
	if err != nil {
		t.Fatalf("6th attempt dial/read error: %v", err)
	}
	if code != 454 || !strings.Contains(msg, "4.7.0 Temporary authentication failure") {
		t.Fatalf("6th attempt = (%d, %q), want 454 4.7.0 Temporary authentication failure", code, msg)
	}

	// 7th attempt still locked and must not write a duplicate audit entry.
	code, msg, _ = sendAuthPlainRaw(ts.addr, cred.Username, "RightPassword12345678901")
	if code != 454 || !strings.Contains(msg, "4.7.0") {
		t.Fatalf("7th attempt = (%d, %q), want 454 4.7.0", code, msg)
	}

	var lockoutCount int
	var actorType string
	if err := ts.store.ReaderDB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(actor_type), '') FROM audit_log WHERE action = 'smtp.auth_lockout' AND project_id = ?`,
		proj.ID,
	).Scan(&lockoutCount, &actorType); err != nil {
		t.Fatalf("query smtp.auth_lockout audit: %v", err)
	}
	if lockoutCount != 1 || actorType != string(store.AuditActorSMTP) {
		t.Fatalf("smtp.auth_lockout audit = (count=%d, actor=%q), want (1, smtp)", lockoutCount, actorType)
	}

	// Advance past the 15-minute lockout window -> right password now succeeds.
	ts.clk.Advance(15*time.Minute + time.Second)
	code, msg, err = sendAuthPlainRaw(ts.addr, cred.Username, "RightPassword12345678901")
	if err != nil || code != 235 {
		t.Fatalf("post-window AUTH = (%d, %q, %v), want 235", code, msg, err)
	}
}

type countingCredStore struct {
	inner   *store.Store
	lookups atomic.Int32
}

func (c *countingCredStore) LookupSMTPCredentialByUsername(ctx context.Context, username string) (store.SMTPCredentialAuthLookup, error) {
	c.lookups.Add(1)
	return c.inner.LookupSMTPCredentialByUsername(ctx, username)
}

func (c *countingCredStore) TouchSMTPCredentialLastUsed(ctx context.Context, projectID, id int64, now time.Time) error {
	return c.inner.TouchSMTPCredentialLastUsed(ctx, projectID, id, now)
}

func (c *countingCredStore) InsertAudit(ctx context.Context, p store.InsertAuditParams) (store.AuditEntry, error) {
	return c.inner.InsertAudit(ctx, p)
}

// FR-004: Plaintext AUTH in server mode with TLS configured returns 523 5.7.10
// without performing any credential lookup; after STARTTLS, AUTH succeeds and
// stored messages record tls=true.
func TestServerMode_AuthRequiresTLSAndSTARTTLS(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return "server", true
		case "MAILCUBBY_DATA_DIR":
			return dataDir, true
		case "MAILCUBBY_BASE_URL":
			return "http://mail.example.test:8025", true
		case "MAILCUBBY_APP_KEY":
			return testServerAppKeyB64, true
		case "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH":
			return "true", true // temporarily pass Load without cert file paths; we set AllowInsecureAuth=false + runtime cert below
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.SMTPAllowInsecureAuth = false

	st := openTestStore(t)
	blobs, err := blob.NewStore(dataDir, cfg.ZstdLevel)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC))
	ing := ingest.New(st, blobs, clk, nil, nil)
	cipher, err := secrets.NewSMTPCredentialCipher(cfg.AppKey)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher: %v", err)
	}
	credStore := &countingCredStore{inner: st}

	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "secure", Name: "Secure", Color: "blue", Settings: store.DefaultProjectSettings(), Now: clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	sealed, err := cipher.Seal([]byte("TLSOnlyPassword123456789"), []byte("secure-tls12345"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	cred, err := st.CreateSMTPCredential(t.Context(), store.CreateSMTPCredentialParams{
		ProjectID: proj.ID, Label: "prod", Username: "secure-tls12345", PasswordEnc: sealed, Now: clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}

	srv := newServer(cfg, &storeProjectLookup{store: st, clk: clk}, credStore, cipher, ing, clk, nil)
	srv.SetTLSConfig(generateSelfSignedTLSConfig(t))

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if serveErr := <-done; serveErr != nil {
			t.Errorf("srv.Serve: %v", serveErr)
		}
	})

	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)

	if _, _, err := tp.ReadResponse(220); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	_ = tp.PrintfLine("EHLO plain.test")
	_, ehloPlain, err := tp.ReadResponse(250)
	if err != nil {
		t.Fatalf("EHLO plain: %v", err)
	}
	if !strings.Contains(ehloPlain, "STARTTLS") {
		t.Fatalf("expected STARTTLS in plaintext EHLO, got:\n%s", ehloPlain)
	}
	if strings.Contains(ehloPlain, "AUTH") {
		t.Fatalf("AUTH must not be advertised before STARTTLS, got:\n%s", ehloPlain)
	}

	// Plaintext AUTH must be rejected with 523 5.7.10 and ZERO credential lookups.
	b64 := base64.StdEncoding.EncodeToString([]byte("\x00" + cred.Username + "\x00TLSOnlyPassword123456789"))
	_ = tp.PrintfLine("AUTH PLAIN %s", b64)
	code, msg, err := tp.ReadResponse(523)
	if err != nil || code != 523 || !strings.Contains(msg, "5.7.10") {
		t.Fatalf("plaintext AUTH = (%d, %q, %v), want 523 5.7.10", code, msg, err)
	}
	if got := credStore.lookups.Load(); got != 0 {
		t.Fatalf("expected 0 credential lookups on plaintext AUTH, got %d", got)
	}

	// Upgrade to TLS with STARTTLS.
	_ = tp.PrintfLine("STARTTLS")
	if _, _, err := tp.ReadResponse(220); err != nil {
		t.Fatalf("STARTTLS 220: %v", err)
	}
	tlsConn := tls.Client(conn, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // runtime-generated self-signed test cert
	})
	if err := tlsConn.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	tpTLS := textproto.NewConn(tlsConn)

	_ = tpTLS.PrintfLine("EHLO tls.test")
	_, ehloTLS, err := tpTLS.ReadResponse(250)
	if err != nil {
		t.Fatalf("EHLO after STARTTLS: %v", err)
	}
	if !strings.Contains(ehloTLS, "AUTH PLAIN LOGIN") {
		t.Fatalf("expected AUTH PLAIN LOGIN after STARTTLS, got:\n%s", ehloTLS)
	}

	_ = tpTLS.PrintfLine("AUTH PLAIN %s", b64)
	if _, _, err := tpTLS.ReadResponse(235); err != nil {
		t.Fatalf("AUTH PLAIN after STARTTLS failed: %v", err)
	}
	_ = tpTLS.PrintfLine("MAIL FROM:<app@secure.test>")
	_, _, _ = tpTLS.ReadResponse(250)
	_ = tpTLS.PrintfLine("RCPT TO:<user@example.test>")
	_, _, _ = tpTLS.ReadResponse(250)
	_ = tpTLS.PrintfLine("DATA")
	_, _, _ = tpTLS.ReadResponse(354)
	dw := tpTLS.DotWriter()
	_, _ = dw.Write([]byte("Subject: Over TLS\r\nFrom: app@secure.test\r\n\r\nEncrypted\r\n"))
	_ = dw.Close()
	if _, _, err := tpTLS.ReadResponse(250); err != nil {
		t.Fatalf("DATA after STARTTLS: %v", err)
	}
	_ = tpTLS.PrintfLine("QUIT")

	msgs, err := st.ListMessages(t.Context(), proj.ID, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = (%d, %v), want 1", len(msgs), err)
	}
	if !msgs[0].TLS {
		t.Fatalf("expected stored message tls=true")
	}
	if msgs[0].SMTPCredentialID == nil || *msgs[0].SMTPCredentialID != cred.ID {
		t.Fatalf("expected smtp_credential_id=%d, got %v", cred.ID, msgs[0].SMTPCredentialID)
	}
}

// FR-011, FR-020, SPEC 7.2: Server-mode unauthenticated recipient routing
// (enabled vs disabled projects, multi-project delivery, deduplication, and
// 530 5.7.0 when INBOX_DOMAIN is empty) plus local-mode multi-project delivery.
func TestServerAndLocalMode_RecipientRoutingAndMultiProjectDelivery(t *testing.T) {
	t.Parallel()

	t.Run("server mode without inbox domain rejects unauthenticated MAIL FROM with 530 5.7.0", func(t *testing.T) {
		t.Parallel()
		ts := startServerModeSMTP(t, func(c *config.Config) { c.InboxDomain = "" })

		var d net.Dialer
		conn, err := d.DialContext(t.Context(), "tcp", ts.addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)
		_, _, _ = tp.ReadResponse(220)
		_ = tp.PrintfLine("EHLO client.test")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("MAIL FROM:<a@b.test>")
		code, msg, err := tp.ReadResponse(530)
		if err != nil || code != 530 || !strings.Contains(msg, "5.7.0 Authentication required") {
			t.Fatalf("MAIL FROM without auth/inbox_domain = (%d, %q, %v), want 530 5.7.0 Authentication required", code, msg, err)
		}
		_ = tp.PrintfLine("QUIT")
	})

	t.Run("server mode recipient routing multi-project delivery and 550 rejections", func(t *testing.T) {
		t.Parallel()
		ts := startServerModeSMTP(t, func(c *config.Config) { c.InboxDomain = "inbox.test" })

		projA, _ := seedProjectAndCred(t, ts, "alpha", "app", "alpha-11111111", "Pass11111111111111111111", true)
		projB, _ := seedProjectAndCred(t, ts, "beta", "app", "beta-22222222", "Pass22222222222222222222", true)
		projClosed, _ := seedProjectAndCred(t, ts, "closed", "app", "closed-33333333", "Pass33333333333333333333", false)

		var d net.Dialer
		conn, err := d.DialContext(t.Context(), "tcp", ts.addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)
		_, _, _ = tp.ReadResponse(220)
		_ = tp.PrintfLine("EHLO router.test")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("MAIL FROM:<sender@external.test>")
		if _, _, err := tp.ReadResponse(250); err != nil {
			t.Fatalf("MAIL FROM with InboxDomain failed: %v", err)
		}

		// Two RCPTs for alpha (subdomain + local-part -> deduplicated into 1 message with both plus tags).
		_ = tp.PrintfLine("RCPT TO:<orders+t1@alpha.inbox.test>")
		if _, _, err := tp.ReadResponse(250); err != nil {
			t.Fatalf("RCPT alpha subdomain: %v", err)
		}
		_ = tp.PrintfLine("RCPT TO:<alpha+t2@inbox.test>")
		if _, _, err := tp.ReadResponse(250); err != nil {
			t.Fatalf("RCPT alpha local-part: %v", err)
		}
		// One RCPT for beta.
		_ = tp.PrintfLine("RCPT TO:<beta+btag@inbox.test>")
		if _, _, err := tp.ReadResponse(250); err != nil {
			t.Fatalf("RCPT beta: %v", err)
		}
		// Closed project -> 550 5.1.1.
		_ = tp.PrintfLine("RCPT TO:<closed@inbox.test>")
		if code, msg, err := tp.ReadResponse(550); err != nil || code != 550 || !strings.Contains(msg, "5.1.1 Mailbox unavailable") {
			t.Fatalf("RCPT closed = (%d, %q, %v), want 550 5.1.1 Mailbox unavailable", code, msg, err)
		}
		// Unrouted domain -> 550 5.1.1.
		_ = tp.PrintfLine("RCPT TO:<nobody@other.test>")
		if code, msg, err := tp.ReadResponse(550); err != nil || code != 550 || !strings.Contains(msg, "5.1.1 Mailbox unavailable") {
			t.Fatalf("RCPT unrouted = (%d, %q, %v), want 550 5.1.1 Mailbox unavailable", code, msg, err)
		}

		_ = tp.PrintfLine("DATA")
		_, _, _ = tp.ReadResponse(354)
		dw := tp.DotWriter()
		_, _ = dw.Write([]byte("Subject: Fanout\r\nFrom: sender@external.test\r\n\r\nMulti-project body\r\n"))
		_ = dw.Close()
		if _, _, err := tp.ReadResponse(250); err != nil {
			t.Fatalf("DATA fanout: %v", err)
		}
		_ = tp.PrintfLine("QUIT")

		// Verify 1 copy in alpha (with tags t1, t2), 1 copy in beta (with tag btag), 0 in closed, distinct blobs.
		msgsA, _ := ts.store.ListMessages(t.Context(), projA.ID, 10)
		msgsB, _ := ts.store.ListMessages(t.Context(), projB.ID, 10)
		msgsClosed, _ := ts.store.ListMessages(t.Context(), projClosed.ID, 10)
		if len(msgsA) != 1 || len(msgsB) != 1 || len(msgsClosed) != 0 {
			t.Fatalf("counts = (alpha=%d, beta=%d, closed=%d), want (1, 1, 0)", len(msgsA), len(msgsB), len(msgsClosed))
		}
		if msgsA[0].ID == msgsB[0].ID || msgsA[0].BlobPath == msgsB[0].BlobPath {
			t.Fatalf("multi-project copies must have distinct ULIDs and blob paths")
		}
		tagsA, _ := ts.store.ListTagsByMessagePK(t.Context(), projA.ID, msgsA[0].PK)
		tagsB, _ := ts.store.ListTagsByMessagePK(t.Context(), projB.ID, msgsB[0].PK)
		if len(tagsA) != 2 || len(tagsB) != 1 || tagsB[0] != "btag" {
			t.Fatalf("tags = (alpha=%v, beta=%v), want 2 tags on alpha and [btag] on beta", tagsA, tagsB)
		}
	})

	t.Run("local mode multi-project delivery routes to each routed project plus default", func(t *testing.T) {
		t.Parallel()
		st := openTestStore(t)
		cfg := defaultLocalConfig(t)
		cfg.InboxDomain = "inbox.test"
		blobs, err := blob.NewStore(cfg.DataDir, cfg.ZstdLevel)
		if err != nil {
			t.Fatalf("blob.NewStore: %v", err)
		}
		ing := ingest.New(st, blobs, clock.NewFake(time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)), nil, nil)
		addr := startTestSMTP(t, cfg, st, ing)

		var d net.Dialer
		conn, err := d.DialContext(t.Context(), "tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)
		_, _, _ = tp.ReadResponse(220)
		_ = tp.PrintfLine("EHLO local.test")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("MAIL FROM:<dev@local.test>")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("RCPT TO:<a@shop.inbox.test>")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("RCPT TO:<blog@inbox.test>")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("RCPT TO:<someone@external.test>")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("DATA")
		_, _, _ = tp.ReadResponse(354)
		dw := tp.DotWriter()
		_, _ = dw.Write([]byte("Subject: Local Multi\r\n\r\nHello all\r\n"))
		_ = dw.Close()
		if _, _, err := tp.ReadResponse(250); err != nil {
			t.Fatalf("DATA: %v", err)
		}
		_ = tp.PrintfLine("QUIT")

		for _, slug := range []string{"shop", "blog", "default"} {
			p, err := st.GetProjectBySlug(t.Context(), slug)
			if err != nil {
				t.Fatalf("expected project %q to exist: %v", slug, err)
			}
			msgs, _ := st.ListMessages(t.Context(), p.ID, 10)
			if len(msgs) != 1 {
				t.Fatalf("project %q message count = %d, want 1", slug, len(msgs))
			}
		}
	})
}

package smtpd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

type stubIngestor struct {
	err error
}

func (s stubIngestor) Ingest(_ context.Context, _ ingest.Envelope, _ []ingest.Target, r io.Reader) ([]ingest.MessageRef, error) {
	_, err := io.Copy(io.Discard, r)
	if err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

func startTestSMTP(t *testing.T, cfg config.Config, st *store.Store, ing Ingestor) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := New(cfg, st, ing, clock.NewFake(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)), nil)
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if serveErr := <-done; serveErr != nil {
			t.Errorf("srv.Serve returned error: %v", serveErr)
		}
	})
	return ln.Addr().String()
}

func defaultLocalConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return t.TempDir(), true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestSession_BareSMTPErrors(t *testing.T) {
	t.Parallel()

	// ARCHITECTURE 3.3 / SPEC 7.4: every Session error path returns a bare
	// *smtp.SMTPError (type assertion succeeds directly, never wrapped, never 554 5.0.0).
	assertBareSMTP := func(t *testing.T, name string, err error, wantCode int, wantEnh smtp.EnhancedCode) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
		//nolint:errorlint // intentionally asserting bare *smtp.SMTPError because go-smtp uses a direct type assertion
		se, ok := err.(*smtp.SMTPError)
		if !ok {
			t.Fatalf("%s: error %T (%v) is not a bare *smtp.SMTPError", name, err, err)
		}
		if se.Code != wantCode || se.EnhancedCode != wantEnh {
			t.Fatalf("%s: got %d %v (%q), want %d %v", name, se.Code, se.EnhancedCode, se.Message, wantCode, wantEnh)
		}
	}

	cfg := defaultLocalConfig(t)
	lookup := newFakeLookup()

	t.Run("AUTH unknown mechanism returns 504 5.7.4", func(t *testing.T) {
		t.Parallel()
		s := &session{be: &backend{cfg: cfg, lookup: lookup, ingest: stubIngestor{}}}
		_, err := s.Auth("CRAM-MD5")
		assertBareSMTP(t, "Auth(CRAM-MD5)", err, 504, smtp.EnhancedCode{5, 7, 4})
	})

	t.Run("Server AUTH failure returns 535 5.7.8", func(t *testing.T) {
		t.Parallel()
		scfg := cfg
		scfg.Mode = config.ModeServer
		s := &session{be: &backend{cfg: scfg, lookup: lookup, ingest: stubIngestor{}}}
		err := s.authenticate("user", "bad")
		assertBareSMTP(t, "authenticate(server)", err, 535, smtp.EnhancedCode{5, 7, 8})
	})

	t.Run("MAIL FROM oversized SIZE returns 552 5.3.4", func(t *testing.T) {
		t.Parallel()
		s := &session{be: &backend{cfg: cfg, lookup: lookup, ingest: stubIngestor{}}}
		err := s.Mail("a@b.test", &smtp.MailOptions{Size: cfg.SMTPMaxMessageBytes + 1})
		assertBareSMTP(t, "Mail(oversize)", err, 552, smtp.EnhancedCode{5, 3, 4})
	})

	t.Run("Server MAIL FROM without AUTH or inbox domain returns 530 5.7.0", func(t *testing.T) {
		t.Parallel()
		scfg := cfg
		scfg.Mode = config.ModeServer
		scfg.InboxDomain = ""
		s := &session{be: &backend{cfg: scfg, lookup: lookup, ingest: stubIngestor{}}}
		err := s.Mail("a@b.test", nil)
		assertBareSMTP(t, "Mail(server no auth)", err, 530, smtp.EnhancedCode{5, 7, 0})
	})

	t.Run("Server RCPT unroutable returns 550 5.1.1", func(t *testing.T) {
		t.Parallel()
		scfg := cfg
		scfg.Mode = config.ModeServer
		scfg.InboxDomain = "inbox.test"
		s := &session{be: &backend{cfg: scfg, lookup: lookup, ingest: stubIngestor{}}}
		err := s.Rcpt("nobody@other.test", nil)
		assertBareSMTP(t, "Rcpt(unroutable)", err, 550, smtp.EnhancedCode{5, 1, 1})
	})

	t.Run("RCPT lookup error returns 451 4.3.0", func(t *testing.T) {
		t.Parallel()
		fl := newFakeLookup()
		fl.err = errors.New("db offline")
		s := &session{be: &backend{cfg: cfg, lookup: fl, ingest: stubIngestor{}, log: New(cfg, nil, stubIngestor{}, nil, nil).log}}
		err := s.Rcpt("a@b.test", nil)
		assertBareSMTP(t, "Rcpt(db error)", err, 451, smtp.EnhancedCode{4, 3, 0})
	})

	t.Run("DATA with no valid recipients returns 554 5.5.1", func(t *testing.T) {
		t.Parallel()
		s := &session{be: &backend{cfg: cfg, lookup: lookup, ingest: stubIngestor{}}}
		err := s.Data(strings.NewReader("Subject: x\r\n\r\nhi\r\n"))
		assertBareSMTP(t, "Data(no rcpts)", err, 554, smtp.EnhancedCode{5, 5, 1})
	})

	t.Run("DATA maps internal, quota, rate limit, and too-large errors", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			ingErr  error
			code    int
			enhCode smtp.EnhancedCode
		}{
			{"ErrDataTooLarge", fmt.Errorf("wrapped: %w", smtp.ErrDataTooLarge), 552, smtp.EnhancedCode{5, 3, 4}},
			{"ErrQuotaExceeded", fmt.Errorf("wrapped: %w", ErrQuotaExceeded), 452, smtp.EnhancedCode{4, 3, 1}},
			{"ErrRateLimited", fmt.Errorf("wrapped: %w", ErrRateLimited), 450, smtp.EnhancedCode{4, 7, 1}},
			{"DBFailure", errors.New("sqlite disk full"), 451, smtp.EnhancedCode{4, 3, 0}},
		}
		for _, tc := range cases {
			s := &session{
				be:      &backend{cfg: cfg, lookup: lookup, ingest: stubIngestor{err: tc.ingErr}, log: New(cfg, nil, stubIngestor{}, nil, nil).log},
				targets: []ingest.Target{{ProjectID: 1, ProjectSlug: "default"}},
			}
			err := s.Data(strings.NewReader("Subject: x\r\n\r\nhi\r\n"))
			assertBareSMTP(t, tc.name, err, tc.code, tc.enhCode)
		}
	})
}

func TestSMTPProtocol_EHLO_AuthCramMD5_Limits_And_DBFailure(t *testing.T) {
	t.Parallel()

	// FR-001, FR-003, FR-005: EHLO capabilities, AUTH CRAM-MD5 -> 504,
	// 101st RCPT -> 452 4.5.3, 26 MiB -> 552 5.3.4.
	st := testutil.NewStore(t)
	cfg := defaultLocalConfig(t)
	blobs, err := blob.NewStore(cfg.DataDir, cfg.ZstdLevel)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	ing := ingest.New(st, blobs, clock.NewFake(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)), nil, nil)
	addr := startTestSMTP(t, cfg, st, ing)

	t.Run("EHLO advertises SIZE 26214400 and AUTH PLAIN LOGIN and CRAM-MD5 returns 504", func(t *testing.T) {
		var d net.Dialer
		conn, err := d.DialContext(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)

		// Greeting
		if _, _, err := tp.ReadResponse(220); err != nil {
			t.Fatalf("greeting: %v", err)
		}
		if err := tp.PrintfLine("EHLO client.test"); err != nil {
			t.Fatalf("EHLO: %v", err)
		}
		_, ehloMsg, err := tp.ReadResponse(250)
		if err != nil {
			t.Fatalf("EHLO response: %v", err)
		}
		for _, want := range []string{"SIZE 26214400", "SMTPUTF8", "AUTH PLAIN LOGIN", "PIPELINING", "8BITMIME", "ENHANCEDSTATUSCODES", "CHUNKING"} {
			if !strings.Contains(ehloMsg, want) {
				t.Errorf("EHLO missing %q; got:\n%s", want, ehloMsg)
			}
		}

		// FR-003: AUTH CRAM-MD5 returns 504 5.7.4.
		if err := tp.PrintfLine("AUTH CRAM-MD5"); err != nil {
			t.Fatalf("AUTH CRAM-MD5: %v", err)
		}
		code, msg, err := tp.ReadResponse(504)
		if err != nil {
			t.Fatalf("expected 504 for AUTH CRAM-MD5, got code=%d msg=%q err=%v", code, msg, err)
		}
		if !strings.Contains(msg, "5.7.4") {
			t.Fatalf("expected 5.7.4 in 504 reply, got %q", msg)
		}

		// FR-003: AUTH LOGIN with initial response works.
		userB64 := base64.StdEncoding.EncodeToString([]byte("Shop Staging"))
		passB64 := base64.StdEncoding.EncodeToString([]byte("any-pass"))
		if err := tp.PrintfLine("AUTH LOGIN %s", userB64); err != nil {
			t.Fatalf("AUTH LOGIN IR: %v", err)
		}
		_, chal, err := tp.ReadResponse(334)
		if err != nil {
			t.Fatalf("expected 334 Password challenge, got %v", err)
		}
		decodedChal, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(chal))
		if string(decodedChal) != "Password:" {
			t.Fatalf("challenge = %q, want Password:", string(decodedChal))
		}
		if err := tp.PrintfLine("%s", passB64); err != nil {
			t.Fatalf("password send: %v", err)
		}
		if _, _, err := tp.ReadResponse(235); err != nil {
			t.Fatalf("expected 235 after AUTH LOGIN IR, got %v", err)
		}
		_ = tp.PrintfLine("QUIT")
	})

	t.Run("101st RCPT returns 452 4.5.3", func(t *testing.T) {
		var d net.Dialer
		conn, err := d.DialContext(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)
		_, _, _ = tp.ReadResponse(220)
		_ = tp.PrintfLine("EHLO rcpt.test")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("MAIL FROM:<sender@example.test>")
		_, _, _ = tp.ReadResponse(250)

		for i := range 100 {
			_ = tp.PrintfLine("RCPT TO:<user%d@example.test>", i)
			if _, _, err := tp.ReadResponse(250); err != nil {
				t.Fatalf("RCPT %d failed unexpectedly: %v", i+1, err)
			}
		}
		_ = tp.PrintfLine("RCPT TO:<user101@example.test>")
		code, msg, err := tp.ReadResponse(452)
		if err != nil {
			t.Fatalf("101st RCPT: got code=%d msg=%q err=%v, want 452", code, msg, err)
		}
		if !strings.Contains(msg, "4.5.3") {
			t.Fatalf("101st RCPT: want 4.5.3, got %q", msg)
		}
		_ = tp.PrintfLine("QUIT")
	})

	t.Run("26 MiB message returns 552 5.3.4", func(t *testing.T) {
		var d net.Dialer
		conn, err := d.DialContext(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)
		_, _, _ = tp.ReadResponse(220)
		_ = tp.PrintfLine("EHLO size.test")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("MAIL FROM:<sender@example.test>")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("RCPT TO:<rcpt@example.test>")
		_, _, _ = tp.ReadResponse(250)
		_ = tp.PrintfLine("DATA")
		if _, _, err := tp.ReadResponse(354); err != nil {
			t.Fatalf("DATA 354: %v", err)
		}

		// Stream 26 MiB of 80-byte lines so MaxLineLength (2000) is not hit.
		bw := bufio.NewWriterSize(conn, 64*1024)
		_, _ = bw.WriteString("Subject: Huge\r\n\r\n")
		line := bytes.Repeat([]byte("A"), 78)
		line = append(line, '\r', '\n')
		total := 26 * 1024 * 1024
		for written := 0; written < total; written += len(line) {
			if _, werr := bw.Write(line); werr != nil {
				break
			}
		}
		_, _ = bw.WriteString(".\r\n")
		_ = bw.Flush()

		code, msg, err := tp.ReadResponse(552)
		if err != nil {
			t.Fatalf("26 MiB DATA: got code=%d msg=%q err=%v, want 552", code, msg, err)
		}
		if !strings.Contains(msg, "5.3.4") {
			t.Fatalf("26 MiB DATA: want 5.3.4, got %q", msg)
		}
		_ = tp.PrintfLine("QUIT")
	})
}

type failingTxStore struct {
	err error
}

func (f failingTxStore) WithTx(context.Context, func(tx *store.Tx) error) error {
	return f.err
}

func TestSMTP_DBFailureAtomicRollback(t *testing.T) {
	t.Parallel()

	// FR-010: injected DB failure returns 451 4.3.0 and leaves no row and no blob.
	st := testutil.NewStore(t)
	cfg := defaultLocalConfig(t)
	blobs, err := blob.NewStore(cfg.DataDir, cfg.ZstdLevel)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	// Ingestor uses a failing TxStore while SMTP routing uses real store so project exists.
	ing := ingest.New(failingTxStore{err: errors.New("injected sqlite failure")}, blobs, clock.NewFake(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)), nil, nil)
	addr := startTestSMTP(t, cfg, st, ing)

	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)
	_, _, _ = tp.ReadResponse(220)
	_ = tp.PrintfLine("EHLO fail.test")
	_, _, _ = tp.ReadResponse(250)
	_ = tp.PrintfLine("MAIL FROM:<sender@example.test>")
	_, _, _ = tp.ReadResponse(250)
	_ = tp.PrintfLine("RCPT TO:<rcpt@example.test>")
	_, _, _ = tp.ReadResponse(250)
	_ = tp.PrintfLine("DATA")
	_, _, _ = tp.ReadResponse(354)
	dw := tp.DotWriter()
	_, _ = dw.Write([]byte("Subject: Fail\r\n\r\nBody\r\n"))
	_ = dw.Close()

	code, msg, err := tp.ReadResponse(451)
	if err != nil {
		t.Fatalf("DB failure DATA: got code=%d msg=%q err=%v, want 451", code, msg, err)
	}
	if !strings.Contains(msg, "4.3.0") {
		t.Fatalf("DB failure DATA: want 4.3.0, got %q", msg)
	}

	// Verify 0 messages in DB and 0 blobs on disk.
	proj, err := st.GetProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	msgs, err := st.ListMessages(context.Background(), proj.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 rows after DB failure, got %d", len(msgs))
	}
	var blobCount int
	if err := blobs.Walk(context.Background(), func(blob.Entry) error {
		blobCount++
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if blobCount != 0 {
		t.Fatalf("expected 0 blobs after DB failure, got %d", blobCount)
	}
}

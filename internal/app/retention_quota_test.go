package app_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// FR-096: instance storage quota rejects SMTP DATA with 452 4.3.1 when exceeded,
// and accepts mail again after retention frees space.
func TestInstanceQuota452AndRecoveryAfterSweep(t *testing.T) {
	t.Parallel()

	rawMsg := "From: alice@example.test\r\nTo: bob@example.test\r\nSubject: Quota\r\n\r\n" +
		strings.Repeat("Q", 180) + "\r\n"
	msgLen := int64(len(rawMsg))

	srv := testutil.StartServer(t, func(cfg *config.Config) {
		// Allow 2 copies of rawMsg, reject the 3rd until retention sweeps down to 1.
		cfg.InstanceMaxBytes = msgLen*2 + 20
		cfg.RetentionMaxAge = 0
		cfg.RetentionMaxBytes = 0
		cfg.RetentionMaxMessages = 1
		cfg.RetentionInterval = time.Hour
		cfg.SMTPRateLimit = config.RateLimit{Count: 0, Window: time.Minute}
	})

	// Messages 1 and 2 fit within InstanceMaxBytes.
	for i := range 2 {
		srv.Clock.Advance(time.Second)
		if err := testutil.TrySendSMTP(srv.SMTPAddr, nil, "alice@example.test", []string{"bob@example.test"}, []byte(rawMsg)); err != nil {
			t.Fatalf("message %d failed unexpectedly: %v", i+1, err)
		}
	}

	// Message 3 exceeds InstanceMaxBytes -> 452 4.3.1 Insufficient system storage.
	srv.Clock.Advance(time.Second)
	err := testutil.TrySendSMTP(srv.SMTPAddr, nil, "alice@example.test", []string{"bob@example.test"}, []byte(rawMsg))
	if err == nil {
		t.Fatal("expected 452 4.3.1 error when instance quota exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "452") || !strings.Contains(err.Error(), "4.3.1") {
		t.Fatalf("expected 452 4.3.1 reply, got: %v", err)
	}

	// Run one retention sweep (max_messages=1 frees 1 message).
	stats, sweepErr := srv.App.Sweeper().SweepAll(context.Background())
	if sweepErr != nil {
		t.Fatalf("SweepAll: %v", sweepErr)
	}
	if stats.DeletedMessages != 1 {
		t.Fatalf("SweepAll deleted %d messages, want 1", stats.DeletedMessages)
	}

	// After space is freed, message 3 is accepted again.
	srv.Clock.Advance(time.Second)
	if err := testutil.TrySendSMTP(srv.SMTPAddr, nil, "alice@example.test", []string{"bob@example.test"}, []byte(rawMsg)); err != nil {
		t.Fatalf("expected message accepted after sweep freed space, got: %v", err)
	}
}

// FR-097: 601st message within a minute for one project fails with 450 4.7.1
// (with default 600/m rate and fake clock), while another project stays unaffected,
// and advancing the clock past the 1 m window restores delivery.
func TestPerProjectSMTPRateLimit601stMessage450(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, func(cfg *config.Config) {
		// Keep default SMTPRateLimit (600/m); disable retention to avoid mid-test sweeps.
		cfg.RetentionMaxAge = 0
		cfg.RetentionMaxMessages = 0
		cfg.RetentionMaxBytes = 0
		cfg.RetentionInterval = time.Hour
	})

	// Reuse a single SMTP connection so sending 600 messages takes < 1 s.
	client, err := smtp.Dial(srv.SMTPAddr)
	if err != nil {
		t.Fatalf("smtp.Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	raw := []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: Rate\r\n\r\nHi\r\n")
	sendOnConn := func(c *smtp.Client, rcpt string) error {
		if err := c.Mail("a@example.test"); err != nil {
			return fmt.Errorf("MAIL FROM: %w", err)
		}
		if err := c.Rcpt(rcpt); err != nil {
			_ = c.Reset()
			return fmt.Errorf("RCPT TO: %w", err)
		}
		wc, err := c.Data()
		if err != nil {
			_ = c.Reset()
			return fmt.Errorf("DATA: %w", err)
		}
		if _, err := wc.Write(raw); err != nil {
			_ = wc.Close()
			return fmt.Errorf("DATA write: %w", err)
		}
		if err := wc.Close(); err != nil {
			return fmt.Errorf("DATA close: %w", err)
		}
		return nil
	}

	// Send 600 messages within 1 minute -> all accepted.
	for i := range 600 {
		if err := sendOnConn(client, "b@example.test"); err != nil {
			t.Fatalf("message %d failed: %v", i+1, err)
		}
	}

	// 601st message within the same minute -> 450 4.7.1.
	err = sendOnConn(client, "b@example.test")
	if err == nil {
		t.Fatal("expected 601st message to fail with 450 4.7.1, got nil")
	}
	if !strings.Contains(err.Error(), "450") || !strings.Contains(err.Error(), "4.7.1") {
		t.Fatalf("expected 450 4.7.1 on 601st message, got: %v", err)
	}

	// Another project (authenticated as "other") is NOT affected by default project's limit.
	if err := testutil.TrySendSMTP(srv.SMTPAddr, testutil.PlainAuth("other", "pw"), "a@example.test", []string{"b@example.test"}, raw); err != nil {
		t.Fatalf("expected other project to remain unaffected, got: %v", err)
	}

	// Advance fake clock past the 1 m window -> default project accepts again.
	srv.Clock.Advance(time.Minute + time.Second)
	if err := sendOnConn(client, "b@example.test"); err != nil {
		t.Fatalf("expected message accepted after 1 m window, got: %v", err)
	}
}

// FR-097: in server mode with AUTH, excess messages are rejected at MAIL FROM with 450 4.7.1.
func TestServerModeAuthRateLimitAtMailFrom(t *testing.T) {
	t.Parallel()

	key := make([]byte, secrets.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	srv := testutil.StartServer(t, func(cfg *config.Config) {
		cfg.Mode = config.ModeServer
		cfg.BaseURL = "http://localhost:8025"
		cfg.AppKey = key
		cfg.SMTPAllowInsecureAuth = true
		cfg.AdminEmail = "admin@example.test"
		cfg.AdminPassword = "correct-horse-battery-staple"
		cfg.SMTPRateLimit = config.RateLimit{Count: 2, Window: time.Minute}
		cfg.RetentionInterval = time.Hour
	})

	proj, err := srv.Store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "billing",
		Name:     "Billing",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      srv.Clock.Now(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	cipher, err := secrets.NewSMTPCredentialCipher(key)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher: %v", err)
	}
	const username = "billing-12345678"
	const password = "SuperSecretSMTPPassword1"
	enc, err := cipher.Seal([]byte(password), []byte(username))
	if err != nil {
		t.Fatalf("cipher.Seal: %v", err)
	}
	if _, err := srv.Store.CreateSMTPCredential(t.Context(), store.CreateSMTPCredentialParams{
		ProjectID:   proj.ID,
		Label:       "app",
		Username:    username,
		PasswordEnc: enc,
		Now:         srv.Clock.Now(),
	}); err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}

	raw := []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: Auth Rate\r\n\r\nHi\r\n")
	for i := range 2 {
		if err := testutil.TrySendSMTP(srv.SMTPAddr, testutil.PlainAuth(username, password), "a@example.test", []string{"b@example.test"}, raw); err != nil {
			t.Fatalf("auth message %d failed: %v", i+1, err)
		}
	}

	// 3rd attempt: MAIL FROM itself rejects with 450 4.7.1 after AUTH.
	client, err := smtp.Dial(srv.SMTPAddr)
	if err != nil {
		t.Fatalf("smtp.Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Auth(testutil.PlainAuth(username, password)); err != nil {
		t.Fatalf("AUTH: %v", err)
	}
	err = client.Mail("a@example.test")
	if err == nil {
		t.Fatal("expected MAIL FROM to fail with 450 4.7.1, got nil")
	}
	if !strings.Contains(err.Error(), "450") || !strings.Contains(err.Error(), "4.7.1") {
		t.Fatalf("expected 450 4.7.1 at MAIL FROM, got: %v", err)
	}

	// Advance clock past window -> succeeds.
	srv.Clock.Advance(time.Minute + time.Second)
	if err := testutil.TrySendSMTP(srv.SMTPAddr, testutil.PlainAuth(username, password), "a@example.test", []string{"b@example.test"}, raw); err != nil {
		t.Fatalf("expected auth message accepted after window, got: %v", err)
	}
}

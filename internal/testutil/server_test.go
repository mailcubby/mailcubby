package testutil_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestSMTPIntegration_LocalRoutingAuthAndRawBytes(t *testing.T) {
	t.Parallel()

	// Phase 2 acceptance:
	// - net/smtp with no AUTH -> stored in "default"
	// - AUTH user "Shop Staging" (PLAIN and LOGIN) -> project "shop-staging" (name "Shop Staging") auto-created
	// - .eml bytes read back from blob equal sent bytes (SHA-256) (FR-006)
	// - Recipient routing with MAILCUBBY_INBOX_DOMAIN in local mode (FR-020, FR-011)
	// - Realtime message.created published (FR-012)
	srv := testutil.StartServer(t, func(cfg *config.Config) {
		cfg.InboxDomain = "inbox.test"
	})

	sub, unsub := srv.Bus.Subscribe(events.AllFilter())
	defer unsub()

	ctx := context.Background()

	// 1. No AUTH -> stored in "default" with exact SHA-256 match.
	msgDefault := []byte("From: Dev <dev@example.test>\r\n" +
		"To: QA <qa@example.test>\r\n" +
		"Subject: Default inbox message\r\n" +
		"\r\n" +
		"Exact body bytes.\r\n")
	wantHash := sha256.Sum256(msgDefault)
	wantHashHex := hex.EncodeToString(wantHash[:])

	testutil.SendSMTP(t, srv.SMTPAddr, "dev@example.test", []string{"qa@example.test"}, msgDefault)

	defProj, err := srv.Store.GetProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatalf("GetProjectBySlug(default): %v", err)
	}
	msgs, err := srv.Store.ListMessages(ctx, defProj.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages(default): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Subject != "Default inbox message" {
		t.Fatalf("unexpected default messages: %+v", msgs)
	}

	rc, err := srv.Blobs.Open(msgs[0].BlobPath)
	if err != nil {
		t.Fatalf("Blobs.Open: %v", err)
	}
	gotBytes, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll blob: %v", err)
	}
	gotHash := sha256.Sum256(gotBytes)
	if hex.EncodeToString(gotHash[:]) != wantHashHex || !bytes.Equal(gotBytes, msgDefault) {
		t.Fatalf("blob bytes mismatch: got sha256=%x want=%s", gotHash, wantHashHex)
	}

	// Verify message.created event arrived.
	select {
	case ev := <-sub.Events():
		if ev.Type != events.TypeMessageCreated || ev.ProjectID != defProj.ID {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message.created event")
	}

	// 2. AUTH PLAIN with user "Shop Staging" -> auto-creates "shop-staging" (Name "Shop Staging").
	msgPlain := []byte("From: Shop <shop@example.test>\r\nTo: Client <c@example.test>\r\nSubject: Via PLAIN\r\n\r\nHello PLAIN\r\n")
	testutil.SendSMTPPlain(t, srv.SMTPAddr, "Shop Staging", "any", "shop@example.test", []string{"c@example.test"}, msgPlain)

	// 3. AUTH LOGIN with user "Shop Staging" -> appends second message into same "shop-staging" project.
	msgLogin := []byte("From: Shop <shop@example.test>\r\nTo: Client <c@example.test>\r\nSubject: Via LOGIN\r\n\r\nHello LOGIN\r\n")
	testutil.SendSMTPLogin(t, srv.SMTPAddr, "Shop Staging", "any", "shop@example.test", []string{"c@example.test"}, msgLogin)

	shopProj, err := srv.Store.GetProjectBySlug(ctx, "shop-staging")
	if err != nil {
		t.Fatalf("GetProjectBySlug(shop-staging): %v", err)
	}
	if shopProj.Name != "Shop Staging" {
		t.Fatalf("shop-staging Name = %q, want %q", shopProj.Name, "Shop Staging")
	}
	if shopProj.MessageCount != 2 || shopProj.UnreadCount != 2 {
		t.Fatalf("shop-staging counters: count=%d unread=%d, want 2/2", shopProj.MessageCount, shopProj.UnreadCount)
	}

	// 4. Multi-project recipient routing without AUTH (FR-011, FR-020).
	msgRouted := []byte("From: App <app@example.test>\r\nTo: Multi <orders+urgent@acme.inbox.test>\r\nSubject: Routed\r\n\r\nRouted body\r\n")
	testutil.SendSMTP(t, srv.SMTPAddr, "app@example.test", []string{
		"orders+urgent@acme.inbox.test",
		"billing+paid@inbox.test",
		"outside@other.test",
	}, msgRouted)

	for _, slug := range []string{"acme", "billing"} {
		p, err := srv.Store.GetProjectBySlug(ctx, slug)
		if err != nil {
			t.Fatalf("routed project %q not created: %v", slug, err)
		}
		if p.MessageCount != 1 {
			t.Fatalf("routed project %q MessageCount = %d, want 1", slug, p.MessageCount)
		}
	}
	// Non-routed recipient in same message also delivered to default project.
	defProjAfter, err := srv.Store.GetProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatalf("GetProjectBySlug(default): %v", err)
	}
	if defProjAfter.MessageCount != 2 {
		t.Fatalf("default project MessageCount = %d, want 2", defProjAfter.MessageCount)
	}
}

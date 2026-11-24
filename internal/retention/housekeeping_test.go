package retention_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/retention"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// FR-099: daily housekeeping removes expired/idle sessions, expired invites,
// expired/used password resets, and audit entries older than MAILCUBBY_AUDIT_RETENTION.
func TestHousekeeping(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	cfg := defaultTestConfig(t)
	cfg.SessionIdleTimeout = 7 * 24 * time.Hour
	cfg.AuditRetention = 180 * 24 * time.Hour

	user, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email: "admin@example.test", Name: "Admin", IsAdmin: true, CanCreateProjects: true, Now: start,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Sessions: 1 expired by absolute TTL, 1 expired by idle timeout, 1 active.
	if _, err := st.CreateSession(t.Context(), store.CreateSessionParams{
		ID: "sess-expired", UserID: user.ID, CSRFToken: "c1",
		Now: start.Add(-31 * 24 * time.Hour), ExpiresAt: start.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession(expired): %v", err)
	}
	if _, err := st.CreateSession(t.Context(), store.CreateSessionParams{
		ID: "sess-idle", UserID: user.ID, CSRFToken: "c2",
		Now: start.Add(-8 * 24 * time.Hour), ExpiresAt: start.Add(20 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession(idle): %v", err)
	}
	if _, err := st.CreateSession(t.Context(), store.CreateSessionParams{
		ID: "sess-active", UserID: user.ID, CSRFToken: "c3",
		Now: start.Add(-time.Hour), ExpiresAt: start.Add(29 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession(active): %v", err)
	}

	// Invites: 1 expired, 1 active.
	if _, err := st.CreateInvite(t.Context(), store.CreateInviteParams{
		Email: "old@example.test", TokenHash: "hash-old", CreatedBy: &user.ID,
		Now: start.Add(-10 * 24 * time.Hour), ExpiresAt: start.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("CreateInvite(old): %v", err)
	}
	if _, err := st.CreateInvite(t.Context(), store.CreateInviteParams{
		Email: "fresh@example.test", TokenHash: "hash-fresh", CreatedBy: &user.ID,
		Now: start.Add(-time.Hour), ExpiresAt: start.Add(6 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateInvite(fresh): %v", err)
	}

	// Password resets: 1 expired, 1 already used, 1 active.
	if _, err := st.CreatePasswordReset(t.Context(), store.CreatePasswordResetParams{
		UserID: user.ID, TokenHash: "reset-expired", CreatedBy: &user.ID,
		Now: start.Add(-2 * time.Hour), ExpiresAt: start.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("CreatePasswordReset(expired): %v", err)
	}
	usedReset, err := st.CreatePasswordReset(t.Context(), store.CreatePasswordResetParams{
		UserID: user.ID, TokenHash: "reset-used", CreatedBy: &user.ID,
		Now: start.Add(-10 * time.Minute), ExpiresAt: start.Add(50 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreatePasswordReset(used): %v", err)
	}
	if err := st.WithTx(t.Context(), func(tx *store.Tx) error {
		ok, err := tx.ConsumePasswordReset(t.Context(), usedReset.ID, start.Add(-5*time.Minute))
		if err != nil {
			return err
		}
		if !ok {
			t.Fatal("expected ConsumePasswordReset to succeed")
		}
		return nil
	}); err != nil {
		t.Fatalf("ConsumePasswordReset: %v", err)
	}
	if _, err := st.CreatePasswordReset(t.Context(), store.CreatePasswordResetParams{
		UserID: user.ID, TokenHash: "reset-active", CreatedBy: &user.ID,
		Now: start.Add(-5 * time.Minute), ExpiresAt: start.Add(55 * time.Minute),
	}); err != nil {
		t.Fatalf("CreatePasswordReset(active): %v", err)
	}

	// Audit entries: 1 older than 180d, 1 recent.
	if _, err := st.InsertAudit(t.Context(), store.InsertAuditParams{
		At: start.Add(-181 * 24 * time.Hour), ActorType: store.AuditActorSystem, Action: "auth.login",
	}); err != nil {
		t.Fatalf("InsertAudit(old): %v", err)
	}
	if _, err := st.InsertAudit(t.Context(), store.InsertAuditParams{
		At: start.Add(-10 * 24 * time.Hour), ActorType: store.AuditActorSystem, Action: "auth.login",
	}); err != nil {
		t.Fatalf("InsertAudit(recent): %v", err)
	}

	hk := retention.NewHousekeeping(st, clk, nil, cfg)
	stats, err := hk.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.DeletedSessions != 2 {
		t.Fatalf("DeletedSessions = %d, want 2", stats.DeletedSessions)
	}
	if stats.DeletedInvites != 1 {
		t.Fatalf("DeletedInvites = %d, want 1", stats.DeletedInvites)
	}
	if stats.DeletedPasswordResets != 2 {
		t.Fatalf("DeletedPasswordResets = %d, want 2", stats.DeletedPasswordResets)
	}
	if stats.DeletedAuditEntries != 1 {
		t.Fatalf("DeletedAuditEntries = %d, want 1", stats.DeletedAuditEntries)
	}

	// Verify active rows survived.
	if _, err := st.GetSessionByID(t.Context(), "sess-active"); err != nil {
		t.Fatalf("expected active session kept: %v", err)
	}
	if _, err := st.GetInviteByTokenHash(t.Context(), "hash-fresh"); err != nil {
		t.Fatalf("expected active invite kept: %v", err)
	}
	if _, err := st.GetPasswordResetByTokenHash(t.Context(), "reset-active"); err != nil {
		t.Fatalf("expected active password reset kept: %v", err)
	}
	entries, err := st.ListAuditEntries(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("remaining audit entries = %d, want 1", len(entries))
	}

	// Verify Run executes on the 24h ticker when fake clock advances.
	runCtx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		hk.Run(runCtx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(2 * time.Second)
	for clk.TickerCount() < 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}

	// Advance 24h -> reset-active (55m TTL) is now expired and deleted by daily tick.
	clk.Advance(24 * time.Hour)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := st.GetPasswordResetByTokenHash(t.Context(), "reset-active"); err != nil {
			break
		}
		runtime.Gosched()
	}
	if _, err := st.GetPasswordResetByTokenHash(t.Context(), "reset-active"); err == nil {
		t.Fatal("expected reset-active to be deleted by daily Run tick")
	}
}

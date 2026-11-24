package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/store"
)

const defaultHousekeepingInterval = 24 * time.Hour

// HousekeepingStats summarizes expired records deleted during one housekeeping pass.
type HousekeepingStats struct {
	DeletedSessions       int64
	DeletedInvites        int64
	DeletedPasswordResets int64
	DeletedAuditEntries   int64
}

// Housekeeping runs daily to remove expired/idle sessions, expired invites,
// expired or used password resets, and audit log entries older than
// MAILCUBBY_AUDIT_RETENTION (FR-099).
type Housekeeping struct {
	store    *store.Store
	clk      clock.Clock
	log      *slog.Logger
	cfg      config.Config
	interval time.Duration

	mu sync.Mutex
}

// NewHousekeeping constructs a Housekeeping worker backed by st, clk, log, and cfg.
func NewHousekeeping(st *store.Store, clk clock.Clock, log *slog.Logger, cfg config.Config) *Housekeeping {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Housekeeping{
		store:    st,
		clk:      clk,
		log:      log,
		cfg:      cfg,
		interval: defaultHousekeepingInterval,
	}
}

// Run executes RunOnce every 24 hours via the injected clock until ctx is cancelled.
func (h *Housekeeping) Run(ctx context.Context) {
	ticker := h.clk.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			stats, err := h.RunOnce(ctx)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					h.log.Error("housekeeping failed", "error", err)
				}
				continue
			}
			if stats.DeletedSessions > 0 || stats.DeletedInvites > 0 || stats.DeletedPasswordResets > 0 || stats.DeletedAuditEntries > 0 {
				h.log.Info("housekeeping completed",
					"deleted_sessions", stats.DeletedSessions,
					"deleted_invites", stats.DeletedInvites,
					"deleted_password_resets", stats.DeletedPasswordResets,
					"deleted_audit_entries", stats.DeletedAuditEntries,
				)
			}
		}
	}
}

// RunOnce deletes all expired sessions, invites, reset tokens, and aged audit entries (FR-099).
func (h *Housekeeping) RunOnce(ctx context.Context) (HousekeepingStats, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.clk.Now().UTC()
	idleTimeout := h.cfg.SessionIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 7 * 24 * time.Hour
	}
	auditRetention := h.cfg.AuditRetention
	if auditRetention <= 0 {
		auditRetention = 180 * 24 * time.Hour
	}

	var stats HousekeepingStats
	var err error

	stats.DeletedSessions, err = h.store.DeleteExpiredSessions(ctx, now, now.Add(-idleTimeout))
	if err != nil {
		return stats, fmt.Errorf("retention: housekeeping sessions: %w", err)
	}

	stats.DeletedInvites, err = h.store.DeleteExpiredInvites(ctx, now)
	if err != nil {
		return stats, fmt.Errorf("retention: housekeeping invites: %w", err)
	}

	stats.DeletedPasswordResets, err = h.store.DeleteExpiredPasswordResets(ctx, now)
	if err != nil {
		return stats, fmt.Errorf("retention: housekeeping password resets: %w", err)
	}

	stats.DeletedAuditEntries, err = h.store.DeleteOldAuditEntries(ctx, now.Add(-auditRetention))
	if err != nil {
		return stats, fmt.Errorf("retention: housekeeping audit log: %w", err)
	}

	return stats, nil
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	sseHeartbeatInterval = 25 * time.Second
	sseRetryDirective    = "retry: 3000\n\n"
	sseHeartbeatComment  = ": ping\n\n"
)

type sseConn struct {
	id        uint64
	userID    int64
	sessionID string
	tokenID   int64
	evictCh   chan struct{}
	evicted   bool
}

// sseTracker bounds concurrent SSE connections per principal (SPEC FR-092),
// closing the oldest stream whenever the limit is exceeded.
type sseTracker struct {
	maxPerUser int

	mu     sync.Mutex
	nextID uint64
	conns  map[string][]*sseConn
}

func newSSETracker(maxPerUser int) *sseTracker {
	if maxPerUser <= 0 {
		maxPerUser = 10
	}
	return &sseTracker{
		maxPerUser: maxPerUser,
		conns:      make(map[string][]*sseConn),
	}
}

func (t *sseTracker) register(key string, userID int64, sessionID string, tokenID int64) (<-chan struct{}, func()) {
	t.mu.Lock()
	t.nextID++
	conn := &sseConn{
		id:        t.nextID,
		userID:    userID,
		sessionID: sessionID,
		tokenID:   tokenID,
		evictCh:   make(chan struct{}),
	}
	list := t.conns[key]
	for len(list) >= t.maxPerUser {
		oldest := list[0]
		list = list[1:]
		if !oldest.evicted {
			oldest.evicted = true
			close(oldest.evictCh)
		}
	}
	list = append(list, conn)
	t.conns[key] = list
	t.mu.Unlock()

	var once sync.Once
	unregister := func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			cur := t.conns[key]
			for i, c := range cur {
				if c.id == conn.id {
					cur = append(cur[:i], cur[i+1:]...)
					break
				}
			}
			if len(cur) == 0 {
				delete(t.conns, key)
			} else {
				t.conns[key] = cur
			}
		})
	}
	return conn.evictCh, unregister
}

func (t *sseTracker) closeMatching(match func(*sseConn) bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, list := range t.conns {
		for _, c := range list {
			if match(c) && !c.evicted {
				c.evicted = true
				close(c.evictCh)
			}
		}
	}
}

func (t *sseTracker) closeSession(sessionID string) {
	if sessionID == "" {
		return
	}
	t.closeMatching(func(c *sseConn) bool {
		return c.sessionID == sessionID
	})
}

func (t *sseTracker) closeOtherSessions(userID int64, keepSessionID string) {
	if userID == 0 {
		return
	}
	t.closeMatching(func(c *sseConn) bool {
		return c.userID == userID && c.sessionID != "" && c.sessionID != keepSessionID
	})
}

func (t *sseTracker) closeUser(userID int64) {
	if userID == 0 {
		return
	}
	t.closeMatching(func(c *sseConn) bool {
		return c.userID == userID
	})
}

func (t *sseTracker) closeToken(tokenID int64) {
	if tokenID == 0 {
		return
	}
	t.closeMatching(func(c *sseConn) bool {
		return c.tokenID == tokenID
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)
	rawSlug := strings.TrimSpace(r.URL.Query().Get("project"))

	var (
		filter          = events.AllFilter()
		projectSet      *events.ProjectSetFilter
		pinnedProjectID *int64
	)

	if s.cfg.Mode == config.ModeLocal {
		if rawSlug != "" {
			proj, err := s.store.GetProjectBySlug(ctx, rawSlug)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
					return
				}
				s.log.Error("resolve project for sse failed",
					"request_id", RequestIDFromContext(ctx),
					"slug", rawSlug,
					"error", err,
				)
				writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				return
			}
			filter = events.ProjectFilter(proj.ID)
		}
	} else {
		// Server mode: resolve optional ?project= access and configure live membership filter (FR-091).
		if rawSlug != "" {
			proj, err := s.store.GetProjectBySlug(ctx, rawSlug)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
					return
				}
				s.log.Error("resolve project for sse failed",
					"request_id", RequestIDFromContext(ctx),
					"slug", rawSlug,
					"error", err,
				)
				writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				return
			}
			if principal.TokenProjectID != nil && *principal.TokenProjectID != proj.ID {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}
			_, memErr := s.store.GetProjectMember(ctx, proj.ID, principal.UserID)
			isMember := memErr == nil
			if memErr != nil && !errors.Is(memErr, store.ErrNotFound) {
				s.log.Error("resolve project membership for sse failed",
					"request_id", RequestIDFromContext(ctx),
					"project_id", proj.ID,
					"user_id", principal.UserID,
					"error", memErr,
				)
				writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				return
			}
			if !principal.IsAdmin && !isMember {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}
			if principal.IsAdmin && !isMember {
				s.recordAdminProjectAccess(ctx, principal, proj)
			}
			id := proj.ID
			pinnedProjectID = &id
		}

		projectSet = events.NewProjectSetFilter(principal.UserID, nil)
		// Every project.deleted is queued, whatever the current set, so the loop below
		// can drop an ID that the initial refresh read just before the project was
		// deleted. The loop discards deletions of projects outside the set.
		filter = func(e events.Event) bool {
			return e.Type == events.TypeProjectDeleted || projectSet.Match(e)
		}
	}

	// Subscribe before computing the project set so a project.deleted published
	// in between cannot be missed.
	var (
		subCh <-chan events.Event
		unsub = func() {}
	)
	if s.bus != nil {
		var sub *events.Subscription
		sub, unsub = s.bus.Subscribe(filter)
		subCh = sub.Events()
	}
	defer unsub()

	if projectSet != nil {
		if err := s.refreshSSEProjectSet(ctx, principal, pinnedProjectID, projectSet); err != nil {
			s.log.Error("initialize sse project set failed",
				"request_id", RequestIDFromContext(ctx),
				"user_id", principal.UserID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
	}

	connKey := fmt.Sprintf("%s:%d", principal.Kind, principal.UserID)
	if principal.Kind == PrincipalToken && principal.TokenID > 0 {
		connKey = fmt.Sprintf("token:%d", principal.TokenID)
	}
	evictCh, unregister := s.sse.register(connKey, principal.UserID, principal.SessionID, principal.TokenID)
	defer unregister()

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Register heartbeat ticker before flushing initial retry directive so callers
	// synchronized on receiving "retry:" observe deterministic fake-clock advances.
	ticker := s.clk.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	if _, err := w.Write([]byte(sseRetryDirective)); err != nil {
		return
	}
	_ = rc.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdownCh:
			return
		case <-evictCh:
			return
		case <-ticker.C():
			if _, err := w.Write([]byte(sseHeartbeatComment)); err != nil {
				return
			}
			_ = rc.Flush()
		case ev, open := <-subCh:
			if !open {
				return
			}
			closeAfter := false
			if projectSet != nil {
				switch {
				case ev.Type == events.TypeMembershipChanged:
					if err := s.refreshSSEProjectSet(ctx, principal, pinnedProjectID, projectSet); err != nil {
						s.log.Warn("refresh sse project set failed",
							"request_id", RequestIDFromContext(ctx),
							"user_id", principal.UserID,
							"error", err,
						)
					}
				case !projectSet.AllowsProject(ev.ProjectID):
					// Drop any buffered event for a project the user lost access to.
					continue
				case ev.Type == events.TypeProjectDeleted:
					// A deleted project's ID may be reused by a new project: stop
					// matching it so the new project's events are never delivered
					// here. Streams scoped to that project end after this event,
					// because a later refresh would restore the pinned ID unchecked.
					projectSet.Remove(ev.ProjectID)
					closeAfter = (pinnedProjectID != nil && *pinnedProjectID == ev.ProjectID) ||
						(principal.TokenProjectID != nil && *principal.TokenProjectID == ev.ProjectID)
				}
			}
			payload, err := json.Marshal(ev.Payload)
			if err != nil {
				s.log.Error("marshal sse event failed",
					"request_id", RequestIDFromContext(ctx),
					"event_type", string(ev.Type),
					"error", err,
				)
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
				return
			}
			_ = rc.Flush()
			if closeAfter {
				return
			}
		}
	}
}

func (s *Server) refreshSSEProjectSet(ctx context.Context, principal Principal, pinnedProjectID *int64, filter *events.ProjectSetFilter) error {
	user, err := s.store.GetUserByID(ctx, principal.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			filter.SetAllowAll(false)
			filter.Set(nil)
			return nil
		}
		return err
	}
	if user.DisabledAt != nil {
		filter.SetAllowAll(false)
		filter.Set(nil)
		return nil
	}

	if user.IsAdmin {
		if pinnedProjectID != nil {
			filter.SetAllowAll(false)
			filter.Set([]int64{*pinnedProjectID})
			return nil
		}
		if principal.TokenProjectID != nil {
			filter.SetAllowAll(false)
			filter.Set([]int64{*principal.TokenProjectID})
			return nil
		}
		filter.SetAllowAll(true)
		filter.Set(nil)
		return nil
	}

	filter.SetAllowAll(false)
	ids, err := s.store.ListProjectIDsForUser(ctx, user.ID)
	if err != nil {
		return err
	}
	if principal.TokenProjectID != nil {
		if slices.Contains(ids, *principal.TokenProjectID) {
			ids = []int64{*principal.TokenProjectID}
		} else {
			ids = nil
		}
	}
	if pinnedProjectID != nil {
		if slices.Contains(ids, *pinnedProjectID) {
			ids = []int64{*pinnedProjectID}
		} else {
			ids = nil
		}
	}
	filter.Set(ids)
	return nil
}

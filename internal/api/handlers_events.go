package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	sseHeartbeatInterval = 25 * time.Second
	sseRetryDirective    = "retry: 3000\n\n"
	sseHeartbeatComment  = ": ping\n\n"
)

type sseConn struct {
	id      uint64
	evictCh chan struct{}
	evicted bool
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

func (t *sseTracker) register(key string) (<-chan struct{}, func()) {
	t.mu.Lock()
	t.nextID++
	conn := &sseConn{
		id:      t.nextID,
		evictCh: make(chan struct{}),
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

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	filter := events.AllFilter()
	if rawSlug := strings.TrimSpace(r.URL.Query().Get("project")); rawSlug != "" {
		proj, err := s.store.GetProjectBySlug(r.Context(), rawSlug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}
			s.log.Error("resolve project for sse failed",
				"request_id", RequestIDFromContext(r.Context()),
				"slug", rawSlug,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		filter = events.ProjectFilter(proj.ID)
	}

	principal := PrincipalFromContext(r.Context())
	connKey := fmt.Sprintf("%s:%d", principal.Kind, principal.UserID)
	evictCh, unregister := s.sse.register(connKey)
	defer unregister()

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
		case <-r.Context().Done():
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
			payload, err := json.Marshal(ev.Payload)
			if err != nil {
				s.log.Error("marshal sse event failed",
					"request_id", RequestIDFromContext(r.Context()),
					"event_type", string(ev.Type),
					"error", err,
				)
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

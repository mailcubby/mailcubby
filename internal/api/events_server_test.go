package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
)

// sseStreamWriter is an in-process streaming http.ResponseWriter that notifies
// tests on every Flush so SSE filtering can be verified without TCP sockets.
type sseStreamWriter struct {
	header http.Header
	mu     sync.Mutex
	code   int
	buf    bytes.Buffer
	notify chan struct{}
}

func newSSEStreamWriter() *sseStreamWriter {
	return &sseStreamWriter{
		header: make(http.Header),
		notify: make(chan struct{}, 64),
	}
}

func (w *sseStreamWriter) Header() http.Header { return w.header }

func (w *sseStreamWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	w.code = statusCode
	w.mu.Unlock()
}

func (w *sseStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	return n, err
}

func (w *sseStreamWriter) Flush() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *sseStreamWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *sseStreamWriter) waitForContains(t *testing.T, substr string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(w.String(), substr) {
			return
		}
		select {
		case <-w.notify:
		case <-deadline.C:
			t.Fatalf("timed out waiting for SSE stream to contain %q; got:\n%s", substr, w.String())
		}
	}
}

// FR-091: SSE subscribers receive only events for projects they belong to, and
// membership changes take effect on open streams via membership.changed refresh.
func TestSSEMembershipFilteringAndLiveRefresh(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	ownerA := env.createUser(t, "ownera@example.test", "Owner A", pass, false, true, false)
	ownerB := env.createUser(t, "ownerb@example.test", "Owner B", pass, false, true, false)
	viewer := env.createUser(t, "viewer@example.test", "Viewer A", pass, false, false, false)

	projA, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "proj-a", Name: "Project A", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(A): %v", err)
	}
	projB, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "proj-b", Name: "Project B", Color: "green", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(B): %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), projA.ID, ownerA.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(ownerA): %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), projA.ID, viewer.ID, "viewer", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(viewer): %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), projB.ID, ownerB.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(ownerB): %v", err)
	}

	ownerACookie, ownerACSRF := loginAndGetCSRF(t, h, ownerA.Email, pass)
	ownerBCookie, ownerBCSRF := loginAndGetCSRF(t, h, ownerB.Email, pass)
	viewerCookie, _ := loginAndGetCSRF(t, h, viewer.Email, pass)

	// Viewer requesting ?project=proj-b gets 404 (never reveals project existence).
	rec := doReq(t, h, http.MethodGet, "/api/v1/events?project=proj-b", nil, viewerCookie, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("viewer GET /events?project=proj-b status = %d, want 404", rec.Code)
	}

	// Open live SSE stream as viewer of project A.
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sseReq := httptest.NewRequestWithContext(streamCtx, http.MethodGet, "/api/v1/events", nil)
	sseReq.AddCookie(viewerCookie)
	sw := newSSEStreamWriter()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(sw, sseReq)
	}()

	sw.waitForContains(t, "retry: 3000")

	// Send test message into project B -> viewer of A must NOT receive any event for proj-b.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/proj-b/send-test", nil, ownerBCookie, csrfHeaders(ownerBCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("send-test proj-b status = %d, want 201", rec.Code)
	}

	// Send test message into project A -> viewer of A receives message.created for proj-a.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/proj-a/send-test", nil, ownerACookie, csrfHeaders(ownerACSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("send-test proj-a status = %d, want 201", rec.Code)
	}
	sw.waitForContains(t, `"project":"proj-a"`)
	if strings.Contains(sw.String(), `"proj-b"`) {
		t.Fatalf("viewer A received leaked event from proj-b:\n%s", sw.String())
	}

	// Owner A removes viewer from project A -> viewer receives membership.changed.
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/proj-a/members/"+strconv.FormatInt(viewer.ID, 10), nil, ownerACookie, csrfHeaders(ownerACSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete viewer from proj-a status = %d, want 204", rec.Code)
	}
	sw.waitForContains(t, "event: membership.changed")

	// Record stream snapshot after membership removal; subsequent proj-a events must not arrive.
	beforeCount := strings.Count(sw.String(), "event: message.created")

	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/proj-a/send-test", nil, ownerACookie, csrfHeaders(ownerACSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("second send-test proj-a status = %d, want 201", rec.Code)
	}

	// Trigger a heartbeat via fake clock so we deterministically synchronize past any event processing.
	env.clk.Advance(25 * time.Second)
	sw.waitForContains(t, ": ping")

	afterCount := strings.Count(sw.String(), "event: message.created")
	if afterCount != beforeCount {
		t.Fatalf("removed viewer received %d new message.created events after removal; stream:\n%s", afterCount-beforeCount, sw.String())
	}

	cancel()
	<-done
}

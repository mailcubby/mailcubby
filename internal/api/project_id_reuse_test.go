package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

// FR-091, SPEC 12.5: a subscriber of a deleted project must never receive events
// of a later project that reuses the deleted project's ID. The reuse is forced
// with an explicit-ID insert so the test stays meaningful regardless of whether
// projects.id is AUTOINCREMENT.
func TestSSEDeletedProjectIDReuseDoesNotLeak(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	ownerX := env.createUser(t, "ownerx@tenant-x.test", "Owner X", pass, false, true, false)
	viewerX := env.createUser(t, "viewerx@tenant-x.test", "Viewer X", pass, false, false, false)
	admin := env.createUser(t, "admin@example.test", "Admin", pass, true, true, false)
	ownerY := env.createUser(t, "ownery@tenant-y.test", "Owner Y", pass, false, true, false)

	now := env.clk.Now().UTC()
	projW, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tenant-w", Name: "Tenant W", Color: "blue", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject(W): %v", err)
	}
	projX, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tenant-x", Name: "Tenant X", Color: "green", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject(X): %v", err)
	}
	for _, m := range []struct {
		pid  int64
		uid  int64
		role string
	}{
		{projW.ID, ownerX.ID, "owner"},
		{projW.ID, viewerX.ID, "viewer"},
		{projX.ID, ownerX.ID, "owner"},
		{projX.ID, viewerX.ID, "viewer"},
	} {
		if _, err := env.store.AddProjectMember(t.Context(), m.pid, m.uid, m.role, now); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}
	}

	ownerXCookie, ownerXCSRF := loginAndGetCSRF(t, h, ownerX.Email, pass)
	viewerCookie, _ := loginAndGetCSRF(t, h, viewerX.Email, pass)
	adminCookie, _ := loginAndGetCSRF(t, h, admin.Email, pass)
	ownerYCookie, ownerYCSRF := loginAndGetCSRF(t, h, ownerY.Email, pass)

	type stream struct {
		name string
		sw   *sseStreamWriter
		done chan struct{}
	}
	open := func(name, path string, cookie *http.Cookie) stream {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		req.AddCookie(cookie)
		s := stream{name: name, sw: newSSEStreamWriter(), done: make(chan struct{})}
		go func() {
			defer close(s.done)
			h.ServeHTTP(s.sw, req)
		}()
		s.sw.waitForContains(t, "retry: 3000")
		return s
	}

	unpinned := open("viewer unpinned", "/api/v1/events", viewerCookie)
	viewerPinned := open("viewer pinned", "/api/v1/events?project=tenant-x", viewerCookie)
	adminPinned := open("admin pinned", "/api/v1/events?project=tenant-x", adminCookie)

	rec := doReq(t, h, http.MethodDelete, "/api/v1/projects/tenant-x", map[string]any{"confirm_slug": "tenant-x"}, ownerXCookie, csrfHeaders(ownerXCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete tenant-x status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// Every subscriber of X still learns about the deletion.
	for _, s := range []stream{unpinned, viewerPinned, adminPinned} {
		s.sw.waitForContains(t, "event: project.deleted\ndata: {\"project\":\"tenant-x\"}")
	}

	// Streams pinned to the deleted project end after delivering project.deleted.
	for _, s := range []stream{viewerPinned, adminPinned} {
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s stream still open after its project was deleted:\n%s", s.name, s.sw.String())
		}
	}

	// Force a new project Y onto X's ID, owned by an unrelated user.
	err = env.store.WithTx(t.Context(), func(tx *store.Tx) error {
		_, err := tx.SQLTx().ExecContext(t.Context(),
			`INSERT INTO projects (id, slug, name, color, created_at, updated_at) VALUES (?, 'tenant-y', 'Tenant Y', 'red', ?, ?)`,
			projX.ID, now.UnixMilli(), now.UnixMilli())
		return err
	})
	if err != nil {
		t.Fatalf("insert tenant-y with reused id %d: %v", projX.ID, err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), projX.ID, ownerY.ID, "owner", now); err != nil {
		t.Fatalf("AddProjectMember(Y): %v", err)
	}
	env.bus.Publish(events.Event{
		Type:      events.TypeProjectCreated,
		ProjectID: projX.ID,
		Payload:   events.ProjectCreatedPayload{Project: "tenant-y"},
	})
	// A membership refresh on the open stream must not bring the reused ID back.
	env.bus.Publish(events.Event{Type: events.TypeMembershipChanged, UserID: viewerX.ID})

	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/tenant-y/send-test", nil, ownerYCookie, csrfHeaders(ownerYCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("send-test tenant-y status = %d, want 201", rec.Code)
	}
	var sentY struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sentY)

	// Barrier: an event for W is queued after every Y event; once it is written,
	// all earlier Y events have been processed (the subscription channel is FIFO).
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/tenant-w/send-test", nil, ownerXCookie, csrfHeaders(ownerXCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("send-test tenant-w status = %d, want 201", rec.Code)
	}
	unpinned.sw.waitForContains(t, `"project":"tenant-w"`)

	for _, s := range []stream{unpinned, viewerPinned, adminPinned} {
		body := s.sw.String()
		if strings.Contains(body, "tenant-y") || (sentY.ID != "" && strings.Contains(body, sentY.ID)) {
			t.Errorf("%s stream leaked events of project tenant-y (reused id %d):\n%s", s.name, projX.ID, body)
		}
	}

	// FR-105: the admin's earlier access to X must not suppress the audit entry
	// for accessing Y in the same session.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tenant-y", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET tenant-y status = %d, want 200", rec.Code)
	}
	entries, err := env.store.ListAuditEntries(t.Context(), 100)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.Action == "project.admin_access" && e.ProjectID != nil && *e.ProjectID == projX.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("project.admin_access entries for tenant-y = %d, want 1", n)
	}
}

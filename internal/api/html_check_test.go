package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/compat"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// FR-079: GET /api/v1/projects/{project}/messages/{id}/html-check returns
// deterministic compatibility scores and per-client support, returns 404 for
// text-only messages, and enforces viewer_hide_technical in server mode.
func TestMessageHTMLCheckEndpoint(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixed)
	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	bus := events.NewBus()
	t.Cleanup(bus.Close)

	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "compat-local",
		Name:     "Compat Local",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      fixed,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, bus, nil)
	htmlRaw := "From: sender@example.test\r\n" +
		"To: rcpt@example.test\r\n" +
		"Subject: Modern CSS Email\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<!DOCTYPE html><html><head><style>" +
		"@media (prefers-color-scheme: dark) { .box { display: grid; border-radius: 8px; } }" +
		"</style></head><body><div class=\"box\"><img src=\"https://cdn.example/pic.webp\"></div></body></html>\r\n"

	textRaw := "From: sender@example.test\r\n" +
		"To: rcpt@example.test\r\n" +
		"Subject: Plain Only\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Hello plain world\r\n"

	htmlRefs, err := ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "sender@example.test",
		RcptTo:   []string{"rcpt@example.test"},
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte(htmlRaw)))
	if err != nil {
		t.Fatalf("Ingest html: %v", err)
	}
	textRefs, err := ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "sender@example.test",
		RcptTo:   []string{"rcpt@example.test"},
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte(textRaw)))
	if err != nil {
		t.Fatalf("Ingest text: %v", err)
	}

	cfg := config.Config{
		Mode:                     config.ModeLocal,
		BaseURL:                  "http://localhost:8025",
		DefaultProject:           "default",
		RenderRemoteImages:       config.RenderRemoteImagesOptIn,
		RenderInlinePDF:          true,
		APIWaitMaxTimeout:        30 * time.Second,
		SSEMaxConnectionsPerUser: 10,
	}
	srv := api.New(cfg, st, blobs, bus, api.Options{Clock: clk, Ingestor: ing})
	t.Cleanup(srv.Close)

	t.Run("HTML message returns deterministic compatibility report", func(t *testing.T) {
		path := "/api/v1/projects/compat-local/messages/" + htmlRefs[0].ID.String() + "/html-check"
		rec1 := doReq(t, srv.Handler(), http.MethodGet, path, nil, nil, nil)
		if rec1.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec1.Code, rec1.Body.String())
		}
		assertHeader(t, rec1.Header(), "Content-Security-Policy", wantAppCSP)
		assertHeader(t, rec1.Header(), "X-Content-Type-Options", "nosniff")

		var rep compat.Report
		if err := json.Unmarshal(rec1.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal report: %v", err)
		}
		if rep.Dataset.Source != "caniemail.com" || rep.Dataset.License != "MIT" {
			t.Errorf("unexpected dataset meta: %+v", rep.Dataset)
		}
		if len(rep.Clients) == 0 {
			t.Fatalf("expected non-empty clients list")
		}
		if rep.Summary.FeaturesCount != len(rep.Features) || len(rep.Features) == 0 {
			t.Fatalf("unexpected features count: summary=%d len=%d", rep.Summary.FeaturesCount, len(rep.Features))
		}

		slugs := make(map[string]bool, len(rep.Features))
		for _, f := range rep.Features {
			slugs[f.Slug] = true
		}
		for _, want := range []string{"css-at-media-prefers-color-scheme", "css-display-grid", "css-border-radius", "image-webp"} {
			if !slugs[want] {
				t.Errorf("report missing expected feature %q", want)
			}
		}

		// Second request (served from LRU cache) returns identical JSON bytes.
		rec2 := doReq(t, srv.Handler(), http.MethodGet, path, nil, nil, nil)
		if rec2.Code != http.StatusOK {
			t.Fatalf("cached status = %d, want 200", rec2.Code)
		}
		if !bytes.Equal(rec1.Body.Bytes(), rec2.Body.Bytes()) {
			t.Errorf("second html-check response differed from first")
		}
	})

	t.Run("text-only message returns 404", func(t *testing.T) {
		path := "/api/v1/projects/compat-local/messages/" + textRefs[0].ID.String() + "/html-check"
		rec := doReq(t, srv.Handler(), http.MethodGet, path, nil, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("text-only status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown message returns 404", func(t *testing.T) {
		rec := doReq(t, srv.Handler(), http.MethodGet, "/api/v1/projects/compat-local/messages/01ARZ3NDEKTSV4RRFFQ69G5FAV/html-check", nil, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown message status = %d, want 404", rec.Code)
		}
	})
}

// FR-079: verify viewer_hide_technical enforcement for html-check in server mode.
func TestMessageHTMLCheckViewerHideTechnical(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "hc-owner@example.test", "Owner", pass, false, true, false)
	viewer := env.createUser(t, "hc-viewer@example.test", "Viewer", pass, false, false, false)

	hideOn := store.DefaultProjectSettings()
	hideOn.ViewerHideTechnical = true
	projHideOn, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "hc-hide-on", Name: "Hide On", Color: "blue", Settings: hideOn, Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(hide-on): %v", err)
	}

	hideOff := store.DefaultProjectSettings()
	hideOff.ViewerHideTechnical = false
	projHideOff, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "hc-hide-off", Name: "Hide Off", Color: "green", Settings: hideOff, Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(hide-off): %v", err)
	}

	for _, p := range []store.Project{projHideOn, projHideOff} {
		if _, err := env.store.AddProjectMember(t.Context(), p.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember(owner): %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), p.ID, viewer.ID, "viewer", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember(viewer): %v", err)
		}
	}

	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
	viewerCookie, _ := loginAndGetCSRF(t, h, viewer.Email, pass)

	sendTest := func(slug string) string {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/send-test", nil, ownerCookie, csrfHeaders(ownerCSRF))
		if rec.Code != http.StatusCreated {
			t.Fatalf("send-test(%s) status = %d, want 201", slug, rec.Code)
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal send-test: %v", err)
		}
		return out.ID
	}

	msgOn := sendTest(projHideOn.Slug)
	msgOff := sendTest(projHideOff.Slug)

	// Viewer blocked (403) when viewer_hide_technical=true.
	rec := doReq(t, h, http.MethodGet, "/api/v1/projects/"+projHideOn.Slug+"/messages/"+msgOn+"/html-check", nil, viewerCookie, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer with hide_technical=true status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// Viewer allowed (200) when viewer_hide_technical=false.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/"+projHideOff.Slug+"/messages/"+msgOff+"/html-check", nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer with hide_technical=false status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

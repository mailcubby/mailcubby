package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

const (
	wantAppCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; " +
		"frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"
	wantRenderCSPBlocked = "sandbox allow-popups allow-popups-to-escape-sandbox; " +
		"default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; " +
		"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	wantRenderCSPImages = "sandbox allow-popups allow-popups-to-escape-sandbox; " +
		"default-src 'none'; img-src data: cid: https:; style-src 'unsafe-inline'; " +
		"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	wantRenderPDFCSP = "default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	wantAttachmentCSP     = "sandbox; default-src 'none'"
	wantPermissionsPolicy = "camera=(), microphone=(), geolocation=()"
)

// SPEC 12.1, 12.2, 12.3, ARCHITECTURE 3.10: verify exact security headers on every
// route class: app (HTTP + HTTPS/HSTS), render (blocked, ?images=1, off), attachment
// (download, inline PNG, forced octet-stream on HTML/SVG), raw/html source, and SSE.
func TestRouteClassSecurityHeaders(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixed)
	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	bus := events.NewBus()
	t.Cleanup(bus.Close)

	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "hdrs",
		Name:     "Headers",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      fixed,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, bus, nil)
	rawEML := "From: alice@example.test\r\n" +
		"To: bob@example.test\r\n" +
		"Subject: Header Check\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><p>Hello</p><img src=\"https://cdn.example/x.png\"></body></html>\r\n" +
		"--B\r\n" +
		"Content-Type: text/html\r\n" +
		"Content-Disposition: attachment; filename=\"evil.html\"\r\n\r\n" +
		"<script>alert(1)</script>\r\n" +
		"--B\r\n" +
		"Content-Type: image/svg+xml\r\n" +
		"Content-Disposition: attachment; filename=\"evil.svg\"\r\n\r\n" +
		"<svg onload=\"alert(1)\"/>\r\n" +
		"--B\r\n" +
		"Content-Type: application/atom+xml\r\n" +
		"Content-Disposition: attachment; filename=\"feed.atom\"\r\n\r\n" +
		"<feed xmlns=\"http://www.w3.org/2005/Atom\"/>\r\n" +
		"--B\r\n" +
		"Content-Type: application/rss+xml\r\n" +
		"Content-Disposition: attachment; filename=\"feed.rss\"\r\n\r\n" +
		"<rss version=\"2.0\"/>\r\n" +
		"--B\r\n" +
		"Content-Type: text/xml\r\n" +
		"Content-Disposition: attachment; filename=\"data.xml\"\r\n\r\n" +
		"<root/>\r\n" +
		"--B\r\n" +
		"Content-Type: application/xml\r\n" +
		"Content-Disposition: attachment; filename=\"config.xml\"\r\n\r\n" +
		"<cfg/>\r\n" +
		"--B\r\n" +
		"Content-Type: foo/xml\r\n" +
		"Content-Disposition: attachment; filename=\"custom.xml\"\r\n\r\n" +
		"<custom/>\r\n" +
		"--B\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Disposition: attachment; filename=\"pic.png\"\r\n\r\n" +
		"\x89PNG\r\n\x1a\n" +
		"--B\r\n" +
		"Content-Type: image/jpeg\r\n" +
		"Content-Disposition: attachment; filename=\"pic.jpg\"\r\n\r\n" +
		"\xff\xd8\xff\xd9\r\n" +
		"--B\r\n" +
		"Content-Type: image/gif\r\n" +
		"Content-Disposition: attachment; filename=\"pic.gif\"\r\n\r\n" +
		"GIF89a\r\n" +
		"--B\r\n" +
		"Content-Type: image/webp\r\n" +
		"Content-Disposition: attachment; filename=\"pic.webp\"\r\n\r\n" +
		"RIFF\x00\x00\x00\x00WEBP\r\n" +
		"--B\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n\r\n" +
		"%PDF-1.4\r\n" +
		"--B--\r\n"

	refs, err := ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "alice@example.test",
		RcptTo:   []string{"bob@example.test"},
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte(rawEML)))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	msgID := refs[0].ID.String()

	makeSrv := func(baseURL string, hsts bool, remoteImages config.RenderRemoteImages, inlinePDF bool) *api.Server {
		cfg := config.Config{
			Mode:                     config.ModeLocal,
			BaseURL:                  baseURL,
			HSTS:                     hsts,
			DefaultProject:           "default",
			RenderRemoteImages:       remoteImages,
			RenderInlinePDF:          inlinePDF,
			APIWaitMaxTimeout:        30 * time.Second,
			SSEMaxConnectionsPerUser: 10,
		}
		srv := api.New(cfg, st, blobs, bus, api.Options{Clock: clk, Ingestor: ing})
		t.Cleanup(srv.Close)
		return srv
	}

	httpSrv := makeSrv("http://localhost:8025", true, config.RenderRemoteImagesOptIn, true)
	httpsSrv := makeSrv("https://mail.example.test", true, config.RenderRemoteImagesOptIn, true)
	remoteOffSrv := makeSrv("http://localhost:8025", false, config.RenderRemoteImagesOff, true)
	pdfOffSrv := makeSrv("http://localhost:8025", false, config.RenderRemoteImagesOptIn, false)

	// Fetch attachment IDs from message detail.
	detailRec := doReq(t, httpSrv.Handler(), http.MethodGet, "/api/v1/projects/hdrs/messages/"+msgID, nil, nil, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET detail status = %d, want 200", detailRec.Code)
	}
	var detail struct {
		Attachments []struct {
			ID       int64  `json:"id"`
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	attByName := make(map[string]int64, len(detail.Attachments))
	for _, a := range detail.Attachments {
		attByName[a.Filename] = a.ID
	}

	t.Run("app route class (HTTP vs HTTPS HSTS)", func(t *testing.T) {
		rec := doReq(t, httpSrv.Handler(), http.MethodGet, "/api/v1/meta", nil, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /meta status = %d, want 200", rec.Code)
		}
		assertHeader(t, rec.Header(), "Content-Security-Policy", wantAppCSP)
		assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
		assertHeader(t, rec.Header(), "Referrer-Policy", "same-origin")
		assertHeader(t, rec.Header(), "X-Frame-Options", "DENY")
		assertHeader(t, rec.Header(), "Permissions-Policy", wantPermissionsPolicy)
		assertHeaderAbsent(t, rec.Header(), "Strict-Transport-Security")

		recHTTPS := doReq(t, httpsSrv.Handler(), http.MethodGet, "/api/v1/meta", nil, nil, nil)
		assertHeader(t, recHTTPS.Header(), "Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	})

	t.Run("render route class (blocked, ?images=1, and remote images off)", func(t *testing.T) {
		rec := doReq(t, httpSrv.Handler(), http.MethodGet, "/render/hdrs/"+msgID, nil, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /render status = %d, want 200", rec.Code)
		}
		assertHeader(t, rec.Header(), "Content-Type", "text/html; charset=utf-8")
		assertHeader(t, rec.Header(), "Content-Security-Policy", wantRenderCSPBlocked)
		assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
		assertHeader(t, rec.Header(), "Referrer-Policy", "no-referrer")
		assertHeader(t, rec.Header(), "Cache-Control", "private, no-store")
		assertHeader(t, rec.Header(), "Cross-Origin-Resource-Policy", "same-origin")
		assertHeader(t, rec.Header(), "X-DNS-Prefetch-Control", "off")
		assertHeaderAbsent(t, rec.Header(), "X-Frame-Options")
		assertHeaderAbsent(t, rec.Header(), "Permissions-Policy")

		recImg := doReq(t, httpSrv.Handler(), http.MethodGet, "/render/hdrs/"+msgID+"?images=1", nil, nil, nil)
		assertHeader(t, recImg.Header(), "Content-Security-Policy", wantRenderCSPImages)

		// When MAILCUBBY_RENDER_REMOTE_IMAGES=off, ?images=1 must stay blocked.
		recOff := doReq(t, remoteOffSrv.Handler(), http.MethodGet, "/render/hdrs/"+msgID+"?images=1", nil, nil, nil)
		assertHeader(t, recOff.Header(), "Content-Security-Policy", wantRenderCSPBlocked)
	})

	t.Run("pdf attachment render route class (ADR 0009)", func(t *testing.T) {
		pdfID := attByName["doc.pdf"]
		pdfPath := "/render/hdrs/" + msgID + "/attachments/" + strconv.FormatInt(pdfID, 10)
		rec := doReq(t, httpSrv.Handler(), http.MethodGet, pdfPath, nil, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", pdfPath, rec.Code, rec.Body.String())
		}
		assertHeader(t, rec.Header(), "Content-Type", "application/pdf")
		assertHeader(t, rec.Header(), "Content-Disposition", `inline; filename="doc.pdf"; filename*=UTF-8''doc.pdf`)
		assertHeader(t, rec.Header(), "Content-Security-Policy", wantRenderPDFCSP)
		assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
		assertHeader(t, rec.Header(), "Referrer-Policy", "no-referrer")
		assertHeader(t, rec.Header(), "Cache-Control", "private, no-store")
		assertHeader(t, rec.Header(), "Cross-Origin-Resource-Policy", "same-origin")
		assertHeader(t, rec.Header(), "X-DNS-Prefetch-Control", "off")
		assertHeaderAbsent(t, rec.Header(), "X-Frame-Options")
		assertHeaderAbsent(t, rec.Header(), "Permissions-Policy")
		assertHeaderAbsent(t, rec.Header(), "Strict-Transport-Security")

		recHTTPS := doReq(t, httpsSrv.Handler(), http.MethodGet, pdfPath, nil, nil, nil)
		if recHTTPS.Code != http.StatusOK {
			t.Fatalf("GET %s (HTTPS) status = %d, want 200", pdfPath, recHTTPS.Code)
		}
		assertHeader(t, recHTTPS.Header(), "Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Non-PDF attachments must be rejected with 415 Unsupported Media Type.
		for _, nonPDF := range []string{"pic.png", "evil.html", "evil.svg"} {
			nonPDFPath := "/render/hdrs/" + msgID + "/attachments/" + strconv.FormatInt(attByName[nonPDF], 10)
			nonPDFRec := doReq(t, httpSrv.Handler(), http.MethodGet, nonPDFPath, nil, nil, nil)
			if nonPDFRec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("GET %s (%s) status = %d, want 415", nonPDFPath, nonPDF, nonPDFRec.Code)
			}
		}

		// Kill-switch MAILCUBBY_RENDER_INLINE_PDF=false returns 404.
		recDisabled := doReq(t, pdfOffSrv.Handler(), http.MethodGet, pdfPath, nil, nil, nil)
		if recDisabled.Code != http.StatusNotFound {
			t.Errorf("GET %s (inline PDF disabled) status = %d, want 404", pdfPath, recDisabled.Code)
		}
	})

	t.Run("attachment and eml route class", func(t *testing.T) {
		for _, tc := range []struct {
			file     string
			query    string
			wantCT   string
			wantDisp string
		}{
			{file: "evil.html", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "evil.svg", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "feed.atom", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "feed.rss", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "data.xml", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "config.xml", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "custom.xml", query: "?inline=1", wantCT: "application/octet-stream", wantDisp: "attachment;"},
			{file: "pic.png", query: "", wantCT: "image/png", wantDisp: "attachment;"},
			{file: "pic.png", query: "?inline=1", wantCT: "image/png", wantDisp: "inline;"},
			{file: "pic.jpg", query: "?inline=1", wantCT: "image/jpeg", wantDisp: "inline;"},
			{file: "pic.gif", query: "?inline=1", wantCT: "image/gif", wantDisp: "inline;"},
			{file: "pic.webp", query: "?inline=1", wantCT: "image/webp", wantDisp: "inline;"},
			{file: "doc.pdf", query: "?inline=1", wantCT: "application/pdf", wantDisp: "attachment;"},
		} {
			id, found := attByName[tc.file]
			if !found {
				t.Fatalf("attachment %q missing from message detail: %+v", tc.file, attByName)
			}
			path := "/api/v1/projects/hdrs/messages/" + msgID + "/attachments/" + strconv.FormatInt(id, 10) + tc.query
			rec := doReq(t, httpSrv.Handler(), http.MethodGet, path, nil, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
			}
			assertHeader(t, rec.Header(), "Content-Type", tc.wantCT)
			assertHeader(t, rec.Header(), "Content-Security-Policy", wantAttachmentCSP)
			assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
			assertHeader(t, rec.Header(), "Cache-Control", "private, no-store")
			if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, tc.wantDisp) {
				t.Errorf("%s Content-Disposition = %q, want prefix %q", path, disp, tc.wantDisp)
			}
		}

		// SPEC 12.2, FR-074: stored attachments with empty, malformed, or header-injecting
		// Content-Type values must always be coerced to application/octet-stream and
		// never honour ?inline=1.
		msgRow, err := st.GetMessageByID(t.Context(), proj.ID, msgID)
		if err != nil {
			t.Fatalf("GetMessageByID: %v", err)
		}
		pngAtt, err := st.GetAttachmentByID(t.Context(), proj.ID, msgRow.PK, attByName["pic.png"])
		if err != nil {
			t.Fatalf("GetAttachmentByID(pic.png): %v", err)
		}
		for _, bad := range []struct {
			name        string
			contentType string
		}{
			{name: "empty", contentType: ""},
			{name: "not-a-mime-type", contentType: "not-a-mime-type"},
			{name: "malformed-param-html", contentType: "text/html; bad=;"},
			{name: "malformed-param-png", contentType: "image/png; bad=;"},
			{name: "crlf-injection", contentType: "image/png\r\nX-Injected: 1"},
		} {
			inserted, insErr := st.InsertAttachment(t.Context(), store.InsertAttachmentParams{
				MessagePK:   msgRow.PK,
				ProjectID:   proj.ID,
				PartPath:    pngAtt.PartPath,
				Filename:    bad.name + ".bin",
				ContentType: bad.contentType,
				SizeBytes:   pngAtt.SizeBytes,
				Disposition: "attachment",
				SHA256:      pngAtt.SHA256,
			})
			if insErr != nil {
				t.Fatalf("InsertAttachment(%s): %v", bad.name, insErr)
			}
			path := "/api/v1/projects/hdrs/messages/" + msgID + "/attachments/" + strconv.FormatInt(inserted.ID, 10) + "?inline=1"
			rec := doReq(t, httpSrv.Handler(), http.MethodGet, path, nil, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s (%s) status = %d, want 200", path, bad.name, rec.Code)
			}
			assertHeader(t, rec.Header(), "Content-Type", "application/octet-stream")
			assertHeader(t, rec.Header(), "Content-Security-Policy", wantAttachmentCSP)
			assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
			assertHeaderAbsent(t, rec.Header(), "X-Injected")
			if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") {
				t.Errorf("%s (%s) Content-Disposition = %q, want prefix attachment;", path, bad.name, disp)
			}
		}

		emlRec := doReq(t, httpSrv.Handler(), http.MethodGet, "/api/v1/projects/hdrs/messages/"+msgID+"/eml", nil, nil, nil)
		if emlRec.Code != http.StatusOK {
			t.Fatalf("GET /eml status = %d, want 200", emlRec.Code)
		}
		assertHeader(t, emlRec.Header(), "Content-Type", "message/rfc822")
		assertHeader(t, emlRec.Header(), "Content-Security-Policy", wantAttachmentCSP)
		assertHeader(t, emlRec.Header(), "X-Content-Type-Options", "nosniff")
		assertHeader(t, emlRec.Header(), "Cache-Control", "private, no-store")
		if disp := emlRec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") {
			t.Errorf("/eml Content-Disposition = %q, want attachment", disp)
		}
	})

	t.Run("raw and html source route class", func(t *testing.T) {
		for _, suffix := range []string{"/raw", "/html"} {
			rec := doReq(t, httpSrv.Handler(), http.MethodGet, "/api/v1/projects/hdrs/messages/"+msgID+suffix, nil, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", suffix, rec.Code)
			}
			assertHeader(t, rec.Header(), "Content-Type", "text/plain; charset=utf-8")
			assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
			assertHeader(t, rec.Header(), "Content-Security-Policy", wantAttachmentCSP)
			assertHeader(t, rec.Header(), "Cache-Control", "private, no-store")
		}
	})

	t.Run("SSE route class", func(t *testing.T) {
		ts := httptest.NewServer(httpSrv.Handler())
		defer ts.Close()

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET /events: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /events status = %d, want 200", resp.StatusCode)
		}
		assertHeader(t, resp.Header, "Content-Type", "text/event-stream")
		assertHeader(t, resp.Header, "Cache-Control", "no-store")
		assertHeader(t, resp.Header, "X-Accel-Buffering", "no")
		assertHeader(t, resp.Header, "X-Content-Type-Options", "nosniff")
		assertHeader(t, resp.Header, "X-Frame-Options", "DENY")

		// Read initial retry directive then cancel so server exits cleanly.
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "retry:") {
			t.Fatalf("first SSE line = %q (err=%v), want retry:", line, err)
		}
		cancel()
	})
}

func assertHeader(t *testing.T, h http.Header, key, want string) {
	t.Helper()
	if got := h.Get(key); got != want {
		t.Errorf("header %s = %q, want %q", key, got, want)
	}
}

func assertHeaderAbsent(t *testing.T, h http.Header, key string) {
	t.Helper()
	if got := h.Get(key); got != "" {
		t.Errorf("header %s = %q, want absent", key, got)
	}
}

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

// FR-074, SPEC 12.2, ADR 0009: inline PDF preview endpoint validates coerced
// media type and byte-0 %PDF- signature, enforces ADR 0009 headers, honors
// MAILCUBBY_RENDER_INLINE_PDF kill-switch, and remains accessible to viewers
// even when viewer_hide_technical=true.
func TestRenderAttachmentPDFValidationAndKillSwitch(t *testing.T) {
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
		Slug:     "pdf-test",
		Name:     "PDF Test",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      fixed,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ing := ingest.New(st, blobs, clk, bus, nil)
	rawEML := "From: billing@example.test\r\n" +
		"To: client@example.test\r\n" +
		"Subject: PDF Validation Matrix\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"PDFB\"\r\n\r\n" +
		"--PDFB\r\n" +
		"Content-Type: text/plain; charset=\"utf-8\"\r\n\r\n" +
		"Attachments attached.\r\n" +
		"--PDFB\r\n" +
		"Content-Type: application/pdf; name=\"=?UTF-8?Q?r=C3=A9sum=C3=A9.pdf?=\"\r\n" +
		"Content-Disposition: attachment; filename=\"=?UTF-8?Q?r=C3=A9sum=C3=A9.pdf?=\"\r\n\r\n" +
		"%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n" +
		"--PDFB\r\n" +
		"Content-Type: application/pdf; name=\"forged-html.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"forged-html.pdf\"\r\n\r\n" +
		"<!doctype html><html><body><script>window.__pwned=1</script></body></html>\r\n" +
		"--PDFB\r\n" +
		"Content-Type: application/pdf; name=\"forged-svg.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"forged-svg.pdf\"\r\n\r\n" +
		"<svg xmlns=\"http://www.w3.org/2000/svg\" onload=\"alert(1)\"/>\r\n" +
		"--PDFB\r\n" +
		"Content-Type: application/pdf; name=\"polyglot-offset.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"polyglot-offset.pdf\"\r\n\r\n" +
		"<html><!-- %PDF-1.4 --></html>\r\n" +
		"--PDFB\r\n" +
		"Content-Type: application/pdf; name=\"leading-space.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"leading-space.pdf\"\r\n\r\n" +
		" %PDF-1.4\n%%EOF\n\r\n" +
		"--PDFB\r\n" +
		"Content-Type: application/pdf; name=\"truncated-magic.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"truncated-magic.pdf\"\r\n\r\n" +
		"%PDF\r\n" +
		"--PDFB\r\n" +
		"Content-Type: text/plain; name=\"notes.txt\"\r\n" +
		"Content-Disposition: attachment; filename=\"notes.txt\"\r\n\r\n" +
		"%PDF-1.4 looks like pdf but declared text/plain\r\n" +
		"--PDFB--\r\n"

	refs, err := ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "billing@example.test",
		RcptTo:   []string{"client@example.test"},
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte(rawEML)))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	msgID := refs[0].ID.String()

	cfgOn, err := config.Load(nil)
	if err != nil {
		t.Fatalf("config.Load(on): %v", err)
	}
	srvOn := api.New(cfgOn, st, blobs, bus, api.Options{Clock: clk, Ingestor: ing})
	t.Cleanup(srvOn.Close)

	cfgOff, err := config.Load(func(k string) (string, bool) {
		if k == "MAILCUBBY_RENDER_INLINE_PDF" {
			return "false", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config.Load(off): %v", err)
	}
	srvOff := api.New(cfgOff, st, blobs, bus, api.Options{Clock: clk, Ingestor: ing})
	t.Cleanup(srvOff.Close)

	// Verify GET /api/v1/meta exposes features.inline_pdf accurately.
	checkMetaInlinePDF := func(srv *api.Server, want bool) {
		t.Helper()
		rec := doReq(t, srv.Handler(), http.MethodGet, "/api/v1/meta", nil, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/meta status = %d, want 200", rec.Code)
		}
		var meta struct {
			Features struct {
				InlinePDF bool `json:"inline_pdf"`
			} `json:"features"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
			t.Fatalf("unmarshal meta: %v", err)
		}
		if meta.Features.InlinePDF != want {
			t.Errorf("meta.features.inline_pdf = %v, want %v", meta.Features.InlinePDF, want)
		}
	}
	checkMetaInlinePDF(srvOn, true)
	checkMetaInlinePDF(srvOff, false)

	// Collect attachment IDs from message detail.
	detRec := doReq(t, srvOn.Handler(), http.MethodGet, "/api/v1/projects/pdf-test/messages/"+msgID, nil, nil, nil)
	if detRec.Code != http.StatusOK {
		t.Fatalf("GET message detail status = %d, want 200", detRec.Code)
	}
	var detail struct {
		Attachments []struct {
			ID       int64  `json:"id"`
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(detRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	attByName := make(map[string]int64, len(detail.Attachments))
	for _, a := range detail.Attachments {
		attByName[a.Filename] = a.ID
	}

	t.Run("valid PDF with Unicode filename renders inline with ADR 0009 headers", func(t *testing.T) {
		id, ok := attByName["résumé.pdf"]
		if !ok {
			t.Fatalf("résumé.pdf missing from attachments: %+v", attByName)
		}
		path := "/render/pdf-test/" + msgID + "/attachments/" + strconv.FormatInt(id, 10)
		rec := doReq(t, srvOn.Handler(), http.MethodGet, path, nil, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		assertHeader(t, rec.Header(), "Content-Type", "application/pdf")
		assertHeader(t, rec.Header(), "Content-Disposition", `inline; filename="r_sum_.pdf"; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`)
		assertHeader(t, rec.Header(), "Content-Security-Policy", wantRenderPDFCSP)
		assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
		assertHeader(t, rec.Header(), "Referrer-Policy", "no-referrer")
		assertHeader(t, rec.Header(), "Cache-Control", "private, no-store")
		assertHeader(t, rec.Header(), "Cross-Origin-Resource-Policy", "same-origin")
		assertHeader(t, rec.Header(), "X-DNS-Prefetch-Control", "off")
		assertHeaderAbsent(t, rec.Header(), "X-Frame-Options")
		assertHeaderAbsent(t, rec.Header(), "Permissions-Policy")
		if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-1.4")) {
			t.Errorf("unexpected PDF body prefix: %q", rec.Body.Bytes())
		}

		// Download endpoint remains unchanged (attachment disposition, sandbox CSP, X-Frame-Options: DENY).
		dlPath := "/api/v1/projects/pdf-test/messages/" + msgID + "/attachments/" + strconv.FormatInt(id, 10) + "?inline=1"
		dlRec := doReq(t, srvOn.Handler(), http.MethodGet, dlPath, nil, nil, nil)
		if dlRec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", dlPath, dlRec.Code)
		}
		assertHeader(t, dlRec.Header(), "Content-Security-Policy", wantAttachmentCSP)
		assertHeader(t, dlRec.Header(), "X-Frame-Options", "DENY")
		if disp := dlRec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") {
			t.Errorf("download Content-Disposition = %q, want attachment;", disp)
		}
	})

	t.Run("forged and non-PDF attachments return 415 unsupported_media_type", func(t *testing.T) {
		for _, name := range []string{
			"forged-html.pdf",
			"forged-svg.pdf",
			"polyglot-offset.pdf",
			"leading-space.pdf",
			"truncated-magic.pdf",
			"notes.txt",
		} {
			id, ok := attByName[name]
			if !ok {
				t.Fatalf("attachment %q missing: %+v", name, attByName)
			}
			path := "/render/pdf-test/" + msgID + "/attachments/" + strconv.FormatInt(id, 10)
			rec := doReq(t, srvOn.Handler(), http.MethodGet, path, nil, nil, nil)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("GET %s (%s) status = %d, want 415; body=%s", path, name, rec.Code, rec.Body.String())
				continue
			}
			var errEnv struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil {
				t.Errorf("unmarshal 415 error for %s: %v", name, err)
			} else if errEnv.Error.Code != "unsupported_media_type" {
				t.Errorf("%s error.code = %q, want unsupported_media_type", name, errEnv.Error.Code)
			}
			// Error responses keep app-level framing/security headers.
			assertHeader(t, rec.Header(), "X-Frame-Options", "DENY")
			assertHeader(t, rec.Header(), "X-Content-Type-Options", "nosniff")
		}
	})

	t.Run("invalid and nonexistent attachment IDs return 400 and 404", func(t *testing.T) {
		for _, badID := range []string{"abc", "0", "-5"} {
			rec := doReq(t, srvOn.Handler(), http.MethodGet, "/render/pdf-test/"+msgID+"/attachments/"+badID, nil, nil, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("bad attachment_id %q status = %d, want 400", badID, rec.Code)
			}
		}
		recMissing := doReq(t, srvOn.Handler(), http.MethodGet, "/render/pdf-test/"+msgID+"/attachments/999999", nil, nil, nil)
		if recMissing.Code != http.StatusNotFound {
			t.Errorf("missing attachment_id status = %d, want 404", recMissing.Code)
		}
	})

	t.Run("kill-switch disables inline PDF endpoint with 404", func(t *testing.T) {
		id := attByName["résumé.pdf"]
		path := "/render/pdf-test/" + msgID + "/attachments/" + strconv.FormatInt(id, 10)
		rec := doReq(t, srvOff.Handler(), http.MethodGet, path, nil, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s with MAILCUBBY_RENDER_INLINE_PDF=false status = %d, want 404", path, rec.Code)
		}
	})

	t.Run("hostile fixture 13-forged-pdf-html.eml rejects both forged PDFs with 415", func(t *testing.T) {
		const fixturePath = "../../testdata/hostile/13-forged-pdf-html.eml"
		rawFixture, err := os.ReadFile(filepath.Clean(fixturePath))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", fixturePath, err)
		}
		hRefs, err := ing.Ingest(t.Context(), ingest.Envelope{
			MailFrom: "attacker@evil.example",
			RcptTo:   []string{"victim@app.test"},
		}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader(rawFixture))
		if err != nil {
			t.Fatalf("Ingest hostile PDF fixture: %v", err)
		}
		hMsgID := hRefs[0].ID.String()
		hDet := doReq(t, srvOn.Handler(), http.MethodGet, "/api/v1/projects/pdf-test/messages/"+hMsgID, nil, nil, nil)
		if hDet.Code != http.StatusOK {
			t.Fatalf("GET hostile detail status = %d, want 200", hDet.Code)
		}
		var hd struct {
			Attachments []struct {
				ID       int64  `json:"id"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(hDet.Body.Bytes(), &hd); err != nil {
			t.Fatalf("unmarshal hostile detail: %v", err)
		}
		if len(hd.Attachments) != 2 {
			t.Fatalf("expected 2 forged attachments in 13-forged-pdf-html.eml, got %d", len(hd.Attachments))
		}
		for _, a := range hd.Attachments {
			p := "/render/pdf-test/" + hMsgID + "/attachments/" + strconv.FormatInt(a.ID, 10)
			rec := doReq(t, srvOn.Handler(), http.MethodGet, p, nil, nil, nil)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("hostile forged PDF %q GET %s status = %d, want 415", a.Filename, p, rec.Code)
			}
		}
	})
}

// FR-050, FR-074, ADR 0009: a viewer with viewer_hide_technical=true can still
// render PDF attachments inline while remaining blocked from technical endpoints.
func TestRenderAttachmentPDFViewerHideTechnical(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner-pdf@example.test", "Owner", pass, false, true, false)
	viewer := env.createUser(t, "viewer-pdf@example.test", "Viewer", pass, false, false, false)

	settings := store.DefaultProjectSettings()
	settings.ViewerHideTechnical = true
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "client-pdf",
		Name:     "Client PDF",
		Color:    "teal",
		Settings: settings,
		Now:      env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(owner): %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, viewer.ID, "viewer", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(viewer): %v", err)
	}

	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
	viewerCookie, _ := loginAndGetCSRF(t, h, viewer.Email, pass)

	sentRec := doReq(t, h, http.MethodPost, "/api/v1/projects/client-pdf/send-test", nil, ownerCookie, csrfHeaders(ownerCSRF))
	if sentRec.Code != http.StatusCreated {
		t.Fatalf("send-test status = %d, want 201", sentRec.Code)
	}
	var sent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(sentRec.Body.Bytes(), &sent); err != nil {
		t.Fatalf("unmarshal send-test: %v", err)
	}

	detRec := doReq(t, h, http.MethodGet, "/api/v1/projects/client-pdf/messages/"+sent.ID, nil, viewerCookie, nil)
	if detRec.Code != http.StatusOK {
		t.Fatalf("viewer GET detail status = %d, want 200", detRec.Code)
	}
	var det struct {
		Attachments []struct {
			ID          int64  `json:"id"`
			ContentType string `json:"content_type"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(detRec.Body.Bytes(), &det); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	var pdfID int64
	for _, a := range det.Attachments {
		if strings.HasPrefix(strings.ToLower(a.ContentType), "application/pdf") {
			pdfID = a.ID
			break
		}
	}
	if pdfID == 0 {
		t.Fatalf("sample message missing PDF attachment: %+v", det.Attachments)
	}

	// Viewer with viewer_hide_technical=true is blocked from /raw (403)...
	rawRec := doReq(t, h, http.MethodGet, "/api/v1/projects/client-pdf/messages/"+sent.ID+"/raw", nil, viewerCookie, nil)
	if rawRec.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /raw status = %d, want 403", rawRec.Code)
	}

	// ...but can render the PDF inline (200) with ADR 0009 headers.
	pdfPath := "/render/client-pdf/" + sent.ID + "/attachments/" + strconv.FormatInt(pdfID, 10)
	pdfRec := doReq(t, h, http.MethodGet, pdfPath, nil, viewerCookie, nil)
	if pdfRec.Code != http.StatusOK {
		t.Fatalf("viewer GET %s status = %d, want 200; body=%s", pdfPath, pdfRec.Code, pdfRec.Body.String())
	}
	assertHeader(t, pdfRec.Header(), "Content-Type", "application/pdf")
	assertHeader(t, pdfRec.Header(), "Content-Security-Policy", wantRenderPDFCSP)
	assertHeaderAbsent(t, pdfRec.Header(), "X-Frame-Options")
}

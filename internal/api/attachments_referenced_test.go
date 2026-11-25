package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
)

// FR-072: message detail flags attachments that /render inlines via cid:, for
// viewers too when viewer_hide_technical is on, and the flag matches exactly
// what the render endpoint rewrites.
func TestMessageDetailReferencedByHTML(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	viewer := env.createUser(t, "viewer@example.test", "Viewer", pass, false, false, false)
	// DefaultProjectSettings has ViewerHideTechnical = true.
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "strip", Name: "Strip", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, viewer.ID, "viewer", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	viewerCookie, _ := loginAndGetCSRF(t, h, viewer.Email, pass)

	parts := map[string][]byte{
		"logo.png":    []byte("logo-image-bytes-referenced-with-other-case"),
		"spare.png":   []byte("spare-inline-image-bytes-not-referenced"),
		"invoice.pdf": []byte("%PDF-1.4 invoice-bytes"),
	}
	b64 := func(name string) string { return base64.StdEncoding.EncodeToString(parts[name]) }

	raw := strings.Join([]string{
		"From: App <app@example.test>",
		"To: User <user@example.test>",
		"Subject: Referenced parts",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		`Content-Type: multipart/related; boundary="rel"`,
		"",
		"--rel",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<html><body><img src="cid:logo%40example.test"></body></html>`,
		"--rel",
		`Content-Type: image/png; name="logo.png"`,
		"Content-Transfer-Encoding: base64",
		`Content-Disposition: inline; filename="logo.png"`,
		"Content-ID: <Logo@Example.TEST>",
		"",
		b64("logo.png"),
		"--rel",
		`Content-Type: image/png; name="spare.png"`,
		"Content-Transfer-Encoding: base64",
		`Content-Disposition: inline; filename="spare.png"`,
		"Content-ID: <spare@example.test>",
		"",
		b64("spare.png"),
		"--rel--",
		"--mix",
		"Content-Type: application/pdf",
		"Content-Transfer-Encoding: base64",
		`Content-Disposition: attachment; filename="invoice.pdf"`,
		"",
		b64("invoice.pdf"),
		"--mix--",
		"",
	}, "\r\n")

	refs, err := env.ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "app@example.test",
		RcptTo:   []string{"user@example.test"},
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	msgID := refs[0].ID.String()

	rec := doReq(t, h, http.MethodGet, "/api/v1/projects/strip/messages/"+msgID, nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET detail status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Envelope    *json.RawMessage `json:"envelope"`
		Attachments []struct {
			Filename         string `json:"filename"`
			ReferencedByHTML *bool  `json:"referenced_by_html"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.Envelope != nil {
		t.Fatalf("precondition: viewer_hide_technical must be on (envelope omitted)")
	}
	if len(detail.Attachments) != len(parts) {
		t.Fatalf("attachments = %d, want %d", len(detail.Attachments), len(parts))
	}

	rec = doReq(t, h, http.MethodGet, "/render/strip/"+msgID, nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET /render status = %d, want 200", rec.Code)
	}
	rendered := rec.Body.String()

	want := map[string]bool{"logo.png": true, "spare.png": false, "invoice.pdf": false}
	for _, a := range detail.Attachments {
		if a.ReferencedByHTML == nil {
			t.Fatalf("%s: referenced_by_html missing from viewer detail", a.Filename)
		}
		inlined := strings.Contains(rendered, b64(a.Filename))
		if *a.ReferencedByHTML != inlined {
			t.Errorf("%s: referenced_by_html = %v, but /render inlined it = %v", a.Filename, *a.ReferencedByHTML, inlined)
		}
		if *a.ReferencedByHTML != want[a.Filename] {
			t.Errorf("%s: referenced_by_html = %v, want %v", a.Filename, *a.ReferencedByHTML, want[a.Filename])
		}
	}
}

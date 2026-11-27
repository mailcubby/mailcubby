package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/testutil"
)

// minimalPNG is a valid 1x1 transparent PNG.
var minimalPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

var minimalPDF = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")

type httpResult struct {
	status int
	header http.Header
	body   []byte
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) httpResult {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, r)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response %s %s: %v", method, url, err)
	}
	return httpResult{
		status: resp.StatusCode,
		header: resp.Header.Clone(),
		body:   data,
	}
}

func TestIntegrationMessagesListPaginationDetailAndViews(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// Send 3 messages via SMTP (FR-070, FR-071, FR-074).
	raws := make([][]byte, 3)
	for i := range 3 {
		srv.Clock.Advance(time.Second)
		raw := []byte(fmt.Sprintf(
			"From: Alice <alice@example.test>\r\n"+
				"To: Bob <bob@example.test>\r\n"+
				"Cc: Carol <carol@example.test>\r\n"+
				"Reply-To: reply@example.test\r\n"+
				"Message-ID: <msg-%d@example.test>\r\n"+
				"Date: Mon, 23 Nov 2026 14:00:0%d +0000\r\n"+
				"Subject: Order #%d\r\n"+
				"MIME-Version: 1.0\r\n"+
				"Content-Type: multipart/alternative; boundary=\"alt\"\r\n"+
				"\r\n"+
				"--alt\r\n"+
				"Content-Type: text/plain; charset=utf-8\r\n"+
				"\r\n"+
				"Plain text body %d\r\n"+
				"--alt\r\n"+
				"Content-Type: text/html; charset=utf-8\r\n"+
				"\r\n"+
				"<p>HTML body %d</p>\r\n"+
				"--alt--\r\n",
			i+1, i+1, i+1, i+1, i+1,
		))
		raws[i] = raw
		testutil.SendSMTP(t, srv.SMTPAddr, "alice@example.test", []string{"bob@example.test", "bcc@example.test"}, raw)
	}

	// Page 1 with limit=2.
	res := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?limit=2", nil)
	if res.status != http.StatusOK {
		t.Fatalf("list page 1 status = %d, body = %s", res.status, string(res.body))
	}
	assertSpec123Headers(t, res.header, false)

	var page1 struct {
		Items []struct {
			ID         string `json:"id"`
			ReceivedAt string `json:"received_at"`
			Subject    string `json:"subject"`
			Snippet    string `json:"snippet"`
			HasHTML    bool   `json:"has_html"`
			HasText    bool   `json:"has_text"`
			Seen       bool   `json:"seen"`
			From       struct {
				Name    string `json:"name"`
				Address string `json:"address"`
			} `json:"from"`
			Tags []string `json:"tags"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(res.body, &page1); err != nil {
		t.Fatalf("unmarshal page1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1.Items))
	}
	if page1.Items[0].Subject != "Order #3" || page1.Items[1].Subject != "Order #2" {
		t.Fatalf("expected newest first (Order #3, Order #2), got (%q, %q)", page1.Items[0].Subject, page1.Items[1].Subject)
	}
	if page1.Items[0].From.Name != "Alice" || page1.Items[0].From.Address != "alice@example.test" {
		t.Fatalf("unexpected from: %+v", page1.Items[0].From)
	}
	if !page1.Items[0].HasHTML || !page1.Items[0].HasText || page1.Items[0].Seen {
		t.Fatalf("unexpected flags: %+v", page1.Items[0])
	}
	if page1.Items[0].Tags == nil {
		t.Fatalf("tags should be empty array, not null")
	}
	if !strings.HasSuffix(page1.Items[0].ReceivedAt, "Z") || !strings.Contains(page1.Items[0].ReceivedAt, ".") {
		t.Errorf("received_at = %q, want RFC 3339 UTC with milliseconds", page1.Items[0].ReceivedAt)
	}
	if page1.NextCursor == nil || *page1.NextCursor == "" {
		t.Fatalf("expected non-empty next_cursor on page 1")
	}

	// Page 2 using next_cursor.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?limit=2&cursor="+*page1.NextCursor, nil)
	if res.status != http.StatusOK {
		t.Fatalf("list page 2 status = %d, body = %s", res.status, string(res.body))
	}
	var page2 struct {
		Items []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(res.body, &page2); err != nil {
		t.Fatalf("unmarshal page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].Subject != "Order #1" {
		t.Fatalf("unexpected page2 items: %+v", page2.Items)
	}
	if page2.NextCursor != nil {
		t.Fatalf("expected nil next_cursor on final page, got %q", *page2.NextCursor)
	}

	// Invalid cursor and limit -> 400.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?cursor=not-a-valid-cursor", nil)
	if res.status != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d, want 400", res.status)
	}
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?limit=999", nil)
	if res.status != http.StatusBadRequest {
		t.Fatalf("limit=999 status = %d, want 400", res.status)
	}

	// Detail endpoint (FR-071) for newest message (Order #3).
	newestID := page1.Items[0].ID
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+newestID, nil)
	if res.status != http.StatusOK {
		t.Fatalf("get detail status = %d, body = %s", res.status, string(res.body))
	}
	var detail struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
		Bcc     []struct {
			Address string `json:"address"`
		} `json:"bcc"`
		RenderURL *string `json:"render_url"`
		Envelope  *struct {
			MailFrom string   `json:"mail_from"`
			RcptTo   []string `json:"rcpt_to"`
		} `json:"envelope"`
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	}
	if err := json.Unmarshal(res.body, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if !strings.Contains(detail.Text, "Plain text body 3") {
		t.Errorf("detail.text = %q, want to contain 'Plain text body 3'", detail.Text)
	}
	if detail.RenderURL == nil || *detail.RenderURL != "/render/default/"+newestID {
		t.Errorf("render_url = %v, want /render/default/%s", detail.RenderURL, newestID)
	}
	if len(detail.Bcc) != 1 || detail.Bcc[0].Address != "bcc@example.test" {
		t.Errorf("unexpected bcc: %+v", detail.Bcc)
	}
	if detail.Envelope == nil || detail.Envelope.MailFrom != "alice@example.test" || len(detail.Envelope.RcptTo) != 2 {
		t.Errorf("unexpected envelope: %+v", detail.Envelope)
	}
	if len(detail.Headers) == 0 || detail.Headers[0].Name != "From" {
		t.Errorf("unexpected headers: %+v", detail.Headers)
	}

	// Raw, EML, HTML, Headers endpoints.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+newestID+"/raw", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /raw status = %d", res.status)
	}
	if res.header.Get("Content-Type") != "text/plain; charset=utf-8" || res.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("unexpected /raw headers: ct=%q nosniff=%q", res.header.Get("Content-Type"), res.header.Get("X-Content-Type-Options"))
	}
	if sha256.Sum256(res.body) != sha256.Sum256(raws[2]) {
		t.Errorf("/raw SHA-256 mismatch with sent bytes")
	}

	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+newestID+"/eml", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /eml status = %d", res.status)
	}
	if res.header.Get("Content-Type") != "message/rfc822" {
		t.Errorf("/eml Content-Type = %q, want message/rfc822", res.header.Get("Content-Type"))
	}
	if !strings.HasPrefix(res.header.Get("Content-Disposition"), "attachment;") || !strings.Contains(res.header.Get("Content-Disposition"), ".eml") {
		t.Errorf("/eml Content-Disposition = %q", res.header.Get("Content-Disposition"))
	}
	if sha256.Sum256(res.body) != sha256.Sum256(raws[2]) {
		t.Errorf("/eml SHA-256 mismatch with sent bytes")
	}

	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+newestID+"/html", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /html status = %d", res.status)
	}
	if res.header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("/html Content-Type = %q", res.header.Get("Content-Type"))
	}
	if !strings.Contains(string(res.body), "<p>HTML body 3</p>") {
		t.Errorf("/html body = %q", string(res.body))
	}

	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+newestID+"/headers", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /headers status = %d", res.status)
	}
	var hdrs []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(res.body, &hdrs); err != nil {
		t.Fatalf("unmarshal headers: %v", err)
	}
	if len(hdrs) < 6 || hdrs[0].Name != "From" || hdrs[1].Name != "To" {
		t.Errorf("headers not in original order: %+v", hdrs)
	}
}

func TestIntegrationAttachmentSecurityHeadersAndInlinePolicy(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// Send one message with active/XML types, raster image, and PDF attachments (SPEC 12.2, FR-074).
	raw := []byte(strings.Join([]string{
		"From: sender@example.test",
		"To: rcpt@example.test",
		"Subject: Attachment security test",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="BOUND"`,
		"",
		"--BOUND",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"See attachments.",
		"--BOUND",
		`Content-Type: text/html; charset=utf-8; name="x.html"`,
		`Content-Disposition: attachment; filename="x.html"`,
		"",
		"<html><script>alert(1)</script></html>",
		"--BOUND",
		`Content-Type: image/png; name="pixel.png"`,
		`Content-Disposition: attachment; filename="pixel.png"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString(minimalPNG),
		"--BOUND",
		`Content-Type: application/pdf; name="doc.pdf"`,
		`Content-Disposition: attachment; filename="doc.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString(minimalPDF),
		"--BOUND",
		`Content-Type: image/svg+xml; name="icon.svg"`,
		`Content-Disposition: attachment; filename="icon.svg"`,
		"",
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"--BOUND",
		`Content-Type: application/atom+xml; name="feed.atom"`,
		`Content-Disposition: attachment; filename="feed.atom"`,
		"",
		`<feed xmlns="http://www.w3.org/2005/Atom"/>`,
		"--BOUND",
		`Content-Type: application/rss+xml; name="feed.rss"`,
		`Content-Disposition: attachment; filename="feed.rss"`,
		"",
		`<rss version="2.0"/>`,
		"--BOUND",
		`Content-Type: foo/xml; name="custom.xml"`,
		`Content-Disposition: attachment; filename="custom.xml"`,
		"",
		`<custom/>`,
		"--BOUND--",
		"",
	}, "\r\n"))

	testutil.SendSMTP(t, srv.SMTPAddr, "sender@example.test", []string{"rcpt@example.test"}, raw)

	listRes := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages", nil)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRes.body, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("expected 1 message, got %s", string(listRes.body))
	}
	msgID := list.Items[0].ID

	detailRes := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+msgID, nil)
	var detail struct {
		Attachments []struct {
			ID       int64  `json:"id"`
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(detailRes.body, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	byName := make(map[string]int64, len(detail.Attachments))
	for _, a := range detail.Attachments {
		byName[a.Filename] = a.ID
	}

	cases := []struct {
		name           string
		filename       string
		query          string
		wantCT         string
		wantDispPrefix string
		wantBody       []byte
	}{
		{
			name:           "x.html served as octet-stream attachment",
			filename:       "x.html",
			query:          "",
			wantCT:         "application/octet-stream",
			wantDispPrefix: `attachment; filename="x.html"`,
			wantBody:       []byte("<html><script>alert(1)</script></html>"),
		},
		{
			name:           "x.html ignores inline=1",
			filename:       "x.html",
			query:          "?inline=1",
			wantCT:         "application/octet-stream",
			wantDispPrefix: `attachment; filename="x.html"`,
		},
		{
			name:           "pixel.png default attachment",
			filename:       "pixel.png",
			query:          "",
			wantCT:         "image/png",
			wantDispPrefix: `attachment; filename="pixel.png"`,
			wantBody:       minimalPNG,
		},
		{
			name:           "pixel.png with inline=1 is inline",
			filename:       "pixel.png",
			query:          "?inline=1",
			wantCT:         "image/png",
			wantDispPrefix: `inline; filename="pixel.png"`,
			wantBody:       minimalPNG,
		},
		{
			name:           "doc.pdf with inline=1 stays attachment",
			filename:       "doc.pdf",
			query:          "?inline=1",
			wantCT:         "application/pdf",
			wantDispPrefix: `attachment; filename="doc.pdf"`,
			wantBody:       minimalPDF,
		},
		{
			name:           "icon.svg downgraded to octet-stream attachment",
			filename:       "icon.svg",
			query:          "?inline=1",
			wantCT:         "application/octet-stream",
			wantDispPrefix: `attachment; filename="icon.svg"`,
		},
		{
			name:           "feed.atom (+xml) downgraded to octet-stream attachment",
			filename:       "feed.atom",
			query:          "?inline=1",
			wantCT:         "application/octet-stream",
			wantDispPrefix: `attachment; filename="feed.atom"`,
		},
		{
			name:           "feed.rss (+xml) downgraded to octet-stream attachment",
			filename:       "feed.rss",
			query:          "?inline=1",
			wantCT:         "application/octet-stream",
			wantDispPrefix: `attachment; filename="feed.rss"`,
		},
		{
			name:           "custom.xml (*/xml) downgraded to octet-stream attachment",
			filename:       "custom.xml",
			query:          "?inline=1",
			wantCT:         "application/octet-stream",
			wantDispPrefix: `attachment; filename="custom.xml"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := byName[tc.filename]
			if !ok {
				t.Fatalf("attachment %q missing from detail: %+v", tc.filename, byName)
			}
			url := fmt.Sprintf("%s/api/v1/projects/default/messages/%s/attachments/%d%s", base, msgID, id, tc.query)
			res := doJSON(t, client, http.MethodGet, url, nil)
			if res.status != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.status, string(res.body))
			}
			if got := res.header.Get("Content-Type"); got != tc.wantCT {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantCT)
			}
			if got := res.header.Get("Content-Disposition"); !strings.HasPrefix(got, tc.wantDispPrefix) {
				t.Errorf("Content-Disposition = %q, want prefix %q", got, tc.wantDispPrefix)
			}
			if got := res.header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := res.header.Get("Content-Security-Policy"); got != "sandbox; default-src 'none'" {
				t.Errorf("Content-Security-Policy = %q, want sandbox; default-src 'none'", got)
			}
			if tc.wantBody != nil && !bytes.Equal(res.body, tc.wantBody) {
				t.Errorf("body mismatch: got %d bytes, want %d bytes", len(res.body), len(tc.wantBody))
			}
		})
	}
}

func TestIntegrationMetaAuthMeProjectsCRUDAndIsolation(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// GET /meta
	res := doJSON(t, client, http.MethodGet, base+"/api/v1/meta", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /meta status = %d", res.status)
	}
	assertSpec123Headers(t, res.header, false)
	var meta struct {
		Mode          string `json:"mode"`
		Version       string `json:"version"`
		InstanceName  string `json:"instance_name"`
		SetupRequired bool   `json:"setup_required"`
		SMTP          struct {
			Host    string `json:"host"`
			Port    int    `json:"port"`
			TLSMode string `json:"tls_mode"`
		} `json:"smtp"`
	}
	if err := json.Unmarshal(res.body, &meta); err != nil {
		t.Fatalf("unmarshal /meta: %v", err)
	}
	// FR-063: local mode announces the bound SMTP listener port.
	wantSMTPPort, err := strconv.Atoi(srv.SMTPAddr[strings.LastIndex(srv.SMTPAddr, ":")+1:])
	if err != nil {
		t.Fatalf("parse SMTP port from %q: %v", srv.SMTPAddr, err)
	}
	if meta.Mode != "local" || meta.SetupRequired || meta.SMTP.Host != "localhost" || meta.SMTP.Port != wantSMTPPort || meta.SMTP.TLSMode != "none" {
		t.Fatalf("unexpected /meta: %+v (want smtp.port %d)", meta, wantSMTPPort)
	}

	// GET /auth/me
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/auth/me", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d", res.status)
	}
	var me struct {
		User struct {
			IsAdmin bool `json:"is_admin"`
		} `json:"user"`
		Memberships []struct {
			Project string `json:"project"`
			Role    string `json:"role"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(res.body, &me); err != nil {
		t.Fatalf("unmarshal /auth/me: %v", err)
	}
	if !me.User.IsAdmin || len(me.Memberships) != 1 || me.Memberships[0].Project != "default" || me.Memberships[0].Role != "owner" {
		t.Fatalf("unexpected /auth/me: %+v", me)
	}

	// POST /projects (auto slugify, FR-030, FR-031)
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects", map[string]any{
		"name":  "Shop Staging",
		"color": "blue",
	})
	if res.status != http.StatusCreated {
		t.Fatalf("POST /projects status = %d, body = %s", res.status, string(res.body))
	}
	var created struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(res.body, &created); err != nil {
		t.Fatalf("unmarshal created project: %v", err)
	}
	if created.Slug != "shop-staging" || created.Color != "blue" {
		t.Fatalf("unexpected created project: %+v", created)
	}

	// Duplicate slug -> 409 conflict.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects", map[string]any{
		"name": "Duplicate",
		"slug": "shop-staging",
	})
	if res.status != http.StatusConflict {
		t.Fatalf("duplicate slug status = %d, want 409", res.status)
	}

	// Invalid color and reserved slug -> 422.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects", map[string]any{
		"name":  "Bad",
		"slug":  "admin",
		"color": "neon-green",
	})
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create status = %d, want 422", res.status)
	}

	// PATCH /projects/{project}: rename slug (FR-032) and set then clear retention override.
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/shop-staging", map[string]any{
		"slug": "shop-qa",
		"settings": map[string]any{
			"retention_max_messages": 250,
		},
	})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH project status = %d, body = %s", res.status, string(res.body))
	}
	var patched struct {
		Slug     string `json:"slug"`
		Settings struct {
			RetentionMaxMessages *int64 `json:"retention_max_messages"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(res.body, &patched); err != nil {
		t.Fatalf("unmarshal patched project: %v", err)
	}
	if patched.Slug != "shop-qa" || patched.Settings.RetentionMaxMessages == nil || *patched.Settings.RetentionMaxMessages != 250 {
		t.Fatalf("unexpected patched project: %+v", patched)
	}

	// Clear retention_max_messages back to null.
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/shop-qa", map[string]any{
		"settings": map[string]any{
			"retention_max_messages": nil,
		},
	})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH clear null status = %d, body = %s", res.status, string(res.body))
	}
	if err := json.Unmarshal(res.body, &patched); err != nil {
		t.Fatalf("unmarshal cleared project: %v", err)
	}
	if patched.Settings.RetentionMaxMessages != nil {
		t.Fatalf("expected retention_max_messages to reset to nil, got %d", *patched.Settings.RetentionMaxMessages)
	}

	// Send one message to shop-qa, verify sparkline updates and cross-project 404.
	_ = doJSON(t, client, http.MethodGet, base+"/api/v1/projects", nil) // Prime sparkline cache.
	testutil.SendSMTPPlain(t, srv.SMTPAddr, "shop-qa", "dev", "a@example.test", []string{"b@example.test"}, []byte("Subject: QA\r\n\r\nhello\r\n"))

	listRes := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/shop-qa/messages", nil)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRes.body, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("expected 1 message in shop-qa, got %s", string(listRes.body))
	}
	shopMsgID := list.Items[0].ID

	// Tenant isolation: looking up shop-qa's message under default must return 404.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+shopMsgID, nil)
	if res.status != http.StatusNotFound {
		t.Fatalf("cross-project message get status = %d, want 404", res.status)
	}

	// Advance clock past 30s sparkline TTL to guarantee deterministic check regardless of event scheduling.
	srv.Clock.Advance(31 * time.Second)
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET /projects status = %d", res.status)
	}
	var projects []struct {
		Slug         string  `json:"slug"`
		MessageCount int64   `json:"message_count"`
		Sparkline    []int64 `json:"sparkline"`
	}
	if err := json.Unmarshal(res.body, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	foundQA := false
	for _, p := range projects {
		if p.Slug == "shop-qa" {
			foundQA = true
			if p.MessageCount != 1 || len(p.Sparkline) != 24 || p.Sparkline[23] != 1 {
				t.Errorf("unexpected shop-qa summary: count=%d spark=%v", p.MessageCount, p.Sparkline)
			}
		}
	}
	if !foundQA {
		t.Fatalf("shop-qa not found in GET /projects")
	}

	// DELETE /projects/{project}: wrong confirm_slug -> 422; right confirm_slug -> 204,
	// and OrphanGC removes the deleted project's blob directory asynchronously (FR-033, FR-098).
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/shop-qa", map[string]string{"confirm_slug": "wrong"})
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("delete wrong confirm_slug status = %d, want 422", res.status)
	}

	projBlobDir := filepath.Join(srv.App.Config().DataDir, "blobs", strconv.FormatInt(created.ID, 10))
	if _, err := os.Stat(projBlobDir); err != nil {
		t.Fatalf("expected project blob dir %s to exist before delete: %v", projBlobDir, err)
	}

	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/shop-qa", map[string]string{"confirm_slug": "shop-qa"})
	if res.status != http.StatusNoContent {
		t.Fatalf("delete project status = %d, want 204", res.status)
	}
	if _, err := srv.App.OrphanGC().RunOnce(t.Context()); err != nil {
		t.Fatalf("OrphanGC.RunOnce: %v", err)
	}
	if _, err := os.Stat(projBlobDir); !os.IsNotExist(err) {
		t.Errorf("expected project blob dir %s to be removed by OrphanGC, stat err = %v", projBlobDir, err)
	}

	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/shop-qa", nil)
	if res.status != http.StatusNotFound {
		t.Fatalf("GET deleted project status = %d, want 404", res.status)
	}
}

func TestIntegrationHealthzAndReadyzDataDirUnwritable(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// FR-110: /healthz and /readyz return 200 when healthy.
	res := doJSON(t, client, http.MethodGet, base+"/healthz", nil)
	if res.status != http.StatusOK || strings.TrimSpace(string(res.body)) != "ok" {
		t.Fatalf("/healthz = %d %q, want 200 ok", res.status, string(res.body))
	}
	assertSpec123Headers(t, res.header, false)

	res = doJSON(t, client, http.MethodGet, base+"/readyz", nil)
	if res.status != http.StatusOK {
		t.Fatalf("/readyz = %d %s, want 200", res.status, string(res.body))
	}
	assertSpec123Headers(t, res.header, false)

	// Make data dir read-only -> /readyz must return 503 with JSON reason.
	dataDir := srv.App.Config().DataDir
	//nolint:gosec // G302: directory requires execute bit for traversal during test
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatalf("chmod 0500 dataDir: %v", err)
	}
	t.Cleanup(func() {
		//nolint:gosec // G302: restore directory mode so t.TempDir cleanup succeeds
		_ = os.Chmod(dataDir, 0o700)
	})

	res = doJSON(t, client, http.MethodGet, base+"/readyz", nil)
	if res.status != http.StatusServiceUnavailable {
		t.Fatalf("/readyz on read-only dataDir = %d %s, want 503", res.status, string(res.body))
	}
	var ready struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(res.body, &ready); err != nil {
		t.Fatalf("unmarshal 503 /readyz: %v", err)
	}
	if ready.Status != "unavailable" || !strings.Contains(ready.Reason, "data dir not writable") {
		t.Fatalf("unexpected 503 readyz payload: %+v", ready)
	}

	// Restore permissions -> /readyz recovers to 200.
	//nolint:gosec // G302: restore directory mode to 0700
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatalf("chmod 0700 dataDir: %v", err)
	}
	res = doJSON(t, client, http.MethodGet, base+"/readyz", nil)
	if res.status != http.StatusOK {
		t.Fatalf("/readyz after restore = %d %s, want 200", res.status, string(res.body))
	}
}

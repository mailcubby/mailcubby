package api_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

type sseFrame struct {
	event   string
	data    string
	comment string
	retry   string
}

func readSSEFrame(t testing.TB, r *bufio.Reader) (sseFrame, error) {
	t.Helper()
	const timeout = 2 * time.Second
	type result struct {
		frame sseFrame
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var f sseFrame
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if f.event != "" || f.data != "" || f.comment != "" || f.retry != "" {
					ch <- result{frame: f}
					return
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "event:"):
				f.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if f.data != "" {
					f.data += "\n" + d
				} else {
					f.data = d
				}
			case strings.HasPrefix(line, "retry:"):
				f.retry = strings.TrimSpace(strings.TrimPrefix(line, "retry:"))
			case strings.HasPrefix(line, ":"):
				f.comment = line
			}
		}
	}()

	select {
	case res := <-ch:
		return res.frame, res.err
	case <-time.After(timeout):
		return sseFrame{}, fmt.Errorf("timed out after %s waiting for SSE frame", timeout)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		runtime.Gosched()
	}
}

func TestIntegrationMutationsAndSendTest(t *testing.T) {
	t.Parallel()

	// FR-034, FR-075, FR-076, FR-077: send-test, seen PATCH, bulk ops, single & delete-all.
	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// POST /send-test creates a sample message with HTML, text, inline image, and PDF attachment (FR-077).
	res := doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/send-test", nil)
	if res.status != http.StatusCreated {
		t.Fatalf("POST /send-test status = %d, want 201, body = %s", res.status, string(res.body))
	}
	var sampleSummary struct {
		ID              string `json:"id"`
		Subject         string `json:"subject"`
		Seen            bool   `json:"seen"`
		HasHTML         bool   `json:"has_html"`
		HasText         bool   `json:"has_text"`
		AttachmentCount int64  `json:"attachment_count"`
	}
	if err := json.Unmarshal(res.body, &sampleSummary); err != nil {
		t.Fatalf("unmarshal send-test summary: %v", err)
	}
	if sampleSummary.ID == "" || !sampleSummary.HasHTML || !sampleSummary.HasText || sampleSummary.Seen {
		t.Fatalf("unexpected send-test summary: %+v", sampleSummary)
	}

	// Inspect detail of send-test message: HTML, text, inline PNG, PDF attachment, render_url, 0 remote resources.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+sampleSummary.ID, nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET detail status = %d, body = %s", res.status, string(res.body))
	}
	var sampleDetail struct {
		Text                string  `json:"text"`
		RenderURL           *string `json:"render_url"`
		RemoteResourceCount int     `json:"remote_resource_count"`
		Attachments         []struct {
			Filename    string  `json:"filename"`
			ContentType string  `json:"content_type"`
			Disposition string  `json:"disposition"`
			ContentID   *string `json:"content_id"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(res.body, &sampleDetail); err != nil {
		t.Fatalf("unmarshal send-test detail: %v", err)
	}
	if !strings.Contains(sampleDetail.Text, "Hello from Mailcubby") {
		t.Errorf("sample text = %q, expected greeting", sampleDetail.Text)
	}
	if sampleDetail.RenderURL == nil || *sampleDetail.RenderURL != "/render/default/"+sampleSummary.ID {
		t.Errorf("sample render_url = %v", sampleDetail.RenderURL)
	}
	if sampleDetail.RemoteResourceCount != 0 {
		t.Errorf("sample remote_resource_count = %d, want 0", sampleDetail.RemoteResourceCount)
	}
	hasInlineImage := false
	hasPDF := false
	for _, a := range sampleDetail.Attachments {
		if a.Disposition == "inline" && strings.HasPrefix(a.ContentType, "image/") {
			hasInlineImage = true
		}
		if a.ContentType == "application/pdf" && strings.HasSuffix(a.Filename, ".pdf") {
			hasPDF = true
		}
	}
	if !hasInlineImage || !hasPDF {
		t.Fatalf("send-test attachments missing inline image (%v) or PDF (%v): %+v", hasInlineImage, hasPDF, sampleDetail.Attachments)
	}

	// Create two more test messages for PATCH, bulk, and delete-all tests.
	srv.Clock.Advance(time.Second)
	res2 := doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/send-test", nil)
	srv.Clock.Advance(time.Second)
	res3 := doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/send-test", nil)
	var m2, m3 struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(res2.body, &m2)
	_ = json.Unmarshal(res3.body, &m3)

	// PATCH seen=true (FR-075).
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/default/messages/"+sampleSummary.ID, map[string]bool{"seen": true})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH seen status = %d, body = %s", res.status, string(res.body))
	}
	var patched struct {
		Seen bool `json:"seen"`
	}
	_ = json.Unmarshal(res.body, &patched)
	if !patched.Seen {
		t.Fatalf("expected seen=true after PATCH")
	}

	// Verify project counters reflect 3 messages, 2 unread.
	assertProjectCounts := func(wantCount, wantUnread int64) {
		t.Helper()
		pr := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default", nil)
		if pr.status != http.StatusOK {
			t.Fatalf("GET project status = %d", pr.status)
		}
		var proj struct {
			MessageCount int64 `json:"message_count"`
			UnreadCount  int64 `json:"unread_count"`
		}
		_ = json.Unmarshal(pr.body, &proj)
		if proj.MessageCount != wantCount || proj.UnreadCount != wantUnread {
			t.Fatalf("project counts = (%d, %d), want (%d, %d)", proj.MessageCount, proj.UnreadCount, wantCount, wantUnread)
		}
	}
	assertProjectCounts(3, 2)

	// Bulk validation errors (422).
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/messages/bulk", map[string]any{
		"ids":    []string{},
		"action": "bogus",
	})
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid bulk status = %d, want 422", res.status)
	}

	// Bulk mark seen (m1 already seen, m2 unseen -> affected = 1).
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/messages/bulk", map[string]any{
		"ids":    []string{sampleSummary.ID, m2.ID},
		"action": "seen",
	})
	if res.status != http.StatusOK {
		t.Fatalf("bulk seen status = %d, body = %s", res.status, string(res.body))
	}
	var bulkRes struct {
		Affected int64 `json:"affected"`
	}
	_ = json.Unmarshal(res.body, &bulkRes)
	if bulkRes.Affected != 1 {
		t.Fatalf("bulk seen affected = %d, want 1", bulkRes.Affected)
	}
	assertProjectCounts(3, 1)

	// Bulk mark unseen on m2 -> affected = 1, unread = 2.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/messages/bulk", map[string]any{
		"ids":    []string{m2.ID},
		"action": "unseen",
	})
	if res.status != http.StatusOK {
		t.Fatalf("bulk unseen status = %d", res.status)
	}
	assertProjectCounts(3, 2)

	// Single DELETE removes message row and blob (FR-076).
	storedMsg, err := srv.Store.GetMessageByID(t.Context(), 1, sampleSummary.ID)
	if err != nil {
		t.Fatalf("GetMessageByID before delete: %v", err)
	}
	blobFull := filepath.Join(srv.App.Config().DataDir, "blobs", storedMsg.BlobPath)
	if _, err := os.Stat(blobFull); err != nil {
		t.Fatalf("expected blob %s to exist before delete: %v", blobFull, err)
	}
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages/"+sampleSummary.ID, nil)
	if res.status != http.StatusNoContent {
		t.Fatalf("DELETE single status = %d, want 204", res.status)
	}
	if _, err := os.Stat(blobFull); !os.IsNotExist(err) {
		t.Errorf("expected blob %s to be deleted, stat err = %v", blobFull, err)
	}
	assertProjectCounts(2, 2)

	// Deleting again returns 404.
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages/"+sampleSummary.ID, nil)
	if res.status != http.StatusNotFound {
		t.Fatalf("second DELETE status = %d, want 404", res.status)
	}

	// Bulk delete m2.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/messages/bulk", map[string]any{
		"ids":    []string{m2.ID, "01JNONEXISTENT000000000000"},
		"action": "delete",
	})
	if res.status != http.StatusOK {
		t.Fatalf("bulk delete status = %d", res.status)
	}
	_ = json.Unmarshal(res.body, &bulkRes)
	if bulkRes.Affected != 1 {
		t.Fatalf("bulk delete affected = %d, want 1", bulkRes.Affected)
	}
	assertProjectCounts(1, 1)

	// DELETE /messages?query=before:invalid rejected with 400.
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages?query=before:invalid", nil)
	if res.status != http.StatusBadRequest {
		t.Fatalf("DELETE with invalid query status = %d, want 400", res.status)
	}

	// DELETE /messages deletes remaining m3 and resets counters.
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages", nil)
	if res.status != http.StatusOK {
		t.Fatalf("DELETE all status = %d, body = %s", res.status, string(res.body))
	}
	_ = json.Unmarshal(res.body, &bulkRes)
	if bulkRes.Affected != 1 {
		t.Fatalf("DELETE all affected = %d, want 1", bulkRes.Affected)
	}
	assertProjectCounts(0, 0)

	_ = m3
}

func TestIntegrationRenderEndpoint(t *testing.T) {
	t.Parallel()

	// FR-073, SPEC 12.1: exact render CSP, script inert in markup, meta refresh dropped, cid -> data URI, remote count.
	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	pngB64 := base64.StdEncoding.EncodeToString(minimalPNG)
	htmlBody := `<!doctype html><html><head>` +
		`<base href="https://evil.test/">` +
		`<meta http-equiv="refresh" content="0;url=https://evil.test/steal">` +
		`<link rel="dns-prefetch" href="https://tracker.test">` +
		`</head><body>` +
		`<script>window.__pwned = 1;</script>` +
		`<img src="cid:logo@mailcubby.test">` +
		`<img src="https://cdn.example.test/pixel.png">` +
		`</body></html>`

	rawHTMLMsg := []byte("From: Sender <sender@example.test>\r\n" +
		"To: Dev <dev@example.test>\r\n" +
		"Subject: Render test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=\"rel\"\r\n\r\n" +
		"--rel\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		htmlBody + "\r\n" +
		"--rel\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
		"Content-ID: <logo@mailcubby.test>\r\n\r\n" +
		pngB64 + "\r\n" +
		"--rel--\r\n")

	testutil.SendSMTP(t, srv.SMTPAddr, "sender@example.test", []string{"dev@example.test"}, rawHTMLMsg)

	// Send a plain-text-only message too.
	srv.Clock.Advance(time.Second)
	rawTextOnly := []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: Text only\r\n\r\nJust text\r\n")
	testutil.SendSMTP(t, srv.SMTPAddr, "a@example.test", []string{"b@example.test"}, rawTextOnly)

	listRes := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages", nil)
	var list struct {
		Items []struct {
			ID      string `json:"id"`
			HasHTML bool   `json:"has_html"`
		} `json:"items"`
	}
	_ = json.Unmarshal(listRes.body, &list)
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(list.Items))
	}
	textID := list.Items[0].ID
	htmlID := list.Items[1].ID

	// MessageDetail reports remote_resource_count == 1.
	detailRes := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/"+htmlID, nil)
	if detailRes.status != http.StatusOK {
		t.Fatalf("detail status = %d", detailRes.status)
	}
	var detail struct {
		RemoteResourceCount int `json:"remote_resource_count"`
	}
	_ = json.Unmarshal(detailRes.body, &detail)
	if detail.RemoteResourceCount != 1 {
		t.Fatalf("remote_resource_count = %d, want 1", detail.RemoteResourceCount)
	}

	// Render default (remote images blocked).
	renderRes := doJSON(t, client, http.MethodGet, base+"/render/default/"+htmlID, nil)
	if renderRes.status != http.StatusOK {
		t.Fatalf("GET /render status = %d, body = %s", renderRes.status, string(renderRes.body))
	}

	const wantBlockedCSP = "sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	if got := renderRes.header.Get("Content-Security-Policy"); got != wantBlockedCSP {
		t.Errorf("CSP = %q, want %q", got, wantBlockedCSP)
	}
	if strings.Contains(renderRes.header.Get("Content-Security-Policy"), "allow-scripts") {
		t.Errorf("CSP must never include allow-scripts")
	}
	if got := renderRes.header.Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options on /render = %q, want empty (frame-ancestors 'self' controls framing)", got)
	}
	if got := renderRes.header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := renderRes.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := renderRes.header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := renderRes.header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := renderRes.header.Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("Cross-Origin-Resource-Policy = %q", got)
	}
	if got := renderRes.header.Get("X-DNS-Prefetch-Control"); got != "off" {
		t.Errorf("X-DNS-Prefetch-Control = %q", got)
	}

	body := string(renderRes.body)
	if !strings.Contains(body, "<script>window.__pwned = 1;</script>") {
		t.Errorf("expected script tag preserved in markup (sandboxed by CSP), got: %s", body)
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("expected meta refresh removed, got: %s", body)
	}
	if strings.Contains(body, `https://evil.test/`) {
		t.Errorf("expected existing base href removed, got: %s", body)
	}
	if !strings.Contains(body, `<meta name="referrer" content="no-referrer"><base target="_blank">`) {
		t.Errorf("expected injected referrer meta and base target, got: %s", body)
	}
	wantDataURI := "data:image/png;base64," + pngB64
	if !strings.Contains(body, wantDataURI) {
		t.Errorf("expected cid: image rewritten to data URI %q, got: %s", wantDataURI, body)
	}

	// Render with ?images=1 (opt-in default allows https: images).
	imgRes := doJSON(t, client, http.MethodGet, base+"/render/default/"+htmlID+"?images=1", nil)
	if imgRes.status != http.StatusOK {
		t.Fatalf("GET /render?images=1 status = %d", imgRes.status)
	}
	const wantImagesCSP = "sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'; img-src data: cid: https:; style-src 'unsafe-inline'; font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	if got := imgRes.header.Get("Content-Security-Policy"); got != wantImagesCSP {
		t.Errorf("CSP with ?images=1 = %q, want %q", got, wantImagesCSP)
	}

	// Text-only message returns 404 on /render.
	noHTMLRes := doJSON(t, client, http.MethodGet, base+"/render/default/"+textID, nil)
	if noHTMLRes.status != http.StatusNotFound {
		t.Fatalf("GET /render for text-only message status = %d, want 404", noHTMLRes.status)
	}

	// Server configured with RenderRemoteImages=off ignores ?images=1.
	srvOff := testutil.StartServer(t, func(cfg *config.Config) {
		cfg.RenderRemoteImages = config.RenderRemoteImagesOff
	})
	offBase := "http://" + srvOff.HTTPAddr
	sendRes := doJSON(t, client, http.MethodPost, offBase+"/api/v1/projects/default/send-test", nil)
	var sent struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(sendRes.body, &sent)
	offRender := doJSON(t, client, http.MethodGet, offBase+"/render/default/"+sent.ID+"?images=1", nil)
	if got := offRender.header.Get("Content-Security-Policy"); got != wantBlockedCSP {
		t.Errorf("CSP with RenderRemoteImages=off and ?images=1 = %q, want %q", got, wantBlockedCSP)
	}
}

func TestIntegrationSSEMessageCreatedHeartbeatAndLimits(t *testing.T) {
	t.Parallel()

	// FR-090, FR-092: SSE message.created after SMTP send (2s timeout), 25s fake-clock heartbeat,
	// project filter, and per-user connection limit evicting oldest stream.
	srv := testutil.StartServer(t, func(cfg *config.Config) {
		cfg.SSEMaxConnectionsPerUser = 2
	})
	base := "http://" + srv.HTTPAddr

	openSSE := func(ctx context.Context, path string) (*http.Response, *bufio.Reader) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("NewRequest %s: %v", path, err)
		}
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			t.Fatalf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		return resp, bufio.NewReader(resp.Body)
	}

	ctx1, cancel1 := context.WithCancel(t.Context())
	defer cancel1()
	resp1, r1 := openSSE(ctx1, "/api/v1/events")
	defer func() { _ = resp1.Body.Close() }()

	if ct := resp1.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp1.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if xab := resp1.Header.Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", xab)
	}

	// Initial retry directive arrives immediately.
	f, err := readSSEFrame(t, r1)
	if err != nil || f.retry != "3000" {
		t.Fatalf("initial SSE frame = (%+v, %v), want retry: 3000", f, err)
	}

	// Send via SMTP -> receive message.created within 2s.
	raw := []byte("From: Alice <alice@example.test>\r\nTo: Bob <bob@example.test>\r\nSubject: Live SSE\r\n\r\nHello SSE!\r\n")
	testutil.SendSMTP(t, srv.SMTPAddr, "alice@example.test", []string{"bob@example.test"}, raw)

	f, err = readSSEFrame(t, r1)
	if err != nil {
		t.Fatalf("read message.created frame: %v", err)
	}
	if f.event != "message.created" {
		t.Fatalf("SSE event = %q, want message.created", f.event)
	}
	var createdPayload struct {
		Project string `json:"project"`
		Message struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(f.data), &createdPayload); err != nil {
		t.Fatalf("unmarshal message.created data %q: %v", f.data, err)
	}
	if createdPayload.Project != "default" || createdPayload.Message.Subject != "Live SSE" || createdPayload.Message.ID == "" {
		t.Fatalf("unexpected message.created payload: %+v", createdPayload)
	}

	// Advance fake clock 25s -> receive ": ping" heartbeat.
	srv.Clock.Advance(25 * time.Second)
	f, err = readSSEFrame(t, r1)
	if err != nil {
		t.Fatalf("read heartbeat frame: %v", err)
	}
	if f.comment != ": ping" {
		t.Fatalf("heartbeat comment = %q, want %q", f.comment, ": ping")
	}

	// Open stream 2 filtered to ?project=default (max is 2).
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	resp2, r2 := openSSE(ctx2, "/api/v1/events?project=default")
	defer func() { _ = resp2.Body.Close() }()
	if _, err := readSSEFrame(t, r2); err != nil {
		t.Fatalf("stream 2 retry: %v", err)
	}

	// Open stream 3 -> exceeds SSEMaxConnectionsPerUser=2 -> evicts oldest (stream 1).
	ctx3, cancel3 := context.WithCancel(t.Context())
	defer cancel3()
	resp3, r3 := openSSE(ctx3, "/api/v1/events")
	defer func() { _ = resp3.Body.Close() }()
	if _, err := readSSEFrame(t, r3); err != nil {
		t.Fatalf("stream 3 retry: %v", err)
	}

	// Stream 1 must close with EOF promptly.
	if _, err := readSSEFrame(t, r1); !errors.Is(err, io.EOF) {
		t.Fatalf("expected evicted stream 1 to close with EOF, got %v", err)
	}

	// Project filter check: send to another project ("other-proj"), then to "default".
	// Stream 2 (?project=default) should only see "default".
	testutil.SendSMTPPlain(t, srv.SMTPAddr, "other-proj", "x", "a@example.test", []string{"b@example.test"},
		[]byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: Other\r\n\r\nHi\r\n"))
	testutil.SendSMTP(t, srv.SMTPAddr, "a@example.test", []string{"b@example.test"},
		[]byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: DefaultOnly\r\n\r\nHi\r\n"))

	f, err = readSSEFrame(t, r2)
	if err != nil {
		t.Fatalf("read filtered stream 2: %v", err)
	}
	if !strings.Contains(f.data, `"DefaultOnly"`) {
		t.Fatalf("filtered stream 2 got %q, want DefaultOnly", f.data)
	}

	// Non-existent project filter returns 404.
	res404 := doJSON(t, &http.Client{Timeout: 5 * time.Second}, http.MethodGet, base+"/api/v1/events?project=no-such-project", nil)
	if res404.status != http.StatusNotFound {
		t.Fatalf("GET /events?project=missing status = %d, want 404", res404.status)
	}

	// Leave stream 2 & 3 open until t.Cleanup cancels server -> verifies graceful shutdown closes SSE immediately.
}

func TestIntegrationWaitMessage(t *testing.T) {
	t.Parallel()

	// FR-078: wait returns message sent after request started, 204 on timeout,
	// immediate return with since=<older ULID> and since=<RFC3339>.
	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Wait for a message sent after request starts.
	beforeSubs := srv.Bus.SubscriberCount()
	waitCh := make(chan httpResult, 1)
	go func() {
		waitCh <- doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait?timeout=10s", nil)
	}()
	waitUntil(t, 2*time.Second, func() bool {
		return srv.Bus.SubscriberCount() > beforeSubs && srv.Clock.TimerCount() > 0
	})

	testutil.SendSMTP(t, srv.SMTPAddr, "alice@example.test", []string{"bob@example.test"},
		[]byte("From: alice@example.test\r\nTo: bob@example.test\r\nSubject: Arrived During Wait\r\n\r\nBody\r\n"))

	var msg1 struct {
		ID         string `json:"id"`
		Subject    string `json:"subject"`
		ReceivedAt string `json:"received_at"`
	}
	select {
	case res := <-waitCh:
		if res.status != http.StatusOK {
			t.Fatalf("wait status = %d, want 200, body = %s", res.status, string(res.body))
		}
		if err := json.Unmarshal(res.body, &msg1); err != nil {
			t.Fatalf("unmarshal wait response: %v", err)
		}
		if msg1.Subject != "Arrived During Wait" {
			t.Fatalf("wait subject = %q, want Arrived During Wait", msg1.Subject)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for wait response")
	}

	// 2. Timeout returns 204 when fake clock advances past timeout.
	timersBefore := srv.Clock.TimerCount()
	timeoutCh := make(chan httpResult, 1)
	go func() {
		timeoutCh <- doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait?timeout=5s", nil)
	}()
	waitUntil(t, 2*time.Second, func() bool {
		return srv.Clock.TimerCount() > timersBefore
	})
	srv.Clock.Advance(5 * time.Second)

	select {
	case res := <-timeoutCh:
		if res.status != http.StatusNoContent {
			t.Fatalf("wait timeout status = %d, want 204", res.status)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for 204 response")
	}

	// 3. Send a second message, then call wait with since=<msg1.ID> -> returns msg2 immediately.
	srv.Clock.Advance(time.Second)
	testutil.SendSMTP(t, srv.SMTPAddr, "alice@example.test", []string{"bob@example.test"},
		[]byte("From: alice@example.test\r\nTo: bob@example.test\r\nSubject: Second Message\r\n\r\nBody 2\r\n"))

	res := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait?since="+msg1.ID+"&timeout=5s", nil)
	if res.status != http.StatusOK {
		t.Fatalf("wait since=<older ULID> status = %d, want 200, body = %s", res.status, string(res.body))
	}
	var msg2 struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(res.body, &msg2)
	if msg2.Subject != "Second Message" || msg2.ID <= msg1.ID {
		t.Fatalf("unexpected wait since=<ULID> result: %+v (msg1=%s)", msg2, msg1.ID)
	}

	// 4. since=<RFC 3339> returns matching existing message immediately.
	sinceTS := srv.Clock.Now().UTC().Add(-500 * time.Millisecond).Format(time.RFC3339)
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait?since="+sinceTS+"&timeout=5s", nil)
	if res.status != http.StatusOK {
		t.Fatalf("wait since=<RFC3339> status = %d, want 200, body = %s", res.status, string(res.body))
	}

	// 5. Validation errors return 400.
	for _, bad := range []string{
		"?since=not-a-ulid-or-time",
		"?timeout=0s",
		"?timeout=999s",
		"?timeout=bogus",
		"?query=before:not-a-date",
	} {
		r := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait"+bad, nil)
		if r.status != http.StatusBadRequest {
			t.Errorf("wait %s status = %d, want 400", bad, r.status)
		}
	}
}

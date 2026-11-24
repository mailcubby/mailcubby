package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/testutil"
)

// TestSearchQueryOnListDeleteAndWait verifies FR-080, FR-081, and FR-082 across
// GET /messages, DELETE /messages, and GET /messages/wait.
func TestSearchQueryOnListDeleteAndWait(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	sendMsg := func(from, to, subject, body string) {
		t.Helper()
		raw := "From: " + from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"\r\n" +
			body + "\r\n"
		testutil.SendSMTP(t, srv.SMTPAddr, from, []string{to}, []byte(raw))
	}

	sendMsg("alice@shop.test", "bob@example.test", "Order 12 confirmation", "Hello João, order 12 is ready.")
	sendMsg("alice@shop.test", "bob@example.test", "Order 12 update", "Another update for order 12.")
	sendMsg("carol@shop.test", "dave@example.test", "Newsletter", "Weekly digest from Carol.")

	// 1. GET /messages?query= with diacritic folding and pagination (FR-080, FR-082).
	res := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?query="+url.QueryEscape("joao"), nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET query=joao status = %d, body = %s", res.status, string(res.body))
	}
	var list struct {
		Items []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(res.body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Subject != "Order 12 confirmation" {
		t.Fatalf("query=joao items = %+v, want [Order 12 confirmation]", list.Items)
	}
	firstID := list.Items[0].ID

	// Paginate filtered list with limit=1 and cursor.
	qAlice := url.QueryEscape(`from:alice subject:"order 12"`)
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?query="+qAlice+"&limit=1", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET query=alice page 1 status = %d, body = %s", res.status, string(res.body))
	}
	if err := json.Unmarshal(res.body, &list); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(list.Items) != 1 || list.NextCursor == nil {
		t.Fatalf("page 1 expected 1 item and next_cursor, got %+v", list)
	}
	page1ID := list.Items[0].ID

	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?query="+qAlice+"&limit=1&cursor="+url.QueryEscape(*list.NextCursor), nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET query=alice page 2 status = %d, body = %s", res.status, string(res.body))
	}
	if err := json.Unmarshal(res.body, &list); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID == page1ID || list.NextCursor != nil {
		t.Fatalf("page 2 expected 1 distinct item and nil cursor, got %+v", list)
	}

	// 2. GET /messages/wait?query=&since= returns matching existing message immediately.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait?since="+url.QueryEscape(firstID)+"&query="+url.QueryEscape("from:carol")+"&timeout=1s", nil)
	if res.status != http.StatusOK {
		t.Fatalf("wait with since+query status = %d, body = %s", res.status, string(res.body))
	}
	var waited struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(res.body, &waited); err != nil {
		t.Fatalf("decode wait existing: %v", err)
	}
	if waited.Subject != "Newsletter" {
		t.Fatalf("wait existing subject = %q, want Newsletter", waited.Subject)
	}

	// 3. GET /messages/wait?query= ignores non-matching live mail and returns matching mail.
	type waitOut struct {
		status int
		body   []byte
	}
	waitCh := make(chan waitOut, 1)
	beforeSubs := srv.App.Bus().SubscriberCount()
	go func() {
		r := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages/wait?query="+url.QueryEscape("from:vip subject:invoice")+"&timeout=2s", nil)
		waitCh <- waitOut{status: r.status, body: r.body}
	}()

	waitUntil(t, 2*time.Second, func() bool {
		return srv.App.Bus().SubscriberCount() > beforeSubs
	})
	sendMsg("noise@shop.test", "bob@example.test", "Ignore me", "Not matching.")
	sendMsg("vip@shop.test", "bob@example.test", "VIP Invoice #99", "Matched live message.")

	select {
	case out := <-waitCh:
		if out.status != http.StatusOK {
			t.Fatalf("live wait status = %d, want 200, body = %s", out.status, string(out.body))
		}
		if err := json.Unmarshal(out.body, &waited); err != nil {
			t.Fatalf("decode live wait: %v", err)
		}
		if waited.Subject != "VIP Invoice #99" {
			t.Fatalf("live wait subject = %q, want VIP Invoice #99", waited.Subject)
		}
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("timed out waiting for filtered live message")
	}

	// 4. DELETE /messages?query= deletes only matching messages and preserves counters.
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages?query="+url.QueryEscape("from:alice"), nil)
	if res.status != http.StatusOK {
		t.Fatalf("DELETE ?query=from:alice status = %d, body = %s", res.status, string(res.body))
	}
	var delRes struct {
		Affected int64 `json:"affected"`
	}
	if err := json.Unmarshal(res.body, &delRes); err != nil {
		t.Fatalf("decode delete result: %v", err)
	}
	if delRes.Affected != 2 {
		t.Fatalf("DELETE ?query=from:alice affected = %d, want 2", delRes.Affected)
	}

	// Verify 3 messages remain (Newsletter, Ignore me, VIP Invoice #99) and project counters match.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET project status = %d", res.status)
	}
	var proj struct {
		MessageCount int64 `json:"message_count"`
		UnreadCount  int64 `json:"unread_count"`
	}
	if err := json.Unmarshal(res.body, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if proj.MessageCount != 3 || proj.UnreadCount != 3 {
		t.Fatalf("project counts after filtered delete = (%d, %d), want (3, 3)", proj.MessageCount, proj.UnreadCount)
	}

	// Empty parsed query on DELETE returns 400 (safety guard against accidental full delete).
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages?query="+url.QueryEscape(`""`), nil)
	if res.status != http.StatusBadRequest {
		t.Fatalf("DELETE ?query=\"\" status = %d, want 400", res.status)
	}
}

// TestDeleteMessagesByQueryRejectsNoOpFilter verifies that DELETE /messages?query=
// returns 400 and deletes nothing when the query does not narrow the selection,
// instead of deleting every message in the project (FR-076, FR-081, FR-082).
func TestDeleteMessagesByQueryRejectsNoOpFilter(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	for _, from := range []string{"alice@shop.test", "bob@shop.test", "carol@shop.test"} {
		raw := "From: " + from + "\r\nTo: dest@example.test\r\nSubject: Hello\r\n\r\nBody.\r\n"
		testutil.SendSMTP(t, srv.SMTPAddr, from, []string{"dest@example.test"}, []byte(raw))
	}

	messageCount := func() int64 {
		t.Helper()
		res := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default", nil)
		if res.status != http.StatusOK {
			t.Fatalf("GET project status = %d, body = %s", res.status, string(res.body))
		}
		var proj struct {
			MessageCount int64 `json:"message_count"`
		}
		if err := json.Unmarshal(res.body, &proj); err != nil {
			t.Fatalf("decode project: %v", err)
		}
		return proj.MessageCount
	}
	if got := messageCount(); got != 3 {
		t.Fatalf("message_count after seeding = %d, want 3", got)
	}

	for _, query := range []string{`-"!"`, `-!`, `-*`, "-\U0001E030", `-credential:nope`, `-tag:nope`} {
		res := doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages?query="+url.QueryEscape(query), nil)
		if res.status != http.StatusBadRequest {
			t.Fatalf("DELETE ?query=%s status = %d, want 400 (body %s)", query, res.status, string(res.body))
		}
		if got := messageCount(); got != 3 {
			t.Fatalf("message_count after DELETE ?query=%s = %d, want 3", query, got)
		}
	}

	// List keeps matching everything for a no-op negated term (documented behaviour).
	res := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?query="+url.QueryEscape(`-"!"`), nil)
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &list) != nil || len(list.Items) != 3 {
		t.Fatalf("GET ?query=-\"!\" status = %d, items = %d; want 200 with 3 items", res.status, len(list.Items))
	}

	// An exclusion that excludes something still deletes the rest.
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/messages?query="+url.QueryEscape(`-from:alice`), nil)
	if res.status != http.StatusOK {
		t.Fatalf("DELETE ?query=-from:alice status = %d, body = %s", res.status, string(res.body))
	}
	if got := messageCount(); got != 1 {
		t.Fatalf("message_count after DELETE ?query=-from:alice = %d, want 1", got)
	}
}

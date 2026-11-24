package api_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/testutil"
)

// TestAutoTagsAndTagEndpoints verifies FR-085, FR-086, SPEC 8.5, PATCH message tags,
// bulk add_tags/remove_tags, tag list filter, and message.updated SSE events.
func TestAutoTagsAndTagEndpoints(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	base := "http://" + srv.HTTPAddr
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. FR-086 acceptance criterion: X-Tags: invoice, Staging + orders+urgent@shop.test -> 3 tags.
	raw := "From: Alice <alice@shop.test>\r\n" +
		"To: orders+urgent@shop.test\r\n" +
		"Subject: Order #42\r\n" +
		"X-Tags: invoice, Staging\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Order body.\r\n"
	testutil.SendSMTP(t, srv.SMTPAddr, "alice@shop.test", []string{"orders+urgent@shop.test"}, []byte(raw))

	res := doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET messages status = %d, body = %s", res.status, string(res.body))
	}
	var list struct {
		Items []struct {
			ID   string   `json:"id"`
			Tags []string `json:"tags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res.body, &list); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 message, got %d", len(list.Items))
	}
	msg1ID := list.Items[0].ID
	if !slices.Equal(list.Items[0].Tags, []string{"invoice", "Staging", "urgent"}) {
		t.Fatalf("auto-tags = %v, want [invoice Staging urgent]", list.Items[0].Tags)
	}

	// Verify GET /tags returns all 3 auto-created tags with count 1.
	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/tags", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET tags status = %d, body = %s", res.status, string(res.body))
	}
	type tagItem struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Color        string `json:"color"`
		MessageCount int64  `json:"message_count"`
	}
	var tags []tagItem
	if err := json.Unmarshal(res.body, &tags); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %+v", tags)
	}
	for _, tg := range tags {
		if tg.MessageCount != 1 {
			t.Errorf("tag %q message_count = %d, want 1", tg.Name, tg.MessageCount)
		}
	}

	// 2. Disable plus_address_tags on project and verify plus tag is NOT attached on next message.
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/default", map[string]any{
		"settings": map[string]any{"plus_address_tags": false},
	})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH project plus_address_tags=false status = %d, body = %s", res.status, string(res.body))
	}
	raw2 := "From: Bob <bob@shop.test>\r\n" +
		"To: user+ignored@shop.test\r\n" +
		"Subject: Second message\r\n" +
		"X-Tags: newsletter\r\n" +
		"\r\n" +
		"Hello.\r\n"
	testutil.SendSMTP(t, srv.SMTPAddr, "bob@shop.test", []string{"user+ignored@shop.test"}, []byte(raw2))

	res = doJSON(t, client, http.MethodGet, base+"/api/v1/projects/default/messages?tag="+url.QueryEscape("NEWSLETTER"), nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET ?tag=NEWSLETTER status = %d, body = %s", res.status, string(res.body))
	}
	if err := json.Unmarshal(res.body, &list); err != nil {
		t.Fatalf("decode filtered messages: %v", err)
	}
	if len(list.Items) != 1 || !slices.Equal(list.Items[0].Tags, []string{"newsletter"}) {
		t.Fatalf("?tag=NEWSLETTER items = %+v, want 1 item with [newsletter]", list.Items)
	}
	msg2ID := list.Items[0].ID

	// 3. Tag CRUD endpoints: POST (201, 409 dup, 422 invalid), PATCH (200), DELETE (204).
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/tags", map[string]any{
		"name":  "vip",
		"color": "violet",
	})
	if res.status != http.StatusCreated {
		t.Fatalf("POST /tags status = %d, body = %s", res.status, string(res.body))
	}
	var createdTag tagItem
	if err := json.Unmarshal(res.body, &createdTag); err != nil {
		t.Fatalf("decode created tag: %v", err)
	}
	if createdTag.Name != "vip" || createdTag.Color != "violet" || createdTag.MessageCount != 0 {
		t.Fatalf("unexpected created tag: %+v", createdTag)
	}

	// Duplicate case-insensitive -> 409.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/tags", map[string]any{"name": "VIP"})
	if res.status != http.StatusConflict {
		t.Fatalf("duplicate POST /tags status = %d, want 409", res.status)
	}

	// Invalid tag name -> 422.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/tags", map[string]any{"name": "bad tag!"})
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid POST /tags status = %d, want 422", res.status)
	}

	// Rename + recolor tag -> 200.
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/default/tags/"+strconv.FormatInt(createdTag.ID, 10), map[string]any{
		"name":  "priority",
		"color": "red",
	})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH /tags/{id} status = %d, body = %s", res.status, string(res.body))
	}

	// 4. Open SSE stream and verify message.updated events for PATCH tags and bulk add/remove tags.
	sseReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/v1/events?project=default", nil)
	if err != nil {
		t.Fatalf("NewRequest SSE: %v", err)
	}
	sseClient := &http.Client{}
	sseResp, err := sseClient.Do(sseReq)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()
	reader := bufio.NewReader(sseResp.Body)
	// Drain initial retry frame.
	if _, err := readSSEFrame(t, reader); err != nil {
		t.Fatalf("read initial SSE frame: %v", err)
	}

	// PATCH message tags (replace with ["priority", "new-tag"]).
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/default/messages/"+msg2ID, map[string]any{
		"tags": []string{"priority", "new-tag"},
	})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH message tags status = %d, body = %s", res.status, string(res.body))
	}
	frame, err := readSSEFrame(t, reader)
	if err != nil {
		t.Fatalf("read SSE after PATCH tags: %v", err)
	}
	if frame.event != "message.updated" {
		t.Fatalf("SSE event = %q, want message.updated", frame.event)
	}
	var updPayload struct {
		IDs  []string  `json:"ids"`
		Tags *[]string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(frame.data), &updPayload); err != nil {
		t.Fatalf("decode SSE message.updated: %v", err)
	}
	if !slices.Equal(updPayload.IDs, []string{msg2ID}) || updPayload.Tags == nil || !slices.Equal(*updPayload.Tags, []string{"new-tag", "priority"}) {
		t.Fatalf("unexpected PATCH SSE payload: %+v", updPayload)
	}

	// Clearing all tags on msg2 emits `"tags": []` (non-nil empty slice).
	res = doJSON(t, client, http.MethodPatch, base+"/api/v1/projects/default/messages/"+msg2ID, map[string]any{
		"tags": []string{},
	})
	if res.status != http.StatusOK {
		t.Fatalf("PATCH clear tags status = %d, body = %s", res.status, string(res.body))
	}
	frame, err = readSSEFrame(t, reader)
	if err != nil {
		t.Fatalf("read SSE after clear tags: %v", err)
	}
	updPayload = struct {
		IDs  []string  `json:"ids"`
		Tags *[]string `json:"tags"`
	}{}
	if err := json.Unmarshal([]byte(frame.data), &updPayload); err != nil {
		t.Fatalf("decode clear tags SSE: %v", err)
	}
	if updPayload.Tags == nil || len(*updPayload.Tags) != 0 {
		t.Fatalf("expected non-nil empty tags in SSE after clear, got %+v (raw: %s)", updPayload, frame.data)
	}

	// Bulk add_tags and remove_tags.
	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/messages/bulk", map[string]any{
		"ids":    []string{msg1ID, msg2ID},
		"action": "add_tags",
		"tags":   []string{"priority"},
	})
	if res.status != http.StatusOK {
		t.Fatalf("bulk add_tags status = %d, body = %s", res.status, string(res.body))
	}
	var bulkRes struct {
		Affected int64 `json:"affected"`
	}
	if err := json.Unmarshal(res.body, &bulkRes); err != nil {
		t.Fatalf("decode bulk add_tags: %v", err)
	}
	if bulkRes.Affected != 2 {
		t.Fatalf("bulk add_tags affected = %d, want 2", bulkRes.Affected)
	}

	// Drain 2 message.updated events from bulk add_tags.
	for range 2 {
		if f, fErr := readSSEFrame(t, reader); fErr != nil || f.event != "message.updated" {
			t.Fatalf("expected message.updated after bulk add_tags, got %+v err=%v", f, fErr)
		}
	}

	res = doJSON(t, client, http.MethodPost, base+"/api/v1/projects/default/messages/bulk", map[string]any{
		"ids":    []string{msg1ID, msg2ID},
		"action": "remove_tags",
		"tags":   []string{"priority"},
	})
	if res.status != http.StatusOK {
		t.Fatalf("bulk remove_tags status = %d, body = %s", res.status, string(res.body))
	}
	if err := json.Unmarshal(res.body, &bulkRes); err != nil {
		t.Fatalf("decode bulk remove_tags: %v", err)
	}
	if bulkRes.Affected != 2 {
		t.Fatalf("bulk remove_tags affected = %d, want 2", bulkRes.Affected)
	}

	// Delete tag returns 204.
	res = doJSON(t, client, http.MethodDelete, base+"/api/v1/projects/default/tags/"+strconv.FormatInt(createdTag.ID, 10), nil)
	if res.status != http.StatusNoContent {
		t.Fatalf("DELETE /tags/{id} status = %d, want 204", res.status)
	}
}

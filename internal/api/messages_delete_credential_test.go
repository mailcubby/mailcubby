package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/mailcubby/mailcubby/internal/store"
)

// TestDeleteMessagesByCredentialID verifies that DELETE /messages?credential=<id>
// deletes by credential ID, so credentials sharing a label (labels are not
// unique) never lose each other's messages (FR-021, FR-076, FR-082).
func TestDeleteMessagesByCredentialID(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@delcred.test", "Owner", pass, false, true, false)
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "delcred", Name: "Del Cred", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	cookie, csrf := loginAndGetCSRF(t, h, owner.Email, pass)

	createCred := func(label string) int64 {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/delcred/smtp-credentials", map[string]any{"label": label}, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create cred(%s) status = %d, body = %s", label, rec.Code, rec.Body.String())
		}
		var out struct {
			Credential struct {
				ID int64 `json:"id"`
			} `json:"credential"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Credential.ID == 0 {
			t.Fatalf("decode cred(%s): %v", label, err)
		}
		return out.Credential.ID
	}
	credA := createCred("staging")
	credB := createCred("Staging")
	credC := createCred("staging")

	now := env.clk.Now().UTC()
	insert := func(id, subject string, credID int64, seen bool) {
		t.Helper()
		_, err := env.store.InsertMessage(t.Context(), store.InsertMessageParams{
			ID: id, ProjectID: proj.ID, SMTPCredentialID: &credID, ReceivedAt: now, Subject: subject,
			FromAddr: "app@delcred.test", EnvelopeFrom: "app@delcred.test", EnvelopeTo: []string{"u@delcred.test"},
			HasText: true, SizeBytes: 64, BlobPath: "1/2026/11/" + id + ".eml.zst", Seen: seen,
		})
		if err != nil {
			t.Fatalf("InsertMessage(%s): %v", id, err)
		}
	}
	insert("01JDELCRED0000000000000001", "A one", credA, false)
	insert("01JDELCRED0000000000000002", "A two", credA, true)
	insert("01JDELCRED0000000000000003", "B one", credB, true)
	insert("01JDELCRED0000000000000004", "C one", credC, true)

	countFor := func(credID int64) int {
		t.Helper()
		rec := doReq(t, h, http.MethodGet, "/api/v1/projects/delcred/messages?credential="+strconv.FormatInt(credID, 10), nil, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list credential=%d status = %d", credID, rec.Code)
		}
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return len(list.Items)
	}
	deleteURL := func(params string) string {
		return "/api/v1/projects/delcred/messages?" + params
	}
	affected := func(rec interface{ Bytes() []byte }) int64 {
		t.Helper()
		var out struct {
			Affected int64 `json:"affected"`
		}
		if err := json.Unmarshal(rec.Bytes(), &out); err != nil {
			t.Fatalf("decode affected: %v", err)
		}
		return out.Affected
	}

	// Invalid credential parameter is rejected like on the list endpoint.
	for _, bad := range []string{"abc", "0", "-1"} {
		rec := doReq(t, h, http.MethodDelete, deleteURL("credential="+bad), nil, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("DELETE ?credential=%s status = %d, want 400", bad, rec.Code)
		}
	}

	// credential + query narrows within the credential (B and C also hold read messages).
	rec := doReq(t, h, http.MethodDelete, deleteURL("credential="+strconv.FormatInt(credA, 10)+"&query=is:read"), nil, cookie, csrfHeaders(csrf))
	if rec.Code != http.StatusOK || affected(rec.Body) != 1 {
		t.Fatalf("DELETE ?credential=A&query=is:read = (%d, %s), want (200, affected 1)", rec.Code, rec.Body.String())
	}

	// credential alone (no query) deletes only that credential's messages.
	rec = doReq(t, h, http.MethodDelete, deleteURL("credential="+strconv.FormatInt(credA, 10)), nil, cookie, csrfHeaders(csrf))
	if rec.Code != http.StatusOK || affected(rec.Body) != 1 {
		t.Fatalf("DELETE ?credential=A = (%d, %s), want (200, affected 1)", rec.Code, rec.Body.String())
	}
	if a, b, c := countFor(credA), countFor(credB), countFor(credC); a != 0 || b != 1 || c != 1 {
		t.Fatalf("remaining per credential = (A %d, B %d, C %d), want (0, 1, 1)", a, b, c)
	}
}

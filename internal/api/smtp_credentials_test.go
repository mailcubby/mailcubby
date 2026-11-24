package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/store"
)

var (
	smtpUsernameRe = regexp.MustCompile(`^shop-[a-z0-9]{8}$`)
	smtpPasswordRe = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)
)

// FR-060, FR-061, FR-062, FR-063, FR-105: SMTP credential CRUD, reveal audit,
// rotate, disable/enable, 20-credential limit, and encrypted-at-rest verification.
func TestSMTPCredentialsLifecycleAndSecurity(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@shop.test", "Owner", pass, false, true, false)
	member := env.createUser(t, "member@shop.test", "Member", pass, false, false, false)

	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "shop", Name: "Shop", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(owner): %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, member.ID, "member", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(member): %v", err)
	}

	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
	memberCookie, memberCSRF := loginAndGetCSRF(t, h, member.Email, pass)

	// 1. Create credential (FR-060).
	createRec := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials", map[string]any{
		"label": "staging",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRec.Code, createRec.Body.String())
	}
	if cc := createRec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("create Cache-Control = %q, want private, no-store", cc)
	}

	var created struct {
		Credential struct {
			ID         int64   `json:"id"`
			Label      string  `json:"label"`
			Username   string  `json:"username"`
			CreatedAt  string  `json:"created_at"`
			LastUsedAt *string `json:"last_used_at"`
			Disabled   bool    `json:"disabled"`
		} `json:"credential"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.Credential.Label != "staging" || created.Credential.Disabled || created.Credential.LastUsedAt != nil {
		t.Fatalf("unexpected credential payload: %+v", created.Credential)
	}
	if !smtpUsernameRe.MatchString(created.Credential.Username) {
		t.Fatalf("username %q does not match <slug>-<8 lower alnum>", created.Credential.Username)
	}
	if !smtpPasswordRe.MatchString(created.Password) {
		t.Fatalf("password %q does not match 24 chars [A-Za-z0-9]", created.Password)
	}

	// 2. FR-061 AC: DB dump (main DB + WAL) contains no plaintext SMTP password.
	rawCred, err := env.store.GetSMTPCredentialByID(t.Context(), proj.ID, created.Credential.ID)
	if err != nil {
		t.Fatalf("GetSMTPCredentialByID: %v", err)
	}
	if bytes.Contains(rawCred.PasswordEnc, []byte(created.Password)) {
		t.Fatalf("password_enc contains plaintext password")
	}
	var seq int
	var dbName, dbFile string
	if err := env.store.ReaderDB().QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &dbName, &dbFile); err != nil {
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	for _, p := range []string{dbFile, dbFile + "-wal"} {
		//nolint:gosec // G304: test reads temp SQLite files reported by PRAGMA database_list
		if data, readErr := os.ReadFile(p); readErr == nil {
			if bytes.Contains(data, []byte(created.Password)) {
				t.Fatalf("sqlite file %s contains plaintext SMTP password", filepath.Base(p))
			}
		}
	}

	// 3. List credentials with connection info (FR-063) — never returns password.
	listRec := doReq(t, h, http.MethodGet, "/api/v1/projects/shop/smtp-credentials", nil, memberCookie, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), created.Password) {
		t.Fatalf("list response leaked password")
	}
	var listed struct {
		Connection struct {
			Host    string `json:"host"`
			Port    int    `json:"port"`
			TLSMode string `json:"tls_mode"`
		} `json:"connection"`
		Items []struct {
			ID       int64  `json:"id"`
			Label    string `json:"label"`
			Username string `json:"username"`
			Disabled bool   `json:"disabled"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if listed.Connection.Host == "" || listed.Connection.Port == 0 || listed.Connection.TLSMode == "" {
		t.Fatalf("incomplete connection info: %+v", listed.Connection)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.Credential.ID {
		t.Fatalf("unexpected listed items: %+v", listed.Items)
	}

	// 4. Member cannot reveal when members_can_reveal_smtp=false (FR-050).
	credIDStr := strconv.FormatInt(created.Credential.ID, 10)
	memRevealRec := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials/"+credIDStr+"/reveal", nil, memberCookie, csrfHeaders(memberCSRF))
	if memRevealRec.Code != http.StatusForbidden {
		t.Fatalf("member reveal status = %d, want 403", memRevealRec.Code)
	}

	// 5. Owner reveal returns exact password and writes exactly one smtp_credential.reveal audit entry (FR-062, FR-105).
	revealRec := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials/"+credIDStr+"/reveal", nil, ownerCookie, csrfHeaders(ownerCSRF))
	if revealRec.Code != http.StatusOK {
		t.Fatalf("owner reveal status = %d, want 200; body=%s", revealRec.Code, revealRec.Body.String())
	}
	var revealed struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(revealRec.Body.Bytes(), &revealed); err != nil {
		t.Fatalf("unmarshal reveal: %v", err)
	}
	if revealed.Password != created.Password {
		t.Fatalf("revealed password mismatch")
	}

	var revealAuditCount int
	var auditActorID int64
	var auditIP string
	var auditMeta string
	if err := env.store.ReaderDB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(actor_user_id), 0), COALESCE(MAX(ip), ''), COALESCE(MAX(metadata_json), '') FROM audit_log WHERE action = 'smtp_credential.reveal' AND project_id = ?`,
		proj.ID,
	).Scan(&revealAuditCount, &auditActorID, &auditIP, &auditMeta); err != nil {
		t.Fatalf("query reveal audit: %v", err)
	}
	if revealAuditCount != 1 || auditActorID != owner.ID || auditIP == "" {
		t.Fatalf("reveal audit = (count=%d, actor=%d, ip=%q), want (1, %d, non-empty)", revealAuditCount, auditActorID, auditIP, owner.ID)
	}
	if strings.Contains(auditMeta, created.Password) {
		t.Fatalf("audit metadata must never contain plaintext password")
	}

	// 6. Rotate keeps username immutable and returns a new password (FR-062).
	rotRec := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials/"+credIDStr+"/rotate", nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rotRec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200; body=%s", rotRec.Code, rotRec.Body.String())
	}
	var rotated struct {
		Credential struct {
			Username string `json:"username"`
		} `json:"credential"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(rotRec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("unmarshal rotate: %v", err)
	}
	if rotated.Credential.Username != created.Credential.Username {
		t.Fatalf("rotate changed username from %q to %q", created.Credential.Username, rotated.Credential.Username)
	}
	if !smtpPasswordRe.MatchString(rotated.Password) || rotated.Password == created.Password {
		t.Fatalf("rotated password invalid or unchanged")
	}

	// 7. Patch label & disabled status.
	patchRec := doReq(t, h, http.MethodPatch, "/api/v1/projects/shop/smtp-credentials/"+credIDStr, map[string]any{
		"label":    "staging-updated",
		"disabled": true,
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRec.Code, patchRec.Body.String())
	}
	var patched struct {
		Label    string `json:"label"`
		Disabled bool   `json:"disabled"`
	}
	_ = json.Unmarshal(patchRec.Body.Bytes(), &patched)
	if patched.Label != "staging-updated" || !patched.Disabled {
		t.Fatalf("unexpected patched credential: %+v", patched)
	}

	// 8. Validation errors.
	badLabel := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials", map[string]any{
		"label": "   ",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if badLabel.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty label status = %d, want 422", badLabel.Code)
	}

	// 9. Max 20 credentials per project (FR-060).
	for i := 2; i <= 20; i++ {
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials", map[string]any{
			"label": "cred-" + strconv.Itoa(i),
		}, ownerCookie, csrfHeaders(ownerCSRF))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create credential #%d status = %d, want 201", i, rec.Code)
		}
	}
	rec21 := doReq(t, h, http.MethodPost, "/api/v1/projects/shop/smtp-credentials", map[string]any{
		"label": "cred-21",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec21.Code != http.StatusConflict {
		t.Fatalf("21st credential status = %d, want 409; body=%s", rec21.Code, rec21.Body.String())
	}

	// 10. Delete credential.
	delRec := doReq(t, h, http.MethodDelete, "/api/v1/projects/shop/smtp-credentials/"+credIDStr, nil, ownerCookie, csrfHeaders(ownerCSRF))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", delRec.Code, delRec.Body.String())
	}
	// Second delete returns 404.
	delRec2 := doReq(t, h, http.MethodDelete, "/api/v1/projects/shop/smtp-credentials/"+credIDStr, nil, ownerCookie, csrfHeaders(ownerCSRF))
	if delRec2.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", delRec2.Code)
	}

	// Verify audit entries for all 5 actions exist.
	for _, action := range []string{
		"smtp_credential.create",
		"smtp_credential.reveal",
		"smtp_credential.rotate",
		"smtp_credential.update",
		"smtp_credential.delete",
	} {
		var cnt int
		if err := env.store.ReaderDB().QueryRowContext(
			t.Context(),
			`SELECT COUNT(*) FROM audit_log WHERE action = ? AND project_id = ?`,
			action, proj.ID,
		).Scan(&cnt); err != nil {
			t.Fatalf("count audit %s: %v", action, err)
		}
		if cnt == 0 {
			t.Errorf("expected at least 1 audit entry for %s", action)
		}
	}
}

// FR-021, FR-081, FR-082: ?credential=<id> list filter, credential:<label> search
// operator on list and delete-all, and credential_label populated on summaries & details.
func TestMessagesCredentialFilterAndSearch(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@creds.test", "Owner", pass, false, true, false)
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "creds-proj", Name: "Creds Proj", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
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
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/creds-proj/smtp-credentials", map[string]any{"label": label}, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create cred(%s) status = %d", label, rec.Code)
		}
		var out struct {
			Credential struct {
				ID int64 `json:"id"`
			} `json:"credential"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Credential.ID
	}

	credStagingID := createCred("staging")
	credWorkerID := createCred("worker")

	// Seed two messages attributed to the two credentials + one without credential.
	now := env.clk.Now().UTC()
	insertAttributed := func(id, subject string, credID *int64) {
		t.Helper()
		_, err := env.store.InsertMessage(t.Context(), store.InsertMessageParams{
			ID:               id,
			ProjectID:        proj.ID,
			SMTPCredentialID: credID,
			ReceivedAt:       now,
			Subject:          subject,
			FromAddr:         "app@creds.test",
			EnvelopeFrom:     "app@creds.test",
			EnvelopeTo:       []string{"u@creds.test"},
			HasText:          true,
			SizeBytes:        64,
			BlobPath:         "1/2026/11/" + id + ".eml.zst",
		})
		if err != nil {
			t.Fatalf("InsertMessage(%s): %v", id, err)
		}
	}

	const (
		idStaging = "01JAPICRED0000000000000001"
		idWorker  = "01JAPICRED0000000000000002"
		idNoCred  = "01JAPICRED0000000000000003"
	)
	insertAttributed(idStaging, "Staging mail", &credStagingID)
	insertAttributed(idWorker, "Worker mail", &credWorkerID)
	insertAttributed(idNoCred, "Direct mail", nil)

	// Invalid ?credential= returns 400.
	badRec := doReq(t, h, http.MethodGet, "/api/v1/projects/creds-proj/messages?credential=abc", nil, cookie, nil)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid ?credential= status = %d, want 400", badRec.Code)
	}

	// ?credential=<id> filters and includes credential_label.
	byIDRec := doReq(t, h, http.MethodGet, "/api/v1/projects/creds-proj/messages?credential="+strconv.FormatInt(credStagingID, 10), nil, cookie, nil)
	if byIDRec.Code != http.StatusOK {
		t.Fatalf("list by credential id status = %d, want 200; body=%s", byIDRec.Code, byIDRec.Body.String())
	}
	var listResp struct {
		Items []struct {
			ID              string `json:"id"`
			CredentialLabel string `json:"credential_label"`
		} `json:"items"`
	}
	_ = json.Unmarshal(byIDRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 || listResp.Items[0].ID != idStaging || listResp.Items[0].CredentialLabel != "staging" {
		t.Fatalf("list by credential id = %+v, want [{%s staging}]", listResp.Items, idStaging)
	}

	// ?query=credential:worker filters by label.
	byLabelRec := doReq(t, h, http.MethodGet, "/api/v1/projects/creds-proj/messages?query=credential:worker", nil, cookie, nil)
	if byLabelRec.Code != http.StatusOK {
		t.Fatalf("list by query credential:worker status = %d", byLabelRec.Code)
	}
	listResp.Items = nil
	_ = json.Unmarshal(byLabelRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 || listResp.Items[0].ID != idWorker || listResp.Items[0].CredentialLabel != "worker" {
		t.Fatalf("list by credential:worker = %+v, want [{%s worker}]", listResp.Items, idWorker)
	}

	// Filtered DELETE /messages?query=credential:worker deletes only worker message.
	delRec := doReq(t, h, http.MethodDelete, "/api/v1/projects/creds-proj/messages?query=credential:worker", nil, cookie, csrfHeaders(csrf))
	if delRec.Code != http.StatusOK || !strings.Contains(delRec.Body.String(), `"affected":1`) {
		t.Fatalf("filtered delete by credential = (%d, %s), want (200, affected=1)", delRec.Code, delRec.Body.String())
	}
}

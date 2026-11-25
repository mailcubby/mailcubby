package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
)

// FR-062: revealing a credential encrypted under another MAILCUBBY_APP_KEY
// returns a clear 409 conflict (not a bare 500) and writes no reveal audit.
func TestRevealSMTPCredentialWrongAppKey(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@keys.test", "Owner", pass, false, true, false)
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "keys", Name: "Keys", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	otherKey := make([]byte, secrets.KeySize)
	for i := range otherKey {
		otherKey[i] = 0xAB
	}
	otherCipher, err := secrets.NewSMTPCredentialCipher(otherKey)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher: %v", err)
	}
	const username = "keys-ab12cd34"
	sealed, err := otherCipher.Seal([]byte("SecretPassword1234567890"), []byte(username))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	cred, err := env.store.CreateSMTPCredential(t.Context(), store.CreateSMTPCredentialParams{
		ProjectID: proj.ID, Label: "old-key", Username: username, PasswordEnc: sealed, Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateSMTPCredential: %v", err)
	}

	cookie, csrf := loginAndGetCSRF(t, h, owner.Email, pass)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects/keys/smtp-credentials/"+strconv.FormatInt(cred.ID, 10)+"/reveal", nil, cookie, csrfHeaders(csrf))
	if rec.Code != http.StatusConflict {
		t.Fatalf("reveal status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "conflict" || !strings.Contains(body.Error.Message, "MAILCUBBY_APP_KEY") || !strings.Contains(body.Error.Message, "rotate") {
		t.Fatalf("error = %+v, want conflict naming MAILCUBBY_APP_KEY and rotate", body.Error)
	}

	var audits int
	if err := env.store.ReaderDB().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM audit_log WHERE action = 'smtp_credential.reveal' AND project_id = ?`, proj.ID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 0 {
		t.Fatalf("reveal audit entries = %d, want 0 for a failed reveal", audits)
	}

	// Recovery: rotate works under the current key, and reveal then succeeds.
	rotRec := doReq(t, h, http.MethodPost, "/api/v1/projects/keys/smtp-credentials/"+strconv.FormatInt(cred.ID, 10)+"/rotate", nil, cookie, csrfHeaders(csrf))
	if rotRec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200; body=%s", rotRec.Code, rotRec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/keys/smtp-credentials/"+strconv.FormatInt(cred.ID, 10)+"/reveal", nil, cookie, csrfHeaders(csrf))
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal after rotate status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

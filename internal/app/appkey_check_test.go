package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
)

// FR-061: server-mode startup trial-decrypts every SMTP credential and logs one
// ERROR naming how many cannot be decrypted with MAILCUBBY_APP_KEY, but still starts.
func TestAppLogsUndecryptableSMTPCredentials(t *testing.T) {
	t.Parallel()

	serverKey, err := base64.StdEncoding.DecodeString(testServerAppKey)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	otherKey := bytes.Repeat([]byte{0xEF}, secrets.KeySize)

	tests := []struct {
		name    string
		keys    [][]byte // one credential per key
		wantLog string   // "" = no ERROR expected
	}{
		{name: "no credentials", keys: nil},
		{name: "all decrypt", keys: [][]byte{serverKey, serverKey}},
		{name: "one of two under another key", keys: [][]byte{serverKey, otherKey}, wantLog: "1 of 2 SMTP credentials cannot be decrypted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			seedCredentials(t, dir, tc.keys)

			cfg, err := config.Load(func(k string) (string, bool) {
				switch k {
				case "MAILCUBBY_MODE":
					return "server", true
				case "MAILCUBBY_DATA_DIR":
					return dir, true
				case "MAILCUBBY_BASE_URL":
					return "http://127.0.0.1:8025", true
				case "MAILCUBBY_APP_KEY":
					return testServerAppKey, true
				case "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH":
					return "true", true
				case "MAILCUBBY_SMTP_ADDR", "MAILCUBBY_HTTP_ADDR":
					return "127.0.0.1:0", true
				default:
					return "", false
				}
			})
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			var logBuf bytes.Buffer
			a, err := app.Open(t.Context(), cfg, app.Options{
				Logger: slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError})),
			})
			if err != nil {
				t.Fatalf("app.Open must not refuse to start: %v", err)
			}
			_ = a.Close()

			got := logBuf.String()
			if tc.wantLog == "" {
				if strings.Contains(got, "cannot be decrypted") {
					t.Fatalf("unexpected decrypt ERROR:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, "level=ERROR") || !strings.Contains(got, tc.wantLog) || !strings.Contains(got, "MAILCUBBY_APP_KEY") {
				t.Fatalf("log missing ERROR %q naming MAILCUBBY_APP_KEY:\n%s", tc.wantLog, got)
			}
		})
	}
}

func seedCredentials(t *testing.T, dir string, keys [][]byte) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	proj, err := st.CreateProject(ctx, store.CreateProjectParams{
		Slug: "shop", Name: "Shop", Color: "blue", Settings: store.DefaultProjectSettings(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for i, key := range keys {
		c, err := secrets.NewSMTPCredentialCipher(key)
		if err != nil {
			t.Fatalf("NewSMTPCredentialCipher: %v", err)
		}
		username := "shop-cred000" + string(rune('a'+i))
		sealed, err := c.Seal([]byte("Password1234567890123456"), []byte(username))
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if _, err := st.CreateSMTPCredential(ctx, store.CreateSMTPCredentialParams{
			ProjectID: proj.ID, Label: username, Username: username, PasswordEnc: sealed, Now: now,
		}); err != nil {
			t.Fatalf("CreateSMTPCredential: %v", err)
		}
	}
}

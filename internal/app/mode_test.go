package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/store"
)

// seedServerDataDir creates a data dir that looks like a server-mode instance
// (one user).
func seedServerDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := st.CreateUser(context.Background(), store.CreateUserParams{
		Email: "owner@example.com",
		Name:  "Owner",
		Now:   time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	return dir
}

func localModeEnv(dir, mode string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_MODE":
			return mode, mode != ""
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_SMTP_ADDR", "MAILCUBBY_HTTP_ADDR":
			return "127.0.0.1:0", true
		default:
			return "", false
		}
	}
}

// SPEC 5: a data dir with users does not silently open in local mode.
func TestAppImplicitLocalModeWithUsers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mode     string
		seed     bool
		wantErr  bool
		wantWarn string
	}{
		{name: "unset mode with users refuses to start", mode: "", seed: true, wantErr: true},
		{name: "explicit local with users starts with a warning", mode: "local", seed: true, wantWarn: "switching from server mode to local mode"},
		{name: "unset mode without users starts", mode: "", seed: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.seed {
				dir = seedServerDataDir(t)
			}
			cfg, err := config.Load(localModeEnv(dir, tc.mode))
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			var logBuf bytes.Buffer
			a, err := app.Open(t.Context(), cfg, app.Options{
				Logger: slog.New(slog.NewTextHandler(&logBuf, nil)),
			})
			if tc.wantErr {
				if err == nil {
					_ = a.Close()
					t.Fatal("app.Open succeeded, want error")
				}
				if !errors.Is(err, app.ErrImplicitLocalMode) || !strings.Contains(err.Error(), "MAILCUBBY_MODE") {
					t.Fatalf("app.Open error = %v, want ErrImplicitLocalMode naming MAILCUBBY_MODE", err)
				}
				// A refused start must not leave a generated app.key behind.
				if _, statErr := os.Stat(filepath.Join(dir, "app.key")); !os.IsNotExist(statErr) {
					t.Fatalf("app.key exists after refused start (stat err = %v)", statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("app.Open: %v", err)
			}
			_ = a.Close()
			if tc.wantWarn != "" && !strings.Contains(logBuf.String(), tc.wantWarn) {
				t.Fatalf("log missing %q:\n%s", tc.wantWarn, logBuf.String())
			}
		})
	}
}

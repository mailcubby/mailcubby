package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/cli"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/store"
)

func TestAdminResetPasswordCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(now)

	st, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	user, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email:             "alice@example.test",
		Name:              "Alice",
		IsAdmin:           true,
		CanCreateProjects: true,
		Now:               now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	env := func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "https://mail.example.test", true
		default:
			return "", false
		}
	}

	// Happy path (FR-046, FR-115).
	var stdout, stderr bytes.Buffer
	code := cli.RunAdminWithClock(t.Context(), []string{"reset-password", "--email", "ALICE@example.test"}, &stdout, &stderr, env, clk)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}

	link := strings.TrimSpace(stdout.String())
	const prefix = "https://mail.example.test/reset/"
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("reset link = %q, want prefix %q", link, prefix)
	}
	rawToken := strings.TrimPrefix(link, prefix)
	if len(rawToken) < 32 {
		t.Fatalf("reset token %q unexpectedly short", rawToken)
	}

	// Verify hashed token and audit log in store.
	st2, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = st2.Close() }()

	rec, err := st2.GetPasswordResetByTokenHash(t.Context(), auth.HashToken(rawToken))
	if err != nil {
		t.Fatalf("GetPasswordResetByTokenHash: %v", err)
	}
	if rec.UserID != user.ID {
		t.Fatalf("reset user_id = %d, want %d", rec.UserID, user.ID)
	}
	if !rec.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("reset expires_at = %v, want %v", rec.ExpiresAt, now.Add(time.Hour))
	}
	entries, err := st2.ListAuditEntries(t.Context(), 10)
	if err != nil || len(entries) != 1 || entries[0].Action != "user.password_reset_link" {
		t.Fatalf("unexpected audit entries: %+v, err=%v", entries, err)
	}

	// Unknown user exits non-zero.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdminWithClock(t.Context(), []string{"reset-password", "--email", "nobody@example.test"}, &stdout, &stderr, env, clk)
	if code != 1 || !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("unknown user = (%d, %q), want (1, contains not found)", code, stderr.String())
	}

	// Missing --email exits 2.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdminWithClock(t.Context(), []string{"reset-password"}, &stdout, &stderr, env, clk)
	if code != 2 {
		t.Fatalf("missing email exit code = %d, want 2", code)
	}
}

func TestAdminCreateUserCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(now)

	env := func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return dir, true
		case "MAILCUBBY_BASE_URL":
			return "https://mail.example.test", true
		default:
			return "", false
		}
	}

	// FR-055, FR-115: create-user creates passwordless user + instance invite link.
	var stdout, stderr bytes.Buffer
	code := cli.RunAdminWithClock(t.Context(), []string{
		"create-user",
		"--email", "carol@example.test",
		"--name", "Carol Ops",
		"--admin",
	}, &stdout, &stderr, env, clk)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}

	link := strings.TrimSpace(stdout.String())
	const prefix = "https://mail.example.test/invite/"
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("invite link = %q, want prefix %q", link, prefix)
	}
	rawToken := strings.TrimPrefix(link, prefix)

	st, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	u, err := st.GetUserByEmail(t.Context(), "carol@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if u.PasswordHash != nil || !u.IsAdmin || !u.CanCreateProjects || u.Name != "Carol Ops" {
		t.Fatalf("unexpected created user: %+v", u)
	}

	inv, err := st.GetInviteByTokenHash(t.Context(), auth.HashToken(rawToken))
	if err != nil {
		t.Fatalf("GetInviteByTokenHash: %v", err)
	}
	if inv.ProjectID != nil || inv.Role != nil || inv.Email != "carol@example.test" {
		t.Fatalf("unexpected instance invite: %+v", inv)
	}

	// Duplicate email fails with exit code 1.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdminWithClock(t.Context(), []string{
		"create-user", "--email", "carol@example.test", "--name", "Duplicate",
	}, &stdout, &stderr, env, clk)
	if code != 1 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("duplicate create-user = (%d, %q), want (1, contains already exists)", code, stderr.String())
	}
}

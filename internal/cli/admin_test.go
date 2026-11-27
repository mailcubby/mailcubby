package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
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

func TestAdminListUsersCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	env := func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return dir, true
		}
		return "", false
	}

	// Empty database reports no users cleanly (FR-115).
	var stdout, stderr bytes.Buffer
	code := cli.RunAdmin(t.Context(), []string{"list-users"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("empty list-users exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No users found") {
		t.Fatalf("empty list-users stdout = %q, want 'No users found'", stdout.String())
	}

	// Seed active, disabled, pending-invite, and disabled-without-password users.
	st, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	storedHash := "$argon2id$v=19$m=19456,t=2,p=1$fake$hash"
	disabledAt := now.Add(-time.Hour)

	alice, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email:             "alice@example.test",
		Name:              "Alice Admin",
		PasswordHash:      &storedHash,
		IsAdmin:           true,
		CanCreateProjects: true,
		Now:               now,
	})
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email:             "bob@example.test",
		Name:              "Bob Disabled",
		PasswordHash:      &storedHash,
		IsAdmin:           false,
		CanCreateProjects: true,
		DisabledAt:        &disabledAt,
		Now:               now,
	})
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	carol, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email:             "carol@example.test",
		Name:              "Carol Pending",
		PasswordHash:      nil,
		IsAdmin:           false,
		CanCreateProjects: false,
		Now:               now,
	})
	if err != nil {
		t.Fatalf("CreateUser carol: %v", err)
	}
	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "shop",
		Name:     "Shop",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.AddProjectMember(t.Context(), proj.ID, alice.ID, "owner", now); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdmin(t.Context(), []string{"list-users"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("list-users exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (header + 3 users):\n%s", len(lines), stdout.String())
	}
	header := lines[0]
	for _, col := range []string{"ID", "EMAIL", "NAME", "ADMIN", "CAN_CREATE_PROJECTS", "PROJECTS", "STATUS"} {
		if !strings.Contains(header, col) {
			t.Errorf("header %q missing column %q", header, col)
		}
	}

	wantRows := []struct {
		lineIdx int
		fields  []string
	}{
		{
			lineIdx: 1,
			fields:  []string{strconv.FormatInt(alice.ID, 10), "alice@example.test", "Alice Admin", "yes", "1", "active"},
		},
		{
			lineIdx: 2,
			fields:  []string{strconv.FormatInt(bob.ID, 10), "bob@example.test", "Bob Disabled", "no", "yes", "0", "disabled"},
		},
		{
			lineIdx: 3,
			fields:  []string{strconv.FormatInt(carol.ID, 10), "carol@example.test", "Carol Pending", "no", "0", "pending-invite"},
		},
	}
	for _, want := range wantRows {
		row := lines[want.lineIdx]
		for _, f := range want.fields {
			if !strings.Contains(row, f) {
				t.Errorf("row %d (%q) missing %q", want.lineIdx, row, f)
			}
		}
	}

	// Extra positional arguments exit 2.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdmin(t.Context(), []string{"list-users", "extra"}, &stdout, &stderr, env)
	if code != 2 {
		t.Fatalf("extra arg exit code = %d, want 2", code)
	}
}

func TestAdminEnableUserCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(now)
	env := func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return dir, true
		}
		return "", false
	}

	st, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	storedHash := "$argon2id$v=19$m=19456,t=2,p=1$fake$hash"
	disabledAt := now.Add(-2 * time.Hour)
	disabledUser, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email:             "locked@example.test",
		Name:              "Locked Admin",
		PasswordHash:      &storedHash,
		IsAdmin:           true,
		CanCreateProjects: true,
		DisabledAt:        &disabledAt,
		Now:               now.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateUser disabled: %v", err)
	}
	activeUser, err := st.CreateUser(t.Context(), store.CreateUserParams{
		Email:             "active@example.test",
		Name:              "Active User",
		PasswordHash:      &storedHash,
		IsAdmin:           false,
		CanCreateProjects: false,
		Now:               now.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateUser active: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Happy path: enables disabled user (case-insensitive email) and writes user.enable audit entry (FR-105, FR-115).
	var stdout, stderr bytes.Buffer
	code := cli.RunAdminWithClock(t.Context(), []string{"enable-user", "--email", "LOCKED@example.test"}, &stdout, &stderr, env, clk)
	if code != 0 {
		t.Fatalf("enable-user exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "enabled") || !strings.Contains(stdout.String(), "locked@example.test") {
		t.Fatalf("enable-user stdout = %q, want confirmation naming locked@example.test", stdout.String())
	}

	st2, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	gotUser, err := st2.GetUserByID(t.Context(), disabledUser.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if gotUser.DisabledAt != nil {
		t.Fatalf("user DisabledAt = %v, want nil", gotUser.DisabledAt)
	}
	if gotUser.Name != "Locked Admin" || !gotUser.IsAdmin || !gotUser.CanCreateProjects {
		t.Fatalf("enable-user unexpectedly altered user fields: %+v", gotUser)
	}
	if !gotUser.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", gotUser.UpdatedAt, now)
	}

	entries, err := st2.ListAuditEntries(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Action != "user.enable" || e.ActorType != store.AuditActorSystem {
		t.Fatalf("audit entry = (action=%q, actor_type=%q), want (user.enable, system)", e.Action, e.ActorType)
	}
	if e.ActorLabel == nil || *e.ActorLabel != "cli" {
		t.Fatalf("audit actor_label = %v, want 'cli'", e.ActorLabel)
	}
	if e.TargetType == nil || *e.TargetType != "user" || e.TargetID == nil || *e.TargetID != strconv.FormatInt(disabledUser.ID, 10) {
		t.Fatalf("audit target = (%v, %v), want (user, %d)", e.TargetType, e.TargetID, disabledUser.ID)
	}
	if e.MetadataJSON == nil {
		t.Fatalf("audit metadata_json is nil, want email=locked@example.test")
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(*e.MetadataJSON), &meta); err != nil {
		t.Fatalf("unmarshal audit metadata %q: %v", *e.MetadataJSON, err)
	}
	if meta["email"] != "locked@example.test" {
		t.Fatalf("audit metadata = %+v, want email=locked@example.test", meta)
	}
	if err := st2.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Already-enabled user reports clearly without adding a second audit entry.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdminWithClock(t.Context(), []string{"enable-user", "--email", activeUser.Email}, &stdout, &stderr, env, clk)
	if code != 0 {
		t.Fatalf("already-enabled exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already enabled") || !strings.Contains(stdout.String(), activeUser.Email) {
		t.Fatalf("already-enabled stdout = %q, want message saying already enabled", stdout.String())
	}

	st3, err := store.Open(t.Context(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("reopen store 3: %v", err)
	}
	entries3, err := st3.ListAuditEntries(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListAuditEntries 3: %v", err)
	}
	if len(entries3) != 1 {
		t.Fatalf("already-enabled wrote unexpected audit entry: %+v", entries3)
	}
	if err := st3.Close(); err != nil {
		t.Fatalf("store.Close 3: %v", err)
	}

	// Missing user reports clearly and exits 1.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdminWithClock(t.Context(), []string{"enable-user", "--email", "missing@example.test"}, &stdout, &stderr, env, clk)
	if code != 1 || !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("missing user = (%d, %q), want (1, contains not found)", code, stderr.String())
	}

	// Missing --email exits 2.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunAdminWithClock(t.Context(), []string{"enable-user"}, &stdout, &stderr, env, clk)
	if code != 2 {
		t.Fatalf("missing --email exit code = %d, want 2", code)
	}
}

func TestAdminHelpCLI(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cli.RunAdmin(t.Context(), []string{"--help"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("admin --help exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	for _, want := range []string{"create-user", "list-users", "enable-user", "reset-password"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("admin help missing %q:\n%s", want, stdout.String())
		}
	}
}

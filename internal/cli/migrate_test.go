package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/store"
)

func TestRunMigrateStatusPendingAndApplied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	lookup := func(k string) (string, bool) {
		if k == "MAILCUBBY_DATA_DIR" {
			return dataDir, true
		}
		return "", false
	}

	// Before opening the store, migrations are reported as pending.
	var stdout, stderr bytes.Buffer
	code := RunMigrate(ctx, []string{"status"}, &stdout, &stderr, lookup)
	if code != 0 {
		t.Fatalf("RunMigrate(status) before open exit=%d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "00001_init.sql") || !strings.Contains(out, "00002_fts.sql") {
		t.Fatalf("output missing migration names:\n%s", out)
	}
	if !strings.Contains(out, "pending") {
		t.Fatalf("expected pending state before migrations:\n%s", out)
	}

	// Open store to apply migrations.
	st, err := store.Open(ctx, dataDir+"/"+store.DBFilename)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	stdout.Reset()
	stderr.Reset()
	code = RunMigrate(ctx, []string{"status", "--data-dir", dataDir}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("RunMigrate(status) after open exit=%d stderr=%q", code, stderr.String())
	}
	out = stdout.String()
	if strings.Contains(out, "pending") {
		t.Fatalf("expected no pending migrations after open:\n%s", out)
	}
	if strings.Count(out, "applied") < 2 {
		t.Fatalf("expected applied migrations in output:\n%s", out)
	}
}

func TestRunMigrateUsageErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	if code := RunMigrate(ctx, nil, &stdout, &stderr, nil); code != 2 {
		t.Fatalf("RunMigrate(nil) exit=%d, want 2", code)
	}
	if code := RunMigrate(ctx, []string{"down"}, &stdout, &stderr, nil); code != 2 {
		t.Fatalf("RunMigrate(down) exit=%d, want 2", code)
	}
	if code := RunMigrate(ctx, []string{"status", "extra"}, &stdout, &stderr, nil); code != 2 {
		t.Fatalf("RunMigrate(status extra) exit=%d, want 2", code)
	}
}

// Package testutil provides shared test helpers for Mailcubby packages.
package testutil

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mailcubby/mailcubby/internal/store"
)

// NewStore opens a freshly migrated SQLite store in a temporary directory
// scoped to t and registers cleanup to close it when the test completes.
func NewStore(t testing.TB) *store.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), store.DBFilename)
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("testutil.NewStore: open %s: %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("testutil.NewStore: close: %v", err)
		}
	})
	return st
}

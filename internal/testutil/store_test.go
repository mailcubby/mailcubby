package testutil

import (
	"context"
	"testing"
)

func TestNewStore(t *testing.T) {
	t.Parallel()

	st := NewStore(t)
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/secrets"
)

func TestRunKeygen(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := RunKeygen(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunKeygen exit code = %d, want 0 (stderr=%q)", code, stderr.String())
	}

	encoded := strings.TrimSpace(stdout.String())
	key, err := secrets.ParseAppKey(encoded)
	if err != nil {
		t.Fatalf("ParseAppKey(%q) failed: %v", encoded, err)
	}
	if len(key) != secrets.KeySize {
		t.Fatalf("len(key) = %d, want %d", len(key), secrets.KeySize)
	}
}

func TestRunKeygenRejectsArgs(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := RunKeygen([]string{"extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("RunKeygen(extra) exit code = %d, want 2", code)
	}
}

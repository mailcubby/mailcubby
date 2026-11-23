package cli_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/cli"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestRunHealthcheckRunningAndStopped(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)

	// FR-115: exits 0 against a running server.
	var stdout, stderr bytes.Buffer
	code := cli.RunHealthcheck(context.Background(), []string{"--url", "http://" + srv.HTTPAddr + "/readyz"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("healthcheck against running server = %d (stderr=%q), want 0", code, stderr.String())
	}

	// Also works using MAILCUBBY_HTTP_ADDR default URL resolution.
	stdout.Reset()
	stderr.Reset()
	code = cli.RunHealthcheck(context.Background(), nil, &stdout, &stderr, func(k string) (string, bool) {
		if k == "MAILCUBBY_HTTP_ADDR" {
			return srv.HTTPAddr, true
		}
		return "", false
	})
	if code != 0 {
		t.Fatalf("healthcheck via MAILCUBBY_HTTP_ADDR = %d (stderr=%q), want 0", code, stderr.String())
	}

	// Exits 1 against a 503 server.
	unready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unready.Close()
	stdout.Reset()
	stderr.Reset()
	code = cli.RunHealthcheck(context.Background(), []string{"--url", unready.URL + "/readyz"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("healthcheck against 503 = %d, want 1", code)
	}

	// Exits 1 when server is stopped (closed port).
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	closedAddr := ln.Addr().String()
	_ = ln.Close()

	stdout.Reset()
	stderr.Reset()
	code = cli.RunHealthcheck(context.Background(), []string{"--url", "http://" + closedAddr + "/readyz"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("healthcheck against stopped server = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "healthcheck:") {
		t.Errorf("expected error message on stderr, got %q", stderr.String())
	}
}

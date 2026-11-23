package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	defaultHTTPPort    = "8025"
	healthcheckTimeout = 2 * time.Second
)

// RunHealthcheck implements "mailcubby healthcheck [--url]" (SPEC FR-115).
// It exits 0 when /readyz responds with HTTP 200 within 2 seconds, else 1.
func RunHealthcheck(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	defaultURL := defaultHealthcheckURL(lookupEnv)

	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	targetURL := fs.String("url", defaultURL, "readiness URL to probe")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "healthcheck takes no positional arguments")
		return 2
	}

	reqCtx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, *targetURL, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "healthcheck: invalid url %q: %v\n", *targetURL, err)
		return 1
	}

	client := &http.Client{
		Timeout: healthcheckTimeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "healthcheck: unexpected status %d from %s\n", resp.StatusCode, *targetURL)
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "ok")
	return 0
}

func defaultHealthcheckURL(lookupEnv func(string) (string, bool)) string {
	port := defaultHTTPPort
	if lookupEnv != nil {
		if raw, ok := lookupEnv("MAILCUBBY_HTTP_ADDR"); ok && strings.TrimSpace(raw) != "" {
			if _, p, err := net.SplitHostPort(strings.TrimSpace(raw)); err == nil && p != "" {
				port = p
			}
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%s/readyz", port)
}

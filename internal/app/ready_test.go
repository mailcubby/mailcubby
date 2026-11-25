package app_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
)

// The startup ready line names the URL, the SMTP address and the absolute data
// dir, even when MAILCUBBY_DATA_DIR is relative. Not parallel: it uses t.Chdir.
func TestAppLogsReadyLine(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)

	cfg, err := config.Load(func(k string) (string, bool) {
		switch k {
		case "MAILCUBBY_DATA_DIR":
			return "./data", true
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
	ctx, cancel := context.WithCancel(t.Context())
	a, err := app.Open(ctx, cfg, app.Options{Logger: slog.New(slog.NewTextHandler(&logBuf, nil))})
	if err != nil {
		cancel()
		t.Fatalf("app.Open: %v", err)
	}
	_, httpPort, _ := net.SplitHostPort(a.HTTPAddr())
	_, smtpPort, _ := net.SplitHostPort(a.SMTPAddr())
	cancel()
	if err := a.Serve(ctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// t.TempDir may sit behind a symlink (macOS /var -> /private/var); the line
	// uses filepath.Abs of the configured path, so build the expectation the same way.
	wantDataDir, err := filepath.Abs("./data")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	var readyLine string
	for line := range strings.SplitSeq(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="mailcubby ready"`) {
			readyLine = line
		}
	}
	if readyLine == "" {
		t.Fatalf("no ready line in log:\n%s", logBuf.String())
	}
	for _, want := range []string{
		"url=http://localhost:" + httpPort,
		"smtp=localhost:" + smtpPort,
		"data_dir=" + wantDataDir,
	} {
		if !strings.Contains(readyLine, want) {
			t.Errorf("ready line %q missing %q", readyLine, want)
		}
	}
}

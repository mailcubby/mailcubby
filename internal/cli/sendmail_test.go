package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/cli"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestRunSendmail_FixtureWithTAndIgnoredFlags(t *testing.T) {
	t.Parallel()

	// FR-115: mailcubby sendmail -t -i < testdata/mime/<fixture>.eml stores message.
	srv := testutil.StartServer(t, nil)

	fixturePath := filepath.Join("..", "..", "testdata", "mime", "02_multipart_alternative.eml")
	//nolint:gosec // G304: static repo fixture path
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.RunSendmail(
		[]string{"-t", "-i", "-oi", "-odi", "-bm", "-F", "PHP Mailer", "--smtp", srv.SMTPAddr, "--user", "Shop Staging", "--password", "dev"},
		bytes.NewReader(raw),
		&stdout,
		&stderr,
		func(string) (string, bool) { return "", false },
	)
	if code != 0 {
		t.Fatalf("RunSendmail exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}

	ctx := context.Background()
	proj, err := srv.Store.GetProjectBySlug(ctx, "shop-staging")
	if err != nil {
		t.Fatalf("GetProjectBySlug(shop-staging): %v", err)
	}
	msgs, err := srv.Store.ListMessages(ctx, proj.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if msgs[0].Subject != "Order #1042 confirmed" {
		t.Fatalf("Subject = %q, want %q", msgs[0].Subject, "Order #1042 confirmed")
	}
	if msgs[0].EnvelopeFrom != "support@shop.example.test" {
		t.Fatalf("EnvelopeFrom = %q, want support@shop.example.test", msgs[0].EnvelopeFrom)
	}
	// -t extracts both To and Cc from headers.
	if len(msgs[0].EnvelopeTo) != 2 {
		t.Fatalf("EnvelopeTo = %v, want 2 recipients (To + Cc)", msgs[0].EnvelopeTo)
	}
}

func TestRunSendmail_AllGoldenFixturesWithT(t *testing.T) {
	t.Parallel()

	// Verifies make seed compatibility: every testdata/mime/*.eml works with sendmail -t.
	srv := testutil.StartServer(t, nil)

	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "mime", "*.eml"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(matches) < 15 {
		t.Fatalf("expected >=15 fixtures, found %d", len(matches))
	}

	env := func(k string) (string, bool) {
		if k == "MAILCUBBY_SENDMAIL_SMTP" {
			return srv.SMTPAddr, true
		}
		return "", false
	}

	for _, path := range matches {
		//nolint:gosec // G304: fixture paths from repo testdata/mime/*.eml
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var stdout, stderr bytes.Buffer
		code := cli.RunSendmail([]string{"-t", "--user", "seed-proj", "--password", "dev"}, bytes.NewReader(raw), &stdout, &stderr, env)
		if code != 0 {
			t.Fatalf("sendmail failed on %s: code=%d stderr=%q", filepath.Base(path), code, stderr.String())
		}
	}

	proj, err := srv.Store.GetProjectBySlug(context.Background(), "seed-proj")
	if err != nil {
		t.Fatalf("GetProjectBySlug(seed-proj): %v", err)
	}
	if int(proj.MessageCount) != len(matches) {
		t.Fatalf("MessageCount = %d, want %d", proj.MessageCount, len(matches))
	}
}

func TestRunSendmail_PositionalRecipientsAndExplicitFromAndErrors(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)

	var stdout, stderr bytes.Buffer
	code := cli.RunSendmail(
		[]string{"-fcustom@example.test", "--smtp=" + srv.SMTPAddr, "rcpt@example.test"},
		strings.NewReader("Subject: Direct\r\n\r\nHello\r\n"),
		&stdout,
		&stderr,
		nil,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	defProj, err := srv.Store.GetProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	msgs, err := srv.Store.ListMessages(context.Background(), defProj.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].EnvelopeFrom != "custom@example.test" {
		t.Fatalf("unexpected msgs: %+v", msgs)
	}

	// Missing recipients returns exit code 2.
	stderr.Reset()
	code = cli.RunSendmail([]string{"--smtp", srv.SMTPAddr}, strings.NewReader("Subject: x\r\n\r\nhi\r\n"), &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("missing recipients exit code = %d, want 2", code)
	}

	// Unreachable SMTP returns non-zero with error on stderr.
	stderr.Reset()
	code = cli.RunSendmail([]string{"--smtp", "127.0.0.1:1", "a@b.test"}, strings.NewReader("Subject: x\r\n\r\nhi\r\n"), &stdout, &stderr, nil)
	if code != 1 || stderr.Len() == 0 {
		t.Fatalf("unreachable SMTP: code=%d stderr=%q, want code=1 with stderr", code, stderr.String())
	}
}

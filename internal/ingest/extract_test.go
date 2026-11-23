package ingest_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/ingest"
)

func TestParseRawHeadersOrderAndUnfolding(t *testing.T) {
	t.Parallel()

	raw := "Received: from a.example.test\r\n" +
		" by b.example.test; Mon, 23 Nov 2026 14:00:00 +0000\r\n" +
		"From: Alice <alice@example.test>\r\n" +
		"X-Custom: one\r\n" +
		"X-Custom: two\r\n" +
		"Subject: Folded\r\n" +
		"\tsubject line\r\n" +
		"\r\n" +
		"Body text here.\r\n"

	headers, err := ingest.ParseRawHeaders(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseRawHeaders: %v", err)
	}

	want := []ingest.Header{
		{Name: "Received", Value: "from a.example.test by b.example.test; Mon, 23 Nov 2026 14:00:00 +0000"},
		{Name: "From", Value: "Alice <alice@example.test>"},
		{Name: "X-Custom", Value: "one"},
		{Name: "X-Custom", Value: "two"},
		{Name: "Subject", Value: "Folded subject line"},
	}

	if len(headers) != len(want) {
		t.Fatalf("got %d headers, want %d: %+v", len(headers), len(want), headers)
	}
	for i := range want {
		if headers[i] != want[i] {
			t.Errorf("header[%d] = %+v, want %+v", i, headers[i], want[i])
		}
	}
}

func TestExtractMessageContentTruncationUTF8(t *testing.T) {
	t.Parallel()

	// "João" has a 2-byte UTF-8 rune 'ã' (0xC3 0xA3).
	raw := "From: a@example.test\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"João likes mailcubby"

	content, err := ingest.ExtractMessageContent(strings.NewReader(raw), 3)
	if err != nil {
		t.Fatalf("ExtractMessageContent: %v", err)
	}
	if !content.TextTruncated {
		t.Fatalf("expected TextTruncated=true")
	}
	// Bytes: 'J'(0), 'o'(1), 0xC3(2), 0xA3(3) -> cutting at 3 must back up to 2 ("Jo").
	if content.Text != "Jo" {
		t.Fatalf("Text = %q, want %q", content.Text, "Jo")
	}
	if len(content.Headers) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(content.Headers))
	}
}

func TestExtractAttachmentContent(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"From: a@example.test",
		"To: b@example.test",
		"Subject: Attachment test",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Hello",
		"--mix",
		`Content-Type: text/html; name="x.html"`,
		`Content-Disposition: attachment; filename="x.html"`,
		"",
		"<script>alert(1)</script>",
		"--mix--",
		"",
	}, "\r\n")

	parsed := ingest.ParseMessage(strings.NewReader(raw), []string{"b@example.test"})
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]

	data, err := ingest.ExtractAttachmentContent(strings.NewReader(raw), att.PartPath, att.SHA256)
	if err != nil {
		t.Fatalf("ExtractAttachmentContent: %v", err)
	}
	if !bytes.Equal(data, []byte("<script>alert(1)</script>")) {
		t.Fatalf("attachment content = %q, want %q", string(data), "<script>alert(1)</script>")
	}
}

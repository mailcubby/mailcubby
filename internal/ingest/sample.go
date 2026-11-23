package ingest

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"time"
)

//go:embed assets/sample.eml
var sampleEML []byte

// SendTest ingests the built-in sample message (HTML + text, inline PNG, PDF
// attachment, link) directly through the ingest pipeline with a fresh Date and
// Message-ID (SPEC FR-077).
func (in *Ingestor) SendTest(ctx context.Context, projectID int64, projectSlug string) (MessageRef, error) {
	now := in.clk.Now().UTC()
	id, err := in.nextULID(now)
	if err != nil {
		return MessageRef{}, fmt.Errorf("ingest: generate sample message id: %w", err)
	}

	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	_, _ = fmt.Fprintf(&buf, "Message-ID: <sample-%s@mailcubby.test>\r\n", id.String())
	// Ensure CRLF line endings for the embedded fixture.
	normalized := bytes.ReplaceAll(sampleEML, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ReplaceAll(normalized, []byte("\n"), []byte("\r\n"))
	buf.Write(normalized)

	env := Envelope{
		MailFrom: "demo@mailcubby.test",
		RcptTo:   []string{"dev@example.test"},
		RemoteIP: "127.0.0.1",
		HELO:     "mailcubby.local",
	}
	targets := []Target{
		{
			ProjectID:       projectID,
			ProjectSlug:     projectSlug,
			PlusAddressTags: true,
		},
	}

	refs, err := in.Ingest(ctx, env, targets, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return MessageRef{}, err
	}
	return refs[0], nil
}

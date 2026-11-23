package ingest

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/jhillyerd/enmime/v2"
)

const maxHeaderBytes = 1 << 20

// ErrAttachmentNotFound is returned when a requested attachment part cannot be located in a message blob.
var ErrAttachmentNotFound = errors.New("ingest: attachment part not found")

// Header represents a single top-level RFC 5322 message header in original order.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MessageContent holds on-demand extracted message bodies and headers for API detail views.
type MessageContent struct {
	Text          string
	TextTruncated bool
	HTML          string
	Headers       []Header
}

// ParseRawHeaders reads the top-level RFC 5322 header section from r and returns
// headers in their original order with folded continuation lines unfolded.
func ParseRawHeaders(r io.Reader) ([]Header, error) {
	hdrBytes, err := readHeaderBlock(r)
	if err != nil {
		return nil, err
	}
	return parseHeaderBytes(hdrBytes), nil
}

// ExtractMessageContent parses headers and body parts from a raw message stream.
// If maxTextBytes > 0 and the text body exceeds maxTextBytes, Text is truncated on
// a valid UTF-8 boundary and TextTruncated is set to true (SPEC 8.4).
func ExtractMessageContent(r io.Reader, maxTextBytes int) (MessageContent, error) {
	br := bufio.NewReader(r)
	hdrBytes, err := readHeaderBlock(br)
	if err != nil {
		return MessageContent{}, fmt.Errorf("ingest: read headers: %w", err)
	}
	headers := parseHeaderBytes(hdrBytes)

	env, err := enmime.NewParser().ReadEnvelope(io.MultiReader(bytes.NewReader(hdrBytes), br))
	if err != nil {
		// Malformed MIME still returns parsed headers and empty bodies (FR-007).
		return MessageContent{
			Headers: headers,
		}, nil
	}

	text, truncated := truncateUTF8(env.Text, maxTextBytes)
	return MessageContent{
		Text:          text,
		TextTruncated: truncated,
		HTML:          env.HTML,
		Headers:       headers,
	}, nil
}

// ExtractHTML parses a raw message stream and returns its decoded HTML part.
func ExtractHTML(r io.Reader) (string, error) {
	env, err := enmime.NewParser().ReadEnvelope(r)
	if err != nil {
		return "", fmt.Errorf("ingest: parse html: %w", err)
	}
	return env.HTML, nil
}

// ExtractAttachmentContent parses a raw message stream and returns the decoded
// bytes of the attachment matching partPath and sha256Hex.
func ExtractAttachmentContent(r io.Reader, partPath, sha256Hex string) ([]byte, error) {
	env, err := enmime.NewParser().ReadEnvelope(r)
	if err != nil {
		return nil, fmt.Errorf("ingest: parse attachment message: %w", err)
	}
	parts := collectAttachmentParts(env)
	var shaFallback []byte
	for i, p := range parts {
		sum := sha256.Sum256(p.Content)
		gotSHA := hex.EncodeToString(sum[:])
		if attachmentPartPath(p, i) == partPath && (sha256Hex == "" || gotSHA == sha256Hex) {
			return p.Content, nil
		}
		if sha256Hex != "" && gotSHA == sha256Hex && shaFallback == nil {
			shaFallback = p.Content
		}
	}
	if shaFallback != nil {
		return shaFallback, nil
	}
	return nil, ErrAttachmentNotFound
}

func readHeaderBlock(r io.Reader) ([]byte, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	var buf bytes.Buffer
	for buf.Len() < maxHeaderBytes {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			remaining := maxHeaderBytes - buf.Len()
			if len(line) > remaining {
				buf.Write(line[:remaining])
				break
			}
			buf.Write(line)
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(trimmed) == 0 {
				break
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func parseHeaderBytes(raw []byte) []Header {
	headers := make([]Header, 0)
	lines := bytes.Split(raw, []byte("\n"))
	for _, rawLine := range lines {
		line := string(bytes.TrimRight(rawLine, "\r"))
		if line == "" {
			break
		}
		if (line[0] == ' ' || line[0] == '\t') && len(headers) > 0 {
			cont := strings.TrimSpace(line)
			if cont != "" {
				if headers[len(headers)-1].Value != "" {
					headers[len(headers)-1].Value += " " + cont
				} else {
					headers[len(headers)-1].Value = cont
				}
			}
			continue
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		headers = append(headers, Header{
			Name:  name,
			Value: strings.TrimSpace(val),
		})
	}
	return headers
}

func truncateUTF8(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/jhillyerd/enmime/v2"

	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	maxSnippetRunes = 200
	maxTagRunes     = 40
)

// ParsedAttachment holds extracted metadata for one attachment or inline part.
type ParsedAttachment struct {
	PartPath    string `json:"part_path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentID   string `json:"content_id,omitempty"`
	Disposition string `json:"disposition"`
	SHA256      string `json:"sha256"`
}

// ParsedMessage holds all metadata extracted from a raw RFC 5322 / MIME message.
type ParsedMessage struct {
	Subject         string             `json:"subject"`
	FromName        string             `json:"from_name"`
	FromAddr        string             `json:"from_addr"`
	To              []store.Address    `json:"to"`
	Cc              []store.Address    `json:"cc"`
	Bcc             []store.Address    `json:"bcc"`
	ReplyTo         []store.Address    `json:"reply_to"`
	MessageIDHeader string             `json:"message_id_header,omitempty"`
	DateHeader      *time.Time         `json:"date_header,omitempty"`
	Snippet         string             `json:"snippet"`
	HasHTML         bool               `json:"has_html"`
	HasText         bool               `json:"has_text"`
	TextGenerated   bool               `json:"text_generated"`
	TextBody        string             `json:"-"`
	Attachments     []ParsedAttachment `json:"attachments"`
	ParseWarnings   []string           `json:"parse_warnings,omitempty"`
	XTags           []string           `json:"x_tags,omitempty"`
}

// ParseMessage parses raw MIME email content from r using enmime v2.
// Per FR-007, parsing never rejects a message: malformed MIME produces a valid
// ParsedMessage with warnings recorded in ParseWarnings.
func ParseMessage(r io.Reader, rcptTo []string) ParsedMessage {
	parser := enmime.NewParser()
	env, err := parser.ReadEnvelope(r)
	if err != nil {
		return fallbackParse(err, rcptTo)
	}

	var warnings []string
	for _, e := range env.Errors {
		if e == nil {
			continue
		}
		// HTML-only down-conversion is tracked explicitly via TextGenerated (FR-009).
		if e.Name == enmime.ErrorPlainTextFromHTML {
			continue
		}
		warnings = append(warnings, formatEnmimeError(e))
	}

	subject := collapseNewlines(env.GetHeader("Subject"))

	var fromName, fromAddr string
	if fromList, err := env.AddressList("From"); err == nil && len(fromList) > 0 {
		fromName = strings.TrimSpace(fromList[0].Name)
		fromAddr = strings.TrimSpace(fromList[0].Address)
	} else if rawFrom := strings.TrimSpace(env.GetHeader("From")); rawFrom != "" {
		fromAddr = rawFrom
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("From header: %v", err))
		}
	}

	toList := extractAddressList(env, "To", &warnings)
	ccList := extractAddressList(env, "Cc", &warnings)
	replyToList := extractAddressList(env, "Reply-To", &warnings)
	bccList := computeBcc(rcptTo, toList, ccList)

	msgIDHdr := strings.TrimSpace(env.GetHeader("Message-ID"))

	var dateHdr *time.Time
	if rawDate := strings.TrimSpace(env.GetHeader("Date")); rawDate != "" {
		if parsedDate, err := env.Date(); err == nil {
			utc := parsedDate.UTC()
			dateHdr = &utc
		} else {
			warnings = append(warnings, fmt.Sprintf("Date header: %v", err))
		}
	}

	hasHTML := env.HTML != "" || hasBodyPartOfType(env.Root, "text/html")
	hasOriginalText := hasOriginalPlainText(env)
	textGenerated := !hasOriginalText && hasHTML && env.Text != ""

	snippet := buildSnippet(env.Text)
	attachments := extractAttachments(env)
	xTags := extractXTags(env)

	return ParsedMessage{
		Subject:         subject,
		FromName:        fromName,
		FromAddr:        fromAddr,
		To:              toList,
		Cc:              ccList,
		Bcc:             bccList,
		ReplyTo:         replyToList,
		MessageIDHeader: msgIDHdr,
		DateHeader:      dateHdr,
		Snippet:         snippet,
		HasHTML:         hasHTML,
		HasText:         hasOriginalText,
		TextGenerated:   textGenerated,
		TextBody:        env.Text,
		Attachments:     attachments,
		ParseWarnings:   warnings,
		XTags:           xTags,
	}
}

func fallbackParse(parseErr error, rcptTo []string) ParsedMessage {
	return ParsedMessage{
		To:            []store.Address{},
		Cc:            []store.Address{},
		Bcc:           computeBcc(rcptTo, nil, nil),
		ReplyTo:       []store.Address{},
		Attachments:   []ParsedAttachment{},
		ParseWarnings: []string{fmt.Sprintf("MIME parse error: %v", parseErr)},
	}
}

func formatEnmimeError(e *enmime.Error) string {
	if e.Detail == "" {
		return e.Name
	}
	return fmt.Sprintf("%s: %s", e.Name, e.Detail)
}

func extractAddressList(env *enmime.Envelope, header string, warnings *[]string) []store.Address {
	addrs, err := env.AddressList(header)
	if err != nil {
		if raw := strings.TrimSpace(env.GetHeader(header)); raw != "" {
			// Fallback to Go stdlib mail.ParseAddressList if enmime errored.
			if stdAddrs, stdErr := mail.ParseAddressList(raw); stdErr == nil {
				return convertMailAddresses(stdAddrs)
			}
			*warnings = append(*warnings, fmt.Sprintf("%s header: %v", header, err))
		}
		return []store.Address{}
	}
	return convertMailAddresses(addrs)
}

func convertMailAddresses(addrs []*mail.Address) []store.Address {
	if len(addrs) == 0 {
		return []store.Address{}
	}
	out := make([]store.Address, 0, len(addrs))
	for _, a := range addrs {
		if a == nil {
			continue
		}
		out = append(out, store.Address{
			Name:    strings.TrimSpace(a.Name),
			Address: strings.TrimSpace(a.Address),
		})
	}
	return out
}

func computeBcc(rcptTo []string, toList, ccList []store.Address) []store.Address {
	if len(rcptTo) == 0 {
		return []store.Address{}
	}
	visible := make(map[string]struct{}, len(toList)+len(ccList))
	for _, a := range toList {
		if a.Address != "" {
			visible[strings.ToLower(strings.TrimSpace(a.Address))] = struct{}{}
		}
	}
	for _, a := range ccList {
		if a.Address != "" {
			visible[strings.ToLower(strings.TrimSpace(a.Address))] = struct{}{}
		}
	}

	seen := make(map[string]struct{}, len(rcptTo))
	bcc := make([]store.Address, 0)
	for _, rcpt := range rcptTo {
		addr := strings.TrimSpace(strings.Trim(rcpt, "<>"))
		if addr == "" {
			continue
		}
		key := strings.ToLower(addr)
		if _, ok := visible[key]; ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		bcc = append(bcc, store.Address{Address: addr})
	}
	return bcc
}

func hasOriginalPlainText(env *enmime.Envelope) bool {
	if env.Root == nil {
		return false
	}
	if hasBodyPartOfType(env.Root, "text/plain") {
		return true
	}
	// A single-part message with no Content-Type defaults to text/plain per RFC 5322.
	if env.Root.FirstChild == nil && !strings.HasPrefix(strings.ToLower(env.Root.ContentType), "multipart/") {
		ct := strings.ToLower(strings.TrimSpace(env.Root.ContentType))
		if (ct == "" || ct == "text/plain") && env.Root.Disposition != "attachment" && len(env.Root.Content) > 0 {
			return true
		}
	}
	return false
}

func hasBodyPartOfType(root *enmime.Part, mediaType string) bool {
	if root == nil {
		return false
	}
	found := false
	_ = root.DepthMatchAll(func(p *enmime.Part) bool {
		if strings.EqualFold(p.ContentType, mediaType) && !strings.EqualFold(p.Disposition, "attachment") {
			found = true
		}
		return false
	})
	return found
}

func collectAttachmentParts(env *enmime.Envelope) []*enmime.Part {
	// Collect candidate parts from Attachments, Inlines, and OtherParts in a
	// deterministic order while deduplicating by part pointer.
	seen := make(map[*enmime.Part]struct{})
	var parts []*enmime.Part

	add := func(list []*enmime.Part, isInlineList bool) {
		for _, p := range list {
			if p == nil {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			// Do not classify bare inline text/plain or text/html body parts as attachments.
			ct := strings.ToLower(strings.TrimSpace(p.ContentType))
			if isInlineList && (ct == "text/plain" || ct == "text/html") && p.FileName == "" && p.ContentID == "" {
				continue
			}
			seen[p] = struct{}{}
			parts = append(parts, p)
		}
	}

	add(env.Attachments, false)
	add(env.Inlines, true)
	add(env.OtherParts, false)
	return parts
}

func attachmentPartPath(p *enmime.Part, idx int) string {
	partPath := strings.TrimSpace(p.PartID)
	if partPath == "" {
		return fmt.Sprintf("%d", idx+1)
	}
	return partPath
}

func extractAttachments(env *enmime.Envelope) []ParsedAttachment {
	parts := collectAttachmentParts(env)
	if len(parts) == 0 {
		return []ParsedAttachment{}
	}

	out := make([]ParsedAttachment, 0, len(parts))
	for i, p := range parts {
		ct := strings.ToLower(strings.TrimSpace(p.ContentType))
		if ct == "" {
			ct = "application/octet-stream"
		}
		cid := strings.TrimSpace(strings.Trim(p.ContentID, "<>"))
		disp := "attachment"
		if strings.EqualFold(p.Disposition, "inline") || (p.Disposition == "" && cid != "") {
			disp = "inline"
		}
		sum := sha256.Sum256(p.Content)
		out = append(out, ParsedAttachment{
			PartPath:    attachmentPartPath(p, i),
			Filename:    strings.TrimSpace(p.FileName),
			ContentType: ct,
			SizeBytes:   int64(len(p.Content)),
			ContentID:   cid,
			Disposition: disp,
			SHA256:      hex.EncodeToString(sum[:]),
		})
	}
	return out
}

func buildSnippet(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	collapsed := strings.Join(fields, " ")
	runes := []rune(collapsed)
	if len(runes) <= maxSnippetRunes {
		return collapsed
	}
	return string(runes[:maxSnippetRunes])
}

func collapseNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func extractXTags(env *enmime.Envelope) []string {
	values := env.GetHeaderValues("X-Tags")
	if len(values) == 0 {
		return nil
	}
	var raw []string
	for _, val := range values {
		raw = append(raw, strings.Split(val, ",")...)
	}
	return sanitizeAndDedupeTags(raw)
}

// SanitizeTag normalizes a raw tag name per FR-085 / FR-086: trims whitespace,
// replaces runes outside [\p{L}\p{N}_.-] with '-', and caps length at 40 runes.
func SanitizeTag(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var buf bytes.Buffer
	count := 0
	for _, r := range trimmed {
		if count >= maxTagRunes {
			break
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '.' || r == '-' {
			buf.WriteRune(r)
		} else {
			buf.WriteByte('-')
		}
		count++
	}
	return buf.String()
}

func sanitizeAndDedupeTags(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		tag := SanitizeTag(item)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

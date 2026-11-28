package linkcheck

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"

	"github.com/jhillyerd/enmime/v2"
	"golang.org/x/net/html"

	"github.com/mailcubby/mailcubby/internal/ingest"
)

const (
	// MaxHTMLBytes caps how many bytes of HTML are tokenized during link extraction.
	MaxHTMLBytes = 2 << 20 // 2 MiB
	// MaxHTMLTokens caps the number of HTML tokens processed per message.
	MaxHTMLTokens = 200_000
	// MaxTextBytes caps how many bytes of plain text are scanned for URLs.
	MaxTextBytes = 1 << 20 // 1 MiB
	// MaxExtractedLinks caps unique deduplicated links returned per message.
	MaxExtractedLinks = 2000
	// maxAnchorTextRunes caps captured anchor label length.
	maxAnchorTextRunes = 200
)

// ExtractFromBlob opens a message blob and extracts all links offline from HTML,
// plain text (when not derived from HTML), and List-Unsubscribe headers.
func ExtractFromBlob(ctx context.Context, blobs BlobOpener, blobPath string) (ExtractResult, error) {
	if err := ctx.Err(); err != nil {
		return ExtractResult{}, err
	}
	if blobs == nil {
		return ExtractResult{}, errors.New("linkcheck: blob opener is not configured")
	}

	rc, err := blobs.Open(blobPath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("linkcheck: open blob: %w", err)
	}
	defer func() { _ = rc.Close() }()

	br := bufio.NewReader(rc)
	hdrBytes, err := readHeaderBlock(br)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("linkcheck: read blob headers: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ExtractResult{}, err
	}

	rawHeaders, _ := ingest.ParseRawHeaders(bytes.NewReader(hdrBytes))
	var listUnsub []string
	for _, h := range rawHeaders {
		if strings.EqualFold(h.Name, "List-Unsubscribe") && strings.TrimSpace(h.Value) != "" {
			listUnsub = append(listUnsub, h.Value)
		}
	}

	env, parseErr := enmime.NewParser().ReadEnvelope(io.MultiReader(bytes.NewReader(hdrBytes), br))
	if parseErr != nil {
		// Malformed MIME: still return any List-Unsubscribe header links (FR-007).
		return Extract("", "", listUnsub), nil
	}

	// Only scan env.Text when the message actually has a text/plain part,
	// not when enmime down-converted HTML to text (avoiding duplicate "text" sources).
	plainText := ""
	if hasOriginalPlainText(env) {
		plainText = env.Text
	}

	return Extract(env.HTML, plainText, listUnsub), nil
}

const maxHeaderReadBytes = 1 << 20

func readHeaderBlock(br *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for buf.Len() < maxHeaderReadBytes {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			remaining := maxHeaderReadBytes - buf.Len()
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

// Extract extracts and deduplicates links from HTML (<a href>, <area href>),
// plain text, and List-Unsubscribe header values entirely offline.
func Extract(rawHTML, rawText string, listUnsubscribeHeaders []string) ExtractResult {
	acc := newLinkAccumulator()

	if rawHTML != "" {
		extractHTMLLinks(rawHTML, acc)
	}
	if rawText != "" {
		extractTextLinks(rawText, acc)
	}
	for _, hdr := range listUnsubscribeHeaders {
		extractListUnsubscribeLinks(hdr, acc)
	}

	return acc.finish()
}

type linkRecord struct {
	url       string
	text      string
	hasHTML   bool
	hasText   bool
	hasHeader bool
	count     int
	scheme    string
	checkable bool
	warnings  []Warning
}

type linkAccumulator struct {
	order     []*linkRecord
	byURL     map[string]*linkRecord
	truncated bool
}

func newLinkAccumulator() *linkAccumulator {
	return &linkAccumulator{
		byURL: make(map[string]*linkRecord),
	}
}

func (a *linkAccumulator) add(rawURL, text string, src Source, missingHref bool) {
	u := strings.TrimSpace(rawURL)
	if missingHref {
		u = ""
	}
	cleanedText := normalizeAnchorText(text)

	if rec, ok := a.byURL[u]; ok {
		rec.count++
		if rec.text == "" && cleanedText != "" {
			rec.text = cleanedText
		}
		markSource(rec, src)
		return
	}

	if len(a.order) >= MaxExtractedLinks {
		a.truncated = true
		return
	}

	scheme, checkable, warnings := classifyExtractedURL(u, missingHref)
	rec := &linkRecord{
		url:       u,
		text:      cleanedText,
		count:     1,
		scheme:    scheme,
		checkable: checkable,
		warnings:  warnings,
	}
	markSource(rec, src)
	a.order = append(a.order, rec)
	a.byURL[u] = rec
}

func markSource(rec *linkRecord, src Source) {
	switch src {
	case SourceHTML:
		rec.hasHTML = true
	case SourceText:
		rec.hasText = true
	case SourceHeader:
		rec.hasHeader = true
	}
}

func (a *linkAccumulator) finish() ExtractResult {
	links := make([]ExtractedLink, 0, len(a.order))
	checkableCount := 0
	for _, rec := range a.order {
		sources := make([]Source, 0, 3)
		if rec.hasHTML {
			sources = append(sources, SourceHTML)
		}
		if rec.hasText {
			sources = append(sources, SourceText)
		}
		if rec.hasHeader {
			sources = append(sources, SourceHeader)
		}
		warnings := rec.warnings
		if warnings == nil {
			warnings = []Warning{}
		}
		if rec.checkable {
			checkableCount++
		}
		links = append(links, ExtractedLink{
			URL:       rec.url,
			Text:      rec.text,
			Sources:   sources,
			Count:     rec.count,
			Scheme:    rec.scheme,
			Checkable: rec.checkable,
			Warnings:  warnings,
		})
	}
	return ExtractResult{
		Links:          links,
		Total:          len(links),
		CheckableCount: checkableCount,
		Truncated:      a.truncated,
	}
}

func classifyExtractedURL(rawURL string, missingHref bool) (scheme string, checkable bool, warnings []Warning) {
	if missingHref || rawURL == "" {
		return "", false, []Warning{WarningMissingHref}
	}

	// Strip ASCII tabs/newlines and leading/trailing C0 controls before scheme detection
	// to catch browser-normalized obfuscations like "java\tscript:alert(1)".
	sanitized := stripURLControlWhitespace(rawURL)
	scheme = extractScheme(sanitized)

	warnings = make([]Warning, 0, 2)
	switch scheme {
	case "javascript":
		warnings = append(warnings, WarningJavaScriptURL)
		return scheme, false, warnings
	case "data":
		warnings = append(warnings, WarningDataURL)
		return scheme, false, warnings
	case "http":
		warnings = append(warnings, WarningHTTPInsecure)
		_, parseErr := url.Parse(rawURL)
		checkable = parseErr == nil && len(rawURL) <= MaxURLBytes
	case "https":
		_, parseErr := url.Parse(rawURL)
		checkable = parseErr == nil && len(rawURL) <= MaxURLBytes
	default:
		checkable = false
	}

	if parsed, err := url.Parse(sanitized); err == nil && parsed.User != nil {
		warnings = append(warnings, WarningCredentialsInURL)
	}

	return scheme, checkable, warnings
}

func stripURLControlWhitespace(s string) string {
	// Trim leading and trailing ASCII C0 controls and spaces.
	trimmed := strings.TrimFunc(s, func(r rune) bool {
		return r <= 0x20
	})
	if !strings.ContainsAny(trimmed, "\t\n\r") {
		return trimmed
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func extractScheme(s string) string {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return ""
	}
	// RFC 3986 scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
	first := s[0]
	if (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') {
		return ""
	}
	for i := 1; i < colon; i++ {
		c := s[i]
		isAlpha := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !isDigit && c != '+' && c != '-' && c != '.' {
			return ""
		}
	}
	return strings.ToLower(s[:colon])
}

func extractHTMLLinks(rawHTML string, acc *linkAccumulator) {
	if len(rawHTML) > MaxHTMLBytes {
		rawHTML = rawHTML[:MaxHTMLBytes]
		acc.truncated = true
	}
	z := html.NewTokenizer(strings.NewReader(rawHTML))

	type openAnchor struct {
		href        string
		hasHref     bool
		hasIDOrName bool
		text        strings.Builder
		imgAlt      string
	}
	var cur *openAnchor

	flushAnchor := func() {
		if cur == nil {
			return
		}
		label := strings.TrimSpace(cur.text.String())
		if label == "" {
			label = cur.imgAlt
		}
		trimmedHref := strings.TrimSpace(cur.href)
		switch {
		case cur.hasHref && trimmedHref == "":
			acc.add("", label, SourceHTML, true)
		case !cur.hasHref && !cur.hasIDOrName:
			acc.add("", label, SourceHTML, true)
		case cur.hasHref:
			acc.add(trimmedHref, label, SourceHTML, false)
		}
		cur = nil
	}

	tokens := 0
	for tokens < MaxHTMLTokens {
		tt := z.Next()
		tokens++
		switch tt {
		case html.ErrorToken:
			flushAnchor()
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)
			switch tag {
			case "a":
				// HTML forbids nested <a>; close any unclosed anchor first.
				flushAnchor()
				var href string
				var hasHref, hasIDOrName bool
				for _, attr := range tok.Attr {
					switch attr.Key {
					case "href":
						if !hasHref {
							href = attr.Val
							hasHref = true
						}
					case "id", "name":
						if strings.TrimSpace(attr.Val) != "" {
							hasIDOrName = true
						}
					}
				}
				cur = &openAnchor{
					href:        href,
					hasHref:     hasHref,
					hasIDOrName: hasIDOrName,
				}
				if tt == html.SelfClosingTagToken {
					flushAnchor()
				}
			case "area":
				var href, alt, title string
				var hasHref bool
				for _, attr := range tok.Attr {
					switch attr.Key {
					case "href":
						if !hasHref {
							href = attr.Val
							hasHref = true
						}
					case "alt":
						if alt == "" {
							alt = attr.Val
						}
					case "title":
						if title == "" {
							title = attr.Val
						}
					}
				}
				label := strings.TrimSpace(alt)
				if label == "" {
					label = strings.TrimSpace(title)
				}
				trimmedHref := strings.TrimSpace(href)
				if !hasHref || trimmedHref == "" {
					acc.add("", label, SourceHTML, true)
				} else {
					acc.add(trimmedHref, label, SourceHTML, false)
				}
			case "img":
				if cur != nil && cur.imgAlt == "" {
					for _, attr := range tok.Attr {
						if attr.Key == "alt" && strings.TrimSpace(attr.Val) != "" {
							cur.imgAlt = attr.Val
							break
						}
					}
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if strings.EqualFold(string(name), "a") {
				flushAnchor()
			}
		case html.TextToken:
			if cur != nil && cur.text.Len() < maxAnchorTextRunes*4 {
				cur.text.Write(z.Text())
				cur.text.WriteByte(' ')
			}
		}
	}
	if tokens >= MaxHTMLTokens {
		acc.truncated = true
	}
	flushAnchor()
}

// extractTextLinks scans plain text in linear O(N) time for http:// and https:// URLs.
func extractTextLinks(rawText string, acc *linkAccumulator) {
	if len(rawText) > MaxTextBytes {
		rawText = rawText[:MaxTextBytes]
		acc.truncated = true
	}

	n := len(rawText)
	i := 0
	for i < n {
		schemeLen := matchHTTPPrefixAt(rawText, i)
		if schemeLen == 0 {
			i++
			continue
		}
		// Require a non-alphanumeric boundary before the scheme so "foohttps://..." does not match.
		if i > 0 && isASCIIAlphaNum(rawText[i-1]) {
			i += schemeLen
			continue
		}

		start := i
		end := i + schemeLen
		for end < n && isTextURLChar(rawText[end]) {
			end++
		}
		candidate := trimTrailingURLPunctuation(rawText[start:end])
		if len(candidate) > schemeLen {
			acc.add(candidate, "", SourceText, false)
		}
		i = end
	}
}

func matchHTTPPrefixAt(s string, i int) int {
	rem := len(s) - i
	if rem >= 8 && strings.EqualFold(s[i:i+8], "https://") {
		return 8
	}
	if rem >= 7 && strings.EqualFold(s[i:i+7], "http://") {
		return 7
	}
	return 0
}

func isASCIIAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isTextURLChar(c byte) bool {
	if c <= 0x20 || c >= 0x7f {
		return false
	}
	switch c {
	case '<', '>', '"', '\'', '`':
		return false
	default:
		return true
	}
}

func trimTrailingURLPunctuation(s string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		switch last {
		case '.', ',', ';', ':', '!', '?':
			s = s[:len(s)-1]
		case ')':
			if strings.Count(s, "(") < strings.Count(s, ")") {
				s = s[:len(s)-1]
			} else {
				return s
			}
		case ']':
			if strings.Count(s, "[") < strings.Count(s, "]") {
				s = s[:len(s)-1]
			} else {
				return s
			}
		case '}':
			if strings.Count(s, "{") < strings.Count(s, "}") {
				s = s[:len(s)-1]
			} else {
				return s
			}
		default:
			return s
		}
	}
	return s
}

// extractListUnsubscribeLinks parses RFC 2369 angle-bracketed URIs from a
// List-Unsubscribe header value (e.g. "<mailto:a@b.test>, <https://example.com/u>").
func extractListUnsubscribeLinks(headerVal string, acc *linkAccumulator) {
	s := headerVal
	for len(s) > 0 {
		openIdx := strings.IndexByte(s, '<')
		if openIdx < 0 {
			return
		}
		s = s[openIdx+1:]
		closeIdx := strings.IndexByte(s, '>')
		if closeIdx < 0 {
			return
		}
		raw := strings.TrimSpace(stripURLControlWhitespace(s[:closeIdx]))
		if raw != "" {
			acc.add(raw, "", SourceHeader, false)
		}
		s = s[closeIdx+1:]
	}
}

func normalizeAnchorText(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	joined := strings.Join(fields, " ")
	runes := 0
	for idx := range joined {
		if runes == maxAnchorTextRunes {
			return strings.TrimRightFunc(joined[:idx], unicode.IsSpace)
		}
		runes++
	}
	return joined
}

func hasOriginalPlainText(env *enmime.Envelope) bool {
	if env == nil {
		return false
	}
	for _, e := range env.Errors {
		if e != nil && e.Name == enmime.ErrorPlainTextFromHTML {
			return false
		}
	}
	if hasBodyPartOfType(env.Root, "text/plain") {
		return true
	}
	return env.Text != "" && env.HTML == ""
}

func hasBodyPartOfType(p *enmime.Part, contentType string) bool {
	if p == nil {
		return false
	}
	if strings.EqualFold(p.ContentType, contentType) && !strings.EqualFold(p.Disposition, "attachment") {
		return true
	}
	for child := p.FirstChild; child != nil; child = child.NextSibling {
		if hasBodyPartOfType(child, contentType) {
			return true
		}
	}
	return false
}

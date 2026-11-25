// Package render rewrites untrusted email HTML for safe sandboxed iframe
// display according to SPEC 12.1 and caches recent renders (ARCHITECTURE 3.11).
package render

import (
	"bytes"
	"container/list"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"sync"

	"github.com/jhillyerd/enmime/v2"
	"golang.org/x/net/html"
)

const (
	// MaxInlineBytes is the maximum cumulative bytes of inlined cid: data URIs
	// allowed per rendered HTML message (SPEC 12.1: 10 MiB).
	MaxInlineBytes = 10 << 20
	// DefaultCacheSize is the default LRU entry capacity for rendered messages (ARCHITECTURE 3.11).
	DefaultCacheSize = 32

	maxRewriteDepth = 16
	headInjectHTML  = `<meta name="referrer" content="no-referrer"><base target="_blank">`
)

// ErrNoHTML is returned when a message does not contain an HTML part (SPEC 12.1).
var ErrNoHTML = errors.New("render: message has no HTML part")

// BlobOpener opens a decompressed message blob by its relative path.
type BlobOpener interface {
	Open(relPath string) (io.ReadCloser, error)
}

// InlinePart represents a MIME part addressable by Content-ID.
type InlinePart struct {
	ContentType string
	Content     []byte
}

// Result holds rewritten HTML ready for the render endpoint, the count of
// external http/https resources referenced by the message (SPEC 12.1), and the
// normalised Content-IDs whose cid: references were inlined (FR-072).
type Result struct {
	HTML                []byte
	RemoteResourceCount int
	// ReferencedCIDs holds the normalised Content-IDs that the rewrite
	// replaced with data: URIs. It must be treated as read-only.
	ReferencedCIDs map[string]struct{}
}

// ReferencesCID reports whether the rewrite inlined at least one cid: reference
// to contentID, applying the same normalisation (angle brackets, surrounding
// whitespace, case) the renderer uses to match Content-ID headers.
func (r Result) ReferencesCID(contentID string) bool {
	cid := normalizeCID(contentID)
	if cid == "" {
		return false
	}
	_, ok := r.ReferencedCIDs[cid]
	return ok
}

type cacheKey struct {
	projectID int64
	msgID     string
}

type cacheEntry struct {
	key cacheKey
	res Result
}

// Renderer rewrites email HTML from blob storage and caches recent results in an LRU cache.
type Renderer struct {
	blobs     BlobOpener
	cacheSize int

	mu    sync.Mutex
	order *list.List
	items map[cacheKey]*list.Element
}

// New creates a Renderer backed by blobs with DefaultCacheSize LRU capacity.
func New(blobs BlobOpener) *Renderer {
	return NewWithCacheSize(blobs, DefaultCacheSize)
}

// NewWithCacheSize creates a Renderer with a custom LRU capacity (0 disables caching).
func NewWithCacheSize(blobs BlobOpener, cacheSize int) *Renderer {
	if cacheSize < 0 {
		cacheSize = 0
	}
	return &Renderer{
		blobs:     blobs,
		cacheSize: cacheSize,
		order:     list.New(),
		items:     make(map[cacheKey]*list.Element, cacheSize),
	}
}

// Render loads the message blob, extracts HTML and inline parts, rewrites HTML per
// SPEC 12.1, and caches the result. The returned HTML slice must be treated as read-only.
func (r *Renderer) Render(ctx context.Context, projectID int64, msgID, blobPath string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	key := cacheKey{projectID: projectID, msgID: msgID}
	if cached, ok := r.getCached(key); ok {
		return cached, nil
	}

	rc, err := r.blobs.Open(blobPath)
	if err != nil {
		return Result{}, fmt.Errorf("render: open blob %s: %w", msgID, err)
	}
	env, parseErr := enmime.NewParser().ReadEnvelope(rc)
	_ = rc.Close()
	if parseErr != nil {
		return Result{}, fmt.Errorf("render: parse message %s: %w", msgID, parseErr)
	}
	if strings.TrimSpace(env.HTML) == "" {
		return Result{}, ErrNoHTML
	}

	cids := collectCIDParts(env)
	res := RewriteHTML(env.HTML, cids, MaxInlineBytes)
	r.putCached(key, res)
	return res, nil
}

func (r *Renderer) getCached(key cacheKey) (Result, bool) {
	if r.cacheSize == 0 {
		return Result{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	elem, ok := r.items[key]
	if !ok {
		return Result{}, false
	}
	r.order.MoveToFront(elem)
	return elem.Value.(cacheEntry).res, true
}

func (r *Renderer) putCached(key cacheKey, res Result) {
	if r.cacheSize == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if elem, ok := r.items[key]; ok {
		elem.Value = cacheEntry{key: key, res: res}
		r.order.MoveToFront(elem)
		return
	}
	elem := r.order.PushFront(cacheEntry{key: key, res: res})
	r.items[key] = elem
	for r.order.Len() > r.cacheSize {
		oldest := r.order.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(cacheEntry)
		delete(r.items, entry.key)
		r.order.Remove(oldest)
	}
}

func collectCIDParts(env *enmime.Envelope) map[string]InlinePart {
	cids := make(map[string]InlinePart)
	addParts := func(parts []*enmime.Part) {
		for _, p := range parts {
			if p == nil {
				continue
			}
			cid := normalizeCID(p.ContentID)
			if cid == "" {
				continue
			}
			if _, exists := cids[cid]; exists {
				continue
			}
			cids[cid] = InlinePart{
				ContentType: p.ContentType,
				Content:     p.Content,
			}
		}
	}
	addParts(env.Inlines)
	addParts(env.Attachments)
	addParts(env.OtherParts)
	return cids
}

// RewriteHTML rewrites email HTML according to SPEC 12.1 using golang.org/x/net/html:
//   - injects <meta name="referrer" content="no-referrer"> and <base target="_blank">
//     at the start of <head> (creating <head> if missing);
//   - drops existing <base>, <meta http-equiv="refresh">, and prefetch-style <link> elements;
//   - replaces cid: URLs in src, background, srcset, and CSS url() with data: URIs up to maxInlineBytes,
//     recording each inlined Content-ID in Result.ReferencedCIDs;
//   - counts remote http:/https:/protocol-relative resources in src, srcset, background, and CSS url();
//   - preserves scripts and remaining markup verbatim (sandboxing is enforced by CSP + iframe).
func RewriteHTML(rawHTML string, cids map[string]InlinePart, maxInlineBytes int) Result {
	normCIDs := make(map[string]InlinePart, len(cids))
	for k, v := range cids {
		if nk := normalizeCID(k); nk != "" {
			normCIDs[nk] = v
		}
	}

	inlineUsed := 0
	dataURICache := make(map[string]string)
	referenced := make(map[string]struct{})
	resolveCID := func(rawURL string) (string, bool) {
		cidRef, ok := extractCIDRef(rawURL)
		if !ok {
			return "", false
		}
		part, found := normCIDs[cidRef]
		if !found {
			return "", false
		}
		if uri, cached := dataURICache[cidRef]; cached {
			if maxInlineBytes > 0 && inlineUsed+len(uri) > maxInlineBytes {
				return "", false
			}
			inlineUsed += len(uri)
			referenced[cidRef] = struct{}{}
			return uri, true
		}

		ct := cleanMediaType(part.ContentType)
		encodedLen := base64.StdEncoding.EncodedLen(len(part.Content))
		uriLen := len("data:") + len(ct) + len(";base64,") + encodedLen
		if maxInlineBytes > 0 && inlineUsed+uriLen > maxInlineBytes {
			return "", false
		}

		var ub strings.Builder
		ub.Grow(uriLen)
		ub.WriteString("data:")
		ub.WriteString(ct)
		ub.WriteString(";base64,")
		ub.WriteString(base64.StdEncoding.EncodeToString(part.Content))
		uri := ub.String()
		dataURICache[cidRef] = uri
		inlineUsed += len(uri)
		referenced[cidRef] = struct{}{}
		return uri, true
	}

	out, remoteCount := rewriteHTMLPass(rawHTML, resolveCID, true, false, false, 0)
	return Result{
		HTML:                out,
		RemoteResourceCount: remoteCount,
		ReferencedCIDs:      referenced,
	}
}

// isTokenizerRawTextTag reports whether golang.org/x/net/html switches into
// raw-text mode for tag. Inside foreign content (<svg>, <math>), browsers stay
// in normal HTML/foreign parsing mode rather than RAWTEXT, so elements like
// <meta http-equiv="refresh"> inside <svg><style> can break out and execute.
// Likewise, inside sandboxed iframes (scripts disabled), browsers parse
// <noscript> children as normal HTML rather than raw text.
func isTokenizerRawTextTag(tag string) bool {
	switch tag {
	case "iframe", "noembed", "noframes", "noscript", "plaintext", "script", "style", "textarea", "title", "xmp":
		return true
	default:
		return false
	}
}

func rewriteHTMLPass(
	rawHTML string,
	resolveCID func(string) (string, bool),
	injectHead bool,
	startInForeign bool,
	rewriteTextAsCSS bool,
	depth int,
) ([]byte, int) {
	var (
		out             bytes.Buffer
		injected        = !injectHead
		inStyle         bool
		inScript        bool
		inNoscript      bool
		inForeignRawTag string
		foreignDepth    int
		remoteCount     int
	)
	if startInForeign {
		foreignDepth = 1
	}
	extra := 0
	if injectHead {
		extra = len(headInjectHTML) + 32
	}
	out.Grow(len(rawHTML) + extra)

	injectSyntheticHead := func() {
		if injected {
			return
		}
		out.WriteString("<head>")
		out.WriteString(headInjectHTML)
		out.WriteString("</head>")
		injected = true
	}

	z := html.NewTokenizer(strings.NewReader(rawHTML))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}

		raw := append([]byte(nil), z.Raw()...)

		switch tt {
		case html.DoctypeToken, html.CommentToken:
			out.Write(raw)

		case html.TextToken:
			if inNoscript {
				if depth < maxRewriteDepth {
					subOut, subRemote := rewriteHTMLPass(string(raw), resolveCID, false, foreignDepth > 0, false, depth+1)
					remoteCount += subRemote
					out.Write(subOut)
				} else {
					out.WriteString(html.EscapeString(string(raw)))
				}
				continue
			}
			if inForeignRawTag != "" {
				if depth < maxRewriteDepth {
					subOut, subRemote := rewriteHTMLPass(string(raw), resolveCID, false, true, inForeignRawTag == "style", depth+1)
					remoteCount += subRemote
					out.Write(subOut)
				} else {
					out.WriteString(html.EscapeString(string(raw)))
				}
				continue
			}
			if inScript {
				out.Write(raw)
				continue
			}
			if inStyle || rewriteTextAsCSS {
				rewritten, n, _ := rewriteCSS(string(raw), resolveCID)
				remoteCount += n
				out.WriteString(rewritten)
				continue
			}
			if !injected && strings.TrimSpace(string(raw)) != "" {
				injectSyntheticHead()
			}
			out.Write(raw)

		case html.EndTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)
			switch tag {
			case "base":
				continue
			case "style":
				inStyle = false
			case "script":
				inScript = false
			case "noscript":
				inNoscript = false
			case "svg", "math":
				if foreignDepth > 0 {
					foreignDepth--
				}
			}
			if inForeignRawTag == tag {
				inForeignRawTag = ""
			}
			out.Write(raw)

		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)

			if injectHead && tag == "html" {
				out.Write(raw)
				continue
			}

			if injectHead && tag == "head" {
				out.Write(raw)
				if !injected && tt == html.StartTagToken {
					out.WriteString(headInjectHTML)
					injected = true
				}
				continue
			}

			if !injected {
				injectSyntheticHead()
			}

			if shouldDropTag(tag, tok.Attr) {
				continue
			}

			if tt == html.StartTagToken {
				switch tag {
				case "svg", "math":
					foreignDepth++
				case "noscript":
					inNoscript = true
				case "style":
					inStyle = true
					if foreignDepth > 0 {
						inForeignRawTag = tag
					}
				case "script":
					inScript = true
					if foreignDepth > 0 {
						inForeignRawTag = tag
					}
				default:
					if foreignDepth > 0 && isTokenizerRawTextTag(tag) {
						inForeignRawTag = tag
					}
				}
			}

			modified := false
			for i := range tok.Attr {
				key := strings.ToLower(tok.Attr[i].Key)
				val := tok.Attr[i].Val
				switch key {
				case "src", "background", "poster":
					if dataURI, ok := resolveCID(val); ok {
						tok.Attr[i].Val = dataURI
						modified = true
					} else if isRemoteURL(val) {
						remoteCount++
					}
				case "href", "xlink:href":
					if tag == "image" || tag == "feimage" {
						if dataURI, ok := resolveCID(val); ok {
							tok.Attr[i].Val = dataURI
							modified = true
						} else if isRemoteURL(val) {
							remoteCount++
						}
					}
				case "srcset":
					newVal, n, changed := rewriteSrcset(val, resolveCID)
					remoteCount += n
					if changed {
						tok.Attr[i].Val = newVal
						modified = true
					}
				case "style":
					newCSS, n, changed := rewriteCSS(val, resolveCID)
					remoteCount += n
					if changed {
						tok.Attr[i].Val = newCSS
						modified = true
					}
				case "srcdoc":
					if depth < maxRewriteDepth {
						subOut, subRemote := rewriteHTMLPass(val, resolveCID, false, false, false, depth+1)
						remoteCount += subRemote
						if string(subOut) != val {
							tok.Attr[i].Val = string(subOut)
							modified = true
						}
					} else if val != "" {
						tok.Attr[i].Val = ""
						modified = true
					}
				}
			}

			if modified {
				out.WriteString(tok.String())
			} else {
				out.Write(raw)
			}
		}
	}

	if !injected {
		var prefixed bytes.Buffer
		prefixed.Grow(out.Len() + len(headInjectHTML) + 16)
		prefixed.WriteString("<head>")
		prefixed.WriteString(headInjectHTML)
		prefixed.WriteString("</head>")
		prefixed.Write(out.Bytes())
		return prefixed.Bytes(), remoteCount
	}

	return out.Bytes(), remoteCount
}

func shouldDropTag(tag string, attrs []html.Attribute) bool {
	switch tag {
	case "base":
		return true
	case "meta":
		for _, a := range attrs {
			if strings.EqualFold(a.Key, "http-equiv") && strings.EqualFold(strings.TrimSpace(a.Val), "refresh") {
				return true
			}
		}
	case "link":
		for _, a := range attrs {
			if !strings.EqualFold(a.Key, "rel") {
				continue
			}
			for _, token := range strings.Fields(strings.ToLower(a.Val)) {
				switch token {
				case "dns-prefetch", "preconnect", "prefetch", "prerender", "preload", "modulepreload":
					return true
				}
			}
		}
	}
	return false
}

func normalizeCID(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	s = strings.TrimSpace(s)
	return strings.ToLower(s)
}

func extractCIDRef(rawURL string) (string, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if len(trimmed) < 4 || !strings.EqualFold(trimmed[:4], "cid:") {
		return "", false
	}
	ref := trimmed[4:]
	if unescaped, err := url.PathUnescape(ref); err == nil {
		ref = unescaped
	}
	norm := normalizeCID(ref)
	if norm == "" {
		return "", false
	}
	return norm, true
}

func cleanMediaType(declared string) string {
	mt, _, err := mime.ParseMediaType(declared)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.Split(declared, ";")[0]))
	}
	mt = strings.ToLower(strings.TrimSpace(mt))
	if mt == "" || !strings.Contains(mt, "/") {
		return "application/octet-stream"
	}
	return mt
}

func isRemoteURL(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//")
}

// rewriteSrcset parses an HTML srcset attribute per the HTML image candidate string
// syntax, counting remote URLs and inlining cid: URLs.
func rewriteSrcset(val string, resolveCID func(string) (string, bool)) (string, int, bool) {
	n := len(val)
	i := 0
	remoteCount := 0
	changed := false
	var out strings.Builder

	for i < n {
		// Skip leading ASCII whitespace and empty commas.
		startWS := i
		for i < n && (isASCIIWhitespace(val[i]) || val[i] == ',') {
			i++
		}
		out.WriteString(val[startWS:i])
		if i >= n {
			break
		}

		// Read URL token up to whitespace (or trailing comma if not data: URL).
		urlStart := i
		for i < n && !isASCIIWhitespace(val[i]) {
			if val[i] == ',' && !strings.HasPrefix(strings.ToLower(val[urlStart:i]), "data:") {
				break
			}
			i++
		}
		rawURL := val[urlStart:i]
		trailingComma := ""
		if strings.HasSuffix(rawURL, ",") {
			rawURL = rawURL[:len(rawURL)-1]
			trailingComma = ","
		}

		if dataURI, ok := resolveCID(rawURL); ok {
			out.WriteString(dataURI)
			changed = true
		} else {
			if isRemoteURL(rawURL) {
				remoteCount++
			}
			out.WriteString(rawURL)
		}
		out.WriteString(trailingComma)

		if trailingComma != "" {
			continue
		}

		// Copy descriptor up to next comma.
		descStart := i
		inParens := 0
		for i < n {
			c := val[i]
			switch {
			case c == '(':
				inParens++
			case c == ')' && inParens > 0:
				inParens--
			case c == ',' && inParens == 0:
				i++
				goto doneDesc
			}
			i++
		}
	doneDesc:
		out.WriteString(val[descStart:i])
	}

	if !changed {
		return val, remoteCount, false
	}
	return out.String(), remoteCount, true
}

// rewriteCSS scans CSS text for url(...) references and @import string rules,
// replacing cid: URLs with data: URIs and counting remote http:/https:/protocol-relative URLs.
func rewriteCSS(css string, resolveCID func(string) (string, bool)) (string, int, bool) {
	var (
		out         strings.Builder
		remoteCount int
		changed     bool
	)
	n := len(css)
	i := 0

	for i < n {
		// Preserve CSS comments without scanning inside them.
		if i+1 < n && css[i] == '/' && css[i+1] == '*' {
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				out.WriteString(css[i:])
				break
			}
			out.WriteString(css[i : i+2+end+2])
			i += 2 + end + 2
			continue
		}

		// Detect @import "https://..." or @import 'https://...'.
		if css[i] == '@' && hasPrefixFold(css[i:], "@import") && (i+7 >= n || !isCSSIdentChar(css[i+7])) {
			j := i + 7
			for j < n && isASCIIWhitespace(css[j]) {
				j++
			}
			if j < n && (css[j] == '"' || css[j] == '\'') {
				q := css[j]
				urlVal, endIdx := readCSSQuotedString(css, j+1, q)
				if isRemoteURL(urlVal) {
					remoteCount++
				}
				out.WriteString(css[i:endIdx])
				i = endIdx
				continue
			}
		}

		// Detect url(...) function token.
		if (css[i] == 'u' || css[i] == 'U') &&
			(i == 0 || !isCSSIdentChar(css[i-1])) &&
			hasPrefixFold(css[i:], "url(") {
			j := i + 4
			for j < n && isASCIIWhitespace(css[j]) {
				j++
			}

			var (
				rawURL string
				endIdx int
			)
			if j < n && (css[j] == '"' || css[j] == '\'') {
				q := css[j]
				val, afterQuote := readCSSQuotedString(css, j+1, q)
				rawURL = val
				k := afterQuote
				for k < n && css[k] != ')' {
					k++
				}
				if k < n && css[k] == ')' {
					endIdx = k + 1
				} else {
					endIdx = k
				}
			} else {
				valStart := j
				for j < n && css[j] != ')' {
					j++
				}
				rawURL = strings.TrimSpace(css[valStart:j])
				if j < n && css[j] == ')' {
					endIdx = j + 1
				} else {
					endIdx = j
				}
			}

			if dataURI, ok := resolveCID(rawURL); ok {
				out.WriteString(`url("`)
				out.WriteString(dataURI)
				out.WriteString(`")`)
				changed = true
			} else {
				if isRemoteURL(rawURL) {
					remoteCount++
				}
				out.WriteString(css[i:endIdx])
			}
			i = endIdx
			continue
		}

		out.WriteByte(css[i])
		i++
	}

	if !changed {
		return css, remoteCount, false
	}
	return out.String(), remoteCount, true
}

func readCSSQuotedString(s string, start int, quote byte) (string, int) {
	var b strings.Builder
	i := start
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == quote {
			return b.String(), i + 1
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), i
}

func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}

func isASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isCSSIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

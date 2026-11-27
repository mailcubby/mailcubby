package compat

import (
	"mime"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

const (
	// MaxHTMLBytes caps the number of HTML bytes walked per analysis.
	MaxHTMLBytes = 2 << 20
	// MaxHTMLNodes caps the number of HTML tokens walked per analysis.
	MaxHTMLNodes = 50_000
	// maxSrcsetCandidates caps image URL candidates inspected per srcset attribute.
	maxSrcsetCandidates = 1_000
)

type analysisResult struct {
	counts    map[string]int
	truncated bool
}

// analyzeDocument walks rawHTML with golang.org/x/net/html under strict byte,
// token, and CSS budgets, returning occurrence counts per caniemail slug.
// MSO conditional comments (<!--[if mso]>...<![endif]-->) are ignored.
func analyzeDocument(rawHTML string, cidContentTypes map[string]string) analysisResult {
	res := analysisResult{
		counts: make(map[string]int, 32),
	}

	if len(rawHTML) > MaxHTMLBytes {
		rawHTML = rawHTML[:MaxHTMLBytes]
		res.truncated = true
	}

	record := func(slug string) {
		if slug == "" {
			return
		}
		res.counts[slug]++
	}

	recordImageURL := func(rawURL string) {
		detectImageURL(rawURL, cidContentTypes, record)
	}

	cssBudget := newCSSBudget()
	z := html.NewTokenizer(strings.NewReader(rawHTML))

	var (
		inStyle  bool
		inScript bool
		tokens   int
	)

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		tokens++
		if tokens > MaxHTMLNodes {
			res.truncated = true
			break
		}

		switch tt {
		case html.DoctypeToken:
			tok := z.Token()
			if strings.EqualFold(strings.TrimSpace(tok.Data), "html") {
				record("html-doctype")
			}

		case html.CommentToken:
			tok := z.Token()
			if isMSOConditionalComment(tok.Data) {
				// MSO conditional comments (<!--[if mso]>...<![endif]--> and
				// downlevel-revealed wrappers <!--[if !mso]><!--> / <!--<![endif]-->)
				// are intentionally ignored so Outlook fallback hacks do not skew results.
				continue
			}
			record("html-comments")

		case html.TextToken:
			if inScript {
				continue
			}
			if inStyle {
				parseCSSStylesheet(string(z.Text()), cssBudget, record, recordImageURL)
			}

		case html.EndTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)
			switch tag {
			case "style":
				inStyle = false
			case "script":
				inScript = false
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)
			if tt == html.StartTagToken {
				switch tag {
				case "style":
					inStyle = true
				case "script":
					inScript = true
				}
			}

			if slug, ok := htmlElementRules[tag]; ok {
				record(slug)
			}

			analyzeElementAttrs(tag, tok.Attr, cssBudget, record, recordImageURL)
		}
	}

	if cssBudget.truncated {
		res.truncated = true
	}
	return res
}

func analyzeElementAttrs(
	tag string,
	attrs []html.Attribute,
	cssBudget *cssBudget,
	record func(string),
	recordImageURL func(string),
) {
	var (
		metaName   string
		inputType  string
		buttonType string
		sourceType string
	)

	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		val := strings.TrimSpace(a.Val)

		if slug, ok := htmlAttrRules[key]; ok {
			record(slug)
		}

		switch key {
		case "style":
			if val != "" {
				parseCSSInlineStyle(val, cssBudget, record, recordImageURL)
			}
		case "type":
			switch tag {
			case "input":
				inputType = strings.ToLower(val)
			case "button":
				buttonType = strings.ToLower(val)
			case "source":
				sourceType = strings.ToLower(val)
			}
		case "name":
			if tag == "meta" {
				metaName = strings.ToLower(val)
			}
		case "href":
			if tag == "a" || tag == "area" {
				if hasPrefixFold(val, "mailto:") {
					record("html-mailto-links")
				} else if strings.HasPrefix(val, "#") && len(val) > 1 {
					record("html-anchor-links")
				}
			}
		case "src":
			if tag == "img" || tag == "input" || tag == "source" {
				recordImageURL(val)
			}
		case "srcset":
			if tag == "img" || tag == "source" {
				forEachSrcsetURL(val, recordImageURL)
			}
		case "background":
			if val != "" {
				recordImageURL(val)
			}
		case "poster":
			if tag == "video" && val != "" {
				recordImageURL(val)
			}
		}
	}

	switch tag {
	case "meta":
		if metaName == "color-scheme" {
			record("html-meta-color-scheme")
		}
	case "input":
		if inputType == "" {
			inputType = "text"
		}
		if slug, ok := htmlInputTypeRules[inputType]; ok {
			record(slug)
		}
	case "button":
		if buttonType == "" {
			buttonType = "submit"
		}
		if slug, ok := htmlButtonTypeRules[buttonType]; ok {
			record(slug)
		}
	case "source":
		if sourceType != "" {
			if mt, _, err := mime.ParseMediaType(sourceType); err == nil {
				if slug, ok := imageMIMERules[strings.ToLower(mt)]; ok {
					record(slug)
				}
			}
		}
	}
}

// isMSOConditionalComment reports whether the raw comment body (without
// surrounding <!-- and -->) is a Microsoft Outlook / IE conditional comment.
// Uses zero-allocation prefix checks to avoid copying large comment payloads.
func isMSOConditionalComment(commentData string) bool {
	trimmed := strings.TrimLeft(commentData, " \t\n\r\f")
	if hasPrefixFold(trimmed, "[if") && len(trimmed) > 3 {
		next := trimmed[3]
		if next == ' ' || next == '\t' || next == '(' || next == '\n' || next == '\r' {
			return true
		}
	}
	if hasPrefixFold(trimmed, "<![endif]") || hasPrefixFold(trimmed, "[endif]") {
		return true
	}
	return false
}

func detectImageURL(rawURL string, cidContentTypes map[string]string, record func(string)) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return
	}

	// data:image/... URI (may include ;base64). Only inspect the header before ','.
	if hasPrefixFold(u, "data:") {
		rest := u[len("data:"):]
		comma := strings.IndexByte(rest, ',')
		if comma < 0 {
			return
		}
		// Cap header inspection so malformed huge data: headers stay cheap.
		if comma > 256 {
			return
		}
		header := strings.ToLower(rest[:comma])
		isBase64 := false
		mediaPart := header
		for _, part := range strings.Split(header, ";") {
			if strings.TrimSpace(part) == "base64" {
				isBase64 = true
			}
		}
		if semi := strings.IndexByte(header, ';'); semi >= 0 {
			mediaPart = header[:semi]
		}
		mediaPart = strings.TrimSpace(mediaPart)
		if isBase64 && (strings.HasPrefix(mediaPart, "image/") || mediaPart == "") {
			record("image-base64")
		}
		if slug, ok := imageMIMERules[mediaPart]; ok {
			record(slug)
		}
		return
	}

	// cid: inline attachment reference.
	if hasPrefixFold(u, "cid:") {
		cid := normalizeCID(u[len("cid:"):])
		if cid != "" && len(cidContentTypes) > 0 {
			if ct, ok := cidContentTypes[cid]; ok {
				if mt, _, err := mime.ParseMediaType(ct); err == nil {
					if slug, found := imageMIMERules[strings.ToLower(mt)]; found {
						record(slug)
					}
				}
			}
		}
		return
	}

	// Regular URL or relative path: check path extension.
	pathPart := u
	if parsed, err := url.Parse(u); err == nil && parsed.Path != "" {
		pathPart = parsed.Path
	} else {
		if q := strings.IndexAny(pathPart, "?#"); q >= 0 {
			pathPart = pathPart[:q]
		}
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(pathPart)), ".")
	if slug, ok := imageExtRules[ext]; ok {
		record(slug)
	}
}

// forEachSrcsetURL streams srcset URL candidates in O(N) time with zero
// per-iteration full-string allocations, capped at maxSrcsetCandidates.
func forEachSrcsetURL(srcset string, fn func(string)) {
	s := strings.TrimSpace(srcset)
	count := 0
	for s != "" && count < maxSrcsetCandidates {
		s = strings.TrimLeft(s, " \t\n\r,")
		if s == "" {
			break
		}
		var candidate string
		if hasPrefixFold(s, "data:") {
			// A data URI contains a comma before its payload; stop at whitespace.
			ws := strings.IndexAny(s, " \t\n\r")
			if ws < 0 {
				candidate = s
				s = ""
			} else {
				candidate = s[:ws]
				s = s[ws:]
				if comma := strings.IndexByte(s, ','); comma >= 0 {
					s = s[comma+1:]
				} else {
					s = ""
				}
			}
		} else {
			comma := strings.IndexByte(s, ',')
			var entry string
			if comma < 0 {
				entry = s
				s = ""
			} else {
				entry = s[:comma]
				s = s[comma+1:]
			}
			// Extract first whitespace-delimited token without allocating a slice.
			entry = strings.TrimLeft(entry, " \t\n\r")
			if ws := strings.IndexAny(entry, " \t\n\r"); ws >= 0 {
				candidate = entry[:ws]
			} else {
				candidate = entry
			}
		}
		if candidate != "" {
			fn(candidate)
			count++
		}
	}
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func normalizeCID(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	if decoded, err := url.PathUnescape(s); err == nil {
		s = decoded
	}
	return strings.ToLower(strings.TrimSpace(s))
}

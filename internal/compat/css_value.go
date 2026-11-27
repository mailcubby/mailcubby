package compat

import "strings"

func (p *cssParser) scanDeclaration(decl string) {
	// Strip CSS comments (quote-aware) before finding the property/value ':'
	// so comments like /* note: fallback */ display: flex; do not drop the declaration.
	cleaned := stripCSSComments(decl)
	colon := strings.IndexByte(cleaned, ':')
	if colon <= 0 {
		return
	}
	prop := strings.TrimSpace(cleaned[:colon])
	val := strings.TrimSpace(cleaned[colon+1:])
	if prop == "" || val == "" {
		return
	}

	if strings.HasPrefix(prop, "--") {
		p.record("css-variables")
		p.scanCSSValue(val, "")
		return
	}

	propLower := strings.ToLower(prop)

	// Detect trailing !important (including optional whitespace like "! important").
	valClean, hasImportant := stripTrailingImportant(val)
	if hasImportant {
		p.record("css-important")
	}

	// Check specific property+value rules first (e.g. display: flex -> css-display-flex).
	valLower := strings.ToLower(valClean)
	matchedSpecific := false
	if vmap, ok := cssPropertyValueRules[propLower]; ok {
		if slug, exact := vmap[valLower]; exact {
			p.record(slug)
			matchedSpecific = true
		}
	}
	if !matchedSpecific {
		if slug, ok := cssPropertyRules[propLower]; ok {
			p.record(slug)
		}
	}

	p.scanCSSValue(valClean, propLower)
}

// scanCSSValue scans a CSS property value linearly for functions, keywords,
// image URLs, and numeric units (including negative lengths/percentages).
func (p *cssParser) scanCSSValue(val, propLower string) {
	n := len(val)
	i := 0
	for i < n {
		c := val[i]
		// Skip quoted strings without mistaking their contents for units/functions.
		if c == '"' || c == '\'' {
			i = advanceCSSString(val, i)
			continue
		}

		// Number (optional leading '+' or '-') followed by a CSS unit
		// (e.g. 16px, 1.5rem, 100%, -2em, -100%, +.5rem).
		if startsCSSNumber(val, i) {
			if c == '-' || c == '+' {
				i++
			}
			for i < n && (isDigit(val[i]) || val[i] == '.') {
				i++
			}
			if i < n && val[i] == '%' {
				p.record("css-unit-percent")
				i++
				continue
			}
			uStart := i
			for i < n && isASCIILetter(val[i]) {
				i++
			}
			if i > uStart {
				unit := strings.ToLower(val[uStart:i])
				if slug, ok := cssUnitRules[unit]; ok {
					p.record(slug)
				}
			}
			continue
		}

		// Identifier or function call.
		if isCSSIdentStart(c) {
			start := i
			i++
			for i < n && isCSSIdentChar(val[i]) {
				i++
			}
			ident := strings.ToLower(val[start:i])

			// Function call: ident(...)
			if i < n && val[i] == '(' {
				if ident == "url" {
					arg, nextPos := readBalancedParens(val, i)
					if p.recordURL != nil {
						p.recordURL(unquoteCSSURL(arg))
					}
					i = nextPos
					continue
				}
				if slug, ok := cssFunctionRules[ident]; ok {
					p.record(slug)
				}
				// Advance past '(' so inner arguments are still scanned for nested functions/units.
				i++
				continue
			}

			// Tracked keywords.
			switch ident {
			case "initial":
				p.record("css-unit-initial")
			case "fit-content", "min-content", "max-content":
				p.record("css-intrinsic-size")
			case "system-ui", "ui-serif", "ui-sans-serif", "ui-rounded", "ui-monospace":
				if propLower == "font-family" || propLower == "font" || propLower == "" {
					p.record(slugCSSSystemUI)
				}
			}
			continue
		}

		i++
	}
}

func startsCSSNumber(s string, i int) bool {
	n := len(s)
	if i >= n {
		return false
	}
	c := s[i]
	if isDigit(c) {
		return true
	}
	if c == '.' && i+1 < n && isDigit(s[i+1]) {
		return true
	}
	if (c == '-' || c == '+') && i+1 < n {
		next := s[i+1]
		if isDigit(next) || (next == '.' && i+2 < n && isDigit(s[i+2])) {
			return true
		}
	}
	return false
}

// stripTrailingImportant removes a trailing "!important" (allowing whitespace
// between "!" and "important", per CSS Syntax) and reports whether one was found.
func stripTrailingImportant(val string) (string, bool) {
	trimmed := strings.TrimRight(val, " \t\n\r\f")
	const kw = "important"
	if len(trimmed) < len(kw)+1 {
		return val, false
	}
	if !strings.EqualFold(trimmed[len(trimmed)-len(kw):], kw) {
		return val, false
	}
	before := strings.TrimRight(trimmed[:len(trimmed)-len(kw)], " \t\n\r\f")
	if !strings.HasSuffix(before, "!") {
		return val, false
	}
	return strings.TrimSpace(before[:len(before)-1]), true
}

// stripCSSComments removes /* ... */ comments while preserving quoted strings
// (e.g. url("https://cdn.example/a/*b*/c.webp") stays intact).
func stripCSSComments(s string) string {
	if !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	n := len(s)
	i := 0
	for i < n {
		c := s[i]
		if c == '"' || c == '\'' {
			end := advanceCSSString(s, i)
			b.WriteString(s[i:end])
			i = end
			continue
		}
		if c == '/' && i+1 < n && s[i+1] == '*' {
			i += 2
			end := strings.Index(s[i:], "*/")
			if end < 0 {
				break
			}
			i += end + 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// advanceCSSString advances past a quoted CSS string starting at s[start],
// stopping at a matching unescaped quote, an unescaped newline (CSS §4.3.5), or EOF.
func advanceCSSString(s string, start int) int {
	n := len(s)
	if start >= n {
		return n
	}
	quote := s[start]
	i := start + 1
	for i < n {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\f' {
			return i
		}
		i++
		if c == '\\' && i < n {
			if s[i] == '\r' && i+1 < n && s[i+1] == '\n' {
				i += 2
			} else {
				i++
			}
			continue
		}
		if c == quote {
			return i
		}
	}
	return n
}

func readBalancedParens(s string, openIdx int) (string, int) {
	n := len(s)
	if openIdx >= n || s[openIdx] != '(' {
		return "", openIdx
	}
	depth := 0
	i := openIdx
	for i < n {
		c := s[i]
		switch c {
		case '"', '\'':
			i = advanceCSSString(s, i)
		case '(':
			depth++
			i++
		case ')':
			depth--
			i++
			if depth == 0 {
				return s[openIdx+1 : i-1], i
			}
		default:
			i++
		}
	}
	return s[openIdx+1:], n
}

func unquoteCSSURL(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func isASCIIWS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isCSSIdentStart(c byte) bool {
	return isASCIILetter(c) || c == '_' || c == '-' || c >= 0x80
}

func isCSSIdentChar(c byte) bool {
	return isASCIILetter(c) || isDigit(c) || c == '_' || c == '-' || c >= 0x80
}

package compat

import (
	"strings"
)

const (
	// MaxCSSBytes caps total CSS bytes scanned across all <style> blocks and
	// inline style="" attributes per message.
	MaxCSSBytes = 512 << 10
	// MaxCSSRules caps total CSS rules and at-rules parsed per message.
	MaxCSSRules = 5_000
	// MaxCSSDeclarations caps total CSS declarations parsed per message.
	MaxCSSDeclarations = 20_000
	// MaxCSSDepth caps brace nesting depth (e.g. nested @media / CSS nesting).
	MaxCSSDepth = 32
)

// cssBudget tracks cumulative CSS analysis limits across one HTML document so
// hostile or pathological stylesheets always terminate in linear time O(N).
type cssBudget struct {
	bytesLeft int
	rulesLeft int
	declsLeft int
	truncated bool
}

func newCSSBudget() *cssBudget {
	return &cssBudget{
		bytesLeft: MaxCSSBytes,
		rulesLeft: MaxCSSRules,
		declsLeft: MaxCSSDeclarations,
	}
}

func (b *cssBudget) slice(s string) string {
	if b.bytesLeft <= 0 {
		if len(s) > 0 {
			b.truncated = true
		}
		return ""
	}
	if len(s) > b.bytesLeft {
		b.truncated = true
		s = s[:b.bytesLeft]
		b.bytesLeft = 0
		return s
	}
	b.bytesLeft -= len(s)
	return s
}

func (b *cssBudget) takeRule() bool {
	if b.rulesLeft <= 0 {
		b.truncated = true
		return false
	}
	b.rulesLeft--
	return true
}

func (b *cssBudget) takeDecl() bool {
	if b.declsLeft <= 0 {
		b.truncated = true
		return false
	}
	b.declsLeft--
	return true
}

// cssParser is a linear-time, zero-regex, hand-written CSS scanner that walks
// stylesheets and inline style attributes once from left to right.
type cssParser struct {
	src       string
	pos       int
	budget    *cssBudget
	record    func(slug string)
	recordURL func(rawURL string)
}

func parseCSSStylesheet(src string, budget *cssBudget, record func(string), recordURL func(string)) {
	src = budget.slice(src)
	if src == "" {
		return
	}
	p := &cssParser{
		src:       src,
		budget:    budget,
		record:    record,
		recordURL: recordURL,
	}
	p.parseRuleList(0, false)
}

func parseCSSInlineStyle(src string, budget *cssBudget, record func(string), recordURL func(string)) {
	src = budget.slice(src)
	if src == "" {
		return
	}
	p := &cssParser{
		src:       src,
		budget:    budget,
		record:    record,
		recordURL: recordURL,
	}
	p.parseDeclarationBlock(0, false, false)
}

// skipWSAndComments advances past ASCII whitespace and /* ... */ comments in O(N).
func (p *cssParser) skipWSAndComments() {
	n := len(p.src)
	for p.pos < n {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			p.pos++
			continue
		}
		if c == '/' && p.pos+1 < n && p.src[p.pos+1] == '*' {
			p.record("css-comments")
			p.pos += 2
			end := strings.Index(p.src[p.pos:], "*/")
			if end < 0 {
				p.pos = n
				return
			}
			p.pos += end + 2
			continue
		}
		// HTML comment delimiters sometimes appear inside <style> blocks.
		if c == '<' && strings.HasPrefix(p.src[p.pos:], "<!--") {
			p.pos += 4
			continue
		}
		if c == '-' && strings.HasPrefix(p.src[p.pos:], "-->") {
			p.pos += 3
			continue
		}
		break
	}
}

// skipString advances past a quoted CSS string. Per CSS Syntax Level 3 §4.3.5,
// an unescaped newline terminates an unclosed string as a bad-string token so
// a single unclosed quote cannot swallow the rest of the stylesheet.
func (p *cssParser) skipString(quote byte) {
	p.pos++ // opening quote
	n := len(p.src)
	for p.pos < n {
		c := p.src[p.pos]
		if c == '\n' || c == '\r' || c == '\f' {
			return
		}
		p.pos++
		if c == '\\' && p.pos < n {
			// Escaped newline (CRLF or single LF/CR/FF) continues the string.
			if p.src[p.pos] == '\r' && p.pos+1 < n && p.src[p.pos+1] == '\n' {
				p.pos += 2
			} else {
				p.pos++
			}
			continue
		}
		if c == quote {
			return
		}
	}
}

// skipBalancedBlock skips a balanced {...} block starting at '{' in O(N).
func (p *cssParser) skipBalancedBlock() {
	n := len(p.src)
	if p.pos >= n || p.src[p.pos] != '{' {
		return
	}
	depth := 0
	for p.pos < n {
		c := p.src[p.pos]
		switch c {
		case '"', '\'':
			p.skipString(c)
		case '/':
			if p.pos+1 < n && p.src[p.pos+1] == '*' {
				p.pos += 2
				if end := strings.Index(p.src[p.pos:], "*/"); end >= 0 {
					p.pos += end + 2
				} else {
					p.pos = n
				}
			} else {
				p.pos++
			}
		case '{':
			depth++
			p.pos++
		case '}':
			depth--
			p.pos++
			if depth <= 0 {
				return
			}
		default:
			p.pos++
		}
	}
}

func (p *cssParser) parseRuleList(depth int, stopAtCloseBrace bool) {
	n := len(p.src)
	for {
		p.skipWSAndComments()
		if p.pos >= n {
			return
		}
		if p.src[p.pos] == '}' {
			if stopAtCloseBrace {
				p.pos++
				return
			}
			// Stray closing brace at top level; advance to guarantee progress.
			p.pos++
			continue
		}
		if !p.budget.takeRule() {
			return
		}
		if p.src[p.pos] == '@' {
			p.parseAtRule(depth, false)
			continue
		}
		p.parseQualifiedRule(depth)
	}
}

func (p *cssParser) parseAtRule(depth int, inStyleRule bool) {
	n := len(p.src)
	p.pos++ // consume '@'
	nameStart := p.pos
	for p.pos < n && isCSSIdentChar(p.src[p.pos]) {
		p.pos++
	}
	name := strings.ToLower(p.src[nameStart:p.pos])
	if slug, ok := cssAtRuleRules[name]; ok {
		p.record(slug)
	}

	preludeStart := p.pos
	parenDepth := 0
	var delim byte
	for p.pos < n {
		c := p.src[p.pos]
		switch c {
		case '"', '\'':
			p.skipString(c)
		case '/':
			if p.pos+1 < n && p.src[p.pos+1] == '*' {
				p.record("css-comments")
				p.pos += 2
				if end := strings.Index(p.src[p.pos:], "*/"); end >= 0 {
					p.pos += end + 2
				} else {
					p.pos = n
				}
			} else {
				p.pos++
			}
		case '(':
			parenDepth++
			p.pos++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
			p.pos++
		case ';':
			if parenDepth == 0 {
				delim = ';'
				goto donePrelude
			}
			p.pos++
		case '{':
			if parenDepth == 0 {
				delim = '{'
				goto donePrelude
			}
			p.pos++
		case '}':
			if parenDepth == 0 {
				delim = '}'
				goto donePrelude
			}
			p.pos++
		default:
			p.pos++
		}
	}

donePrelude:
	prelude := p.src[preludeStart:p.pos]
	if name == "media" {
		p.scanMediaPrelude(prelude)
	}

	switch delim {
	case ';':
		p.pos++
		return
	case '}':
		// Let parent block consume '}'
		return
	case '{':
		if depth >= MaxCSSDepth {
			p.budget.truncated = true
			p.skipBalancedBlock()
			return
		}
		p.pos++ // consume '{'
		switch name {
		case "media", "supports", "layer", "container", "scope":
			if inStyleRule {
				// Nested conditional at-rule inside a style rule may contain bare
				// declarations as well as nested style rules (CSS Nesting).
				p.parseDeclarationBlock(depth+1, true, true)
			} else {
				p.parseRuleList(depth+1, true)
			}
		case "font-face", "page",
			"top-left", "top-center", "top-right",
			"bottom-left", "bottom-center", "bottom-right",
			"left-top", "left-middle", "left-bottom",
			"right-top", "right-middle", "right-bottom":
			p.parseDeclarationBlock(depth+1, true, false)
		case "keyframes", "-webkit-keyframes":
			// Keyframes contain frame selectors (from/to/50%) with declaration blocks;
			// skip inner frame selectors so "from"/"to" are not miscounted as type selectors.
			p.parseKeyframeBlock(depth + 1)
		default:
			p.pos-- // back up to '{'
			p.skipBalancedBlock()
		}
	}
}

func (p *cssParser) parseKeyframeBlock(depth int) {
	n := len(p.src)
	for {
		p.skipWSAndComments()
		if p.pos >= n {
			return
		}
		if p.src[p.pos] == '}' {
			p.pos++
			return
		}
		if !p.budget.takeRule() {
			return
		}
		// Advance to '{' or '}', skipping strings and /* ... */ comments.
		for p.pos < n && p.src[p.pos] != '{' && p.src[p.pos] != '}' {
			c := p.src[p.pos]
			if c == '"' || c == '\'' {
				p.skipString(c)
				continue
			}
			if c == '/' && p.pos+1 < n && p.src[p.pos+1] == '*' {
				p.record("css-comments")
				p.pos += 2
				if end := strings.Index(p.src[p.pos:], "*/"); end >= 0 {
					p.pos += end + 2
				} else {
					p.pos = n
				}
				continue
			}
			p.pos++
		}
		if p.pos >= n {
			return
		}
		if p.src[p.pos] == '}' {
			p.pos++
			return
		}
		if depth >= MaxCSSDepth {
			p.budget.truncated = true
			p.skipBalancedBlock()
			continue
		}
		p.pos++ // consume '{'
		p.parseDeclarationBlock(depth+1, true, false)
	}
}

// scanMediaPrelude inspects @media (...) expressions for tracked media features.
func (p *cssParser) scanMediaPrelude(prelude string) {
	cleaned := stripCSSComments(prelude)
	n := len(cleaned)
	for i := 0; i < n; i++ {
		if cleaned[i] != '(' {
			continue
		}
		j := i + 1
		for j < n && isASCIIWS(cleaned[j]) {
			j++
		}
		start := j
		for j < n && isCSSIdentChar(cleaned[j]) {
			j++
		}
		if j > start {
			feat := strings.ToLower(cleaned[start:j])
			if slug, ok := cssMediaFeatureRules[feat]; ok {
				p.record(slug)
			}
		}
	}
}

func (p *cssParser) parseQualifiedRule(depth int) {
	n := len(p.src)
	selStart := p.pos
	parenDepth := 0
	bracketDepth := 0
	var delim byte
	for p.pos < n {
		c := p.src[p.pos]
		switch c {
		case '"', '\'':
			p.skipString(c)
		case '/':
			if p.pos+1 < n && p.src[p.pos+1] == '*' {
				p.record("css-comments")
				p.pos += 2
				if end := strings.Index(p.src[p.pos:], "*/"); end >= 0 {
					p.pos += end + 2
				} else {
					p.pos = n
				}
			} else {
				p.pos++
			}
		case '(':
			parenDepth++
			p.pos++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
			p.pos++
		case '[':
			bracketDepth++
			p.pos++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
			p.pos++
		case '{':
			if parenDepth == 0 && bracketDepth == 0 {
				delim = '{'
				goto doneSelector
			}
			p.pos++
		case '}':
			if parenDepth == 0 && bracketDepth == 0 {
				delim = '}'
				goto doneSelector
			}
			p.pos++
		default:
			p.pos++
		}
	}

doneSelector:
	selectorText := strings.TrimSpace(p.src[selStart:p.pos])
	if delim != '{' {
		// Unterminated or broken rule; do not consume parent '}'.
		if p.pos < n && p.src[p.pos] != '}' {
			p.pos++
		}
		return
	}

	p.scanSelectorList(selectorText)

	if depth >= MaxCSSDepth {
		p.budget.truncated = true
		p.skipBalancedBlock()
		return
	}
	p.pos++ // consume '{'
	p.parseDeclarationBlock(depth+1, true, true)
}

func (p *cssParser) parseDeclarationBlock(depth int, stopAtCloseBrace, inStyleRule bool) {
	n := len(p.src)
	for {
		p.skipWSAndComments()
		if p.pos >= n {
			return
		}
		if p.src[p.pos] == '}' {
			if stopAtCloseBrace {
				p.pos++
				return
			}
			p.pos++
			continue
		}
		if p.src[p.pos] == ';' {
			p.pos++
			continue
		}

		// Nested at-rule inside a style rule (CSS Nesting) or @page margin rule.
		if p.src[p.pos] == '@' {
			if inStyleRule {
				p.record("css-nesting")
			}
			if !p.budget.takeRule() {
				return
			}
			p.parseAtRule(depth, inStyleRule)
			continue
		}

		// Scan one item until ';', '{', or '}' at top parenthesis/bracket level.
		start := p.pos
		parenDepth := 0
		bracketDepth := 0
		var delim byte
		for p.pos < n {
			c := p.src[p.pos]
			switch c {
			case '"', '\'':
				p.skipString(c)
			case '/':
				if p.pos+1 < n && p.src[p.pos+1] == '*' {
					p.record("css-comments")
					p.pos += 2
					if end := strings.Index(p.src[p.pos:], "*/"); end >= 0 {
						p.pos += end + 2
					} else {
						p.pos = n
					}
				} else {
					p.pos++
				}
			case '(':
				parenDepth++
				p.pos++
			case ')':
				if parenDepth > 0 {
					parenDepth--
				}
				p.pos++
			case '[':
				bracketDepth++
				p.pos++
			case ']':
				if bracketDepth > 0 {
					bracketDepth--
				}
				p.pos++
			case ';', '{', '}':
				if parenDepth == 0 && bracketDepth == 0 {
					delim = c
					goto doneItem
				}
				p.pos++
			default:
				p.pos++
			}
		}

	doneItem:
		item := strings.TrimSpace(p.src[start:p.pos])

		if delim == '{' {
			// Nested selector block inside a style rule (CSS Nesting).
			if inStyleRule {
				p.record("css-nesting")
			}
			if !p.budget.takeRule() {
				return
			}
			p.scanSelectorList(item)
			if depth >= MaxCSSDepth {
				p.budget.truncated = true
				p.skipBalancedBlock()
				continue
			}
			p.pos++ // consume '{'
			p.parseDeclarationBlock(depth+1, true, inStyleRule)
			continue
		}

		if item != "" {
			if !p.budget.takeDecl() {
				return
			}
			p.scanDeclaration(item)
		}

		switch delim {
		case ';':
			p.pos++
		case '}':
			if stopAtCloseBrace {
				p.pos++
				return
			}
			p.pos++
		}
	}
}

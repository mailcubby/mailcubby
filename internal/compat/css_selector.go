package compat

import "strings"

// scanSelectorList analyzes a selector list linearly for selector types,
// combinators, grouping, chaining, pseudo-classes, and pseudo-elements.
func (p *cssParser) scanSelectorList(selList string) {
	cleaned := stripCSSComments(selList)
	parts := splitTopLevelComma(cleaned)
	if len(parts) > 1 {
		p.record("css-selector-grouping")
	}
	for _, rawSel := range parts {
		sel := strings.TrimSpace(rawSel)
		if sel == "" {
			continue
		}
		p.scanSingleSelector(sel)
	}
}

func (p *cssParser) scanSingleSelector(sel string) {
	n := len(sel)
	i := 0
	compoundParts := 0
	sawCompound := false
	pendingSpace := false
	explicitCombinator := false

	flushCompound := func() {
		if compoundParts >= 2 {
			p.record("css-selector-chaining")
		}
		if compoundParts > 0 {
			sawCompound = true
		}
		compoundParts = 0
	}

	noteStartOfPart := func() {
		if pendingSpace && sawCompound && !explicitCombinator {
			p.record("css-selector-descendant")
		}
		pendingSpace = false
		explicitCombinator = false
	}

	for i < n {
		c := sel[i]
		if isASCIIWS(c) {
			flushCompound()
			pendingSpace = true
			for i < n && isASCIIWS(sel[i]) {
				i++
			}
			continue
		}

		switch c {
		case '>':
			flushCompound()
			p.record("css-selector-child")
			explicitCombinator = true
			pendingSpace = false
			i++
		case '+':
			flushCompound()
			p.record("css-selector-adjacent-sibling")
			explicitCombinator = true
			pendingSpace = false
			i++
		case '~':
			flushCompound()
			p.record("css-selector-general-sibling")
			explicitCombinator = true
			pendingSpace = false
			i++
		case '*':
			noteStartOfPart()
			p.record("css-selector-universal")
			compoundParts++
			i++
		case '.':
			noteStartOfPart()
			i++
			start := i
			i = advanceCSSIdent(sel, i)
			if i > start {
				p.record("css-selector-class")
				compoundParts++
			}
		case '#':
			noteStartOfPart()
			i++
			start := i
			i = advanceCSSIdent(sel, i)
			if i > start {
				p.record("css-selector-id")
				compoundParts++
			}
		case '[':
			noteStartOfPart()
			p.record("css-selector-attribute")
			compoundParts++
			i = skipBracketGroup(sel, i)
		case ':':
			noteStartOfPart()
			isDouble := i+1 < n && sel[i+1] == ':'
			if isDouble {
				i += 2
			} else {
				i++
			}
			start := i
			for i < n && isCSSIdentChar(sel[i]) {
				i++
			}
			name := strings.ToLower(sel[start:i])
			if isDouble {
				if slug, ok := cssPseudoElementRules[name]; ok {
					p.record(slug)
				}
			} else {
				if slug, ok := cssPseudoClassRules[name]; ok {
					p.record(slug)
				} else if slug, ok := cssPseudoElementRules[name]; ok {
					// Legacy single-colon syntax (:before, :after, :first-line, :first-letter).
					p.record(slug)
				}
			}
			if i < n && sel[i] == '(' {
				_, nextPos := readBalancedParens(sel, i)
				i = nextPos
			}
		case '&':
			noteStartOfPart()
			compoundParts++
			i++
		default:
			if isCSSIdentStart(c) || (c == '\\' && i+1 < n) {
				noteStartOfPart()
				i = advanceCSSIdent(sel, i)
				p.record("css-selector-type")
				compoundParts++
			} else {
				i++
			}
		}
	}
	flushCompound()
}

// advanceCSSIdent scans a CSS identifier including backslash escapes (such as
// Tailwind/Maizzle utility classes `.dark\:hover\:underline`) so escaped colons
// or brackets are not mistaken for pseudo-classes or attribute selectors.
func advanceCSSIdent(s string, i int) int {
	n := len(s)
	for i < n {
		if s[i] == '\\' {
			if i+1 < n && s[i+1] != '\n' && s[i+1] != '\r' && s[i+1] != '\f' {
				i += 2
				continue
			}
			break
		}
		if isCSSIdentChar(s[i]) {
			i++
			continue
		}
		break
	}
	return i
}

func splitTopLevelComma(s string) []string {
	var parts []string
	start := 0
	parenDepth := 0
	bracketDepth := 0
	n := len(s)
	for i := 0; i < n; i++ {
		switch s[i] {
		case '"', '\'':
			i = advanceCSSString(s, i) - 1
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ',':
			if parenDepth == 0 && bracketDepth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func skipBracketGroup(s string, openIdx int) int {
	n := len(s)
	i := openIdx + 1
	for i < n {
		c := s[i]
		if c == '"' || c == '\'' {
			i = advanceCSSString(s, i)
			continue
		}
		if c == ']' {
			return i + 1
		}
		i++
	}
	return n
}

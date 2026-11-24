package search

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxQueryBytes bounds raw search input length to prevent query DoS.
	MaxQueryBytes = 4096
	// MaxQueryTerms bounds the number of terms in a single search query.
	MaxQueryTerms = 64
)

// ErrInvalidQuery is returned when a search query contains an invalid operator value
// (such as a malformed date in before:/after:) or exceeds query limits.
var ErrInvalidQuery = errors.New("search: invalid query")

// TermKind identifies the type of a parsed search term (SPEC FR-081).
type TermKind int

const (
	// TermText matches all FTS5 columns.
	TermText TermKind = iota
	// TermFrom matches FTS5 from_text.
	TermFrom
	// TermTo matches FTS5 to_text (To/Cc/Bcc).
	TermTo
	// TermSubject matches FTS5 subject.
	TermSubject
	// TermHasAttachment filters messages.attachment_count > 0.
	TermHasAttachment
	// TermHasHTML filters messages.has_html = 1.
	TermHasHTML
	// TermHasText filters messages.has_text = 1.
	TermHasText
	// TermIsUnread filters messages.seen = 0.
	TermIsUnread
	// TermIsRead filters messages.seen = 1.
	TermIsRead
	// TermTag filters message_tags by tag name (case-insensitive).
	TermTag
	// TermBefore filters messages.received_at < Time.
	TermBefore
	// TermAfter filters messages.received_at >= Time.
	TermAfter
	// TermCredential filters messages received via an SMTP credential with the given label (FR-021, FR-081).
	TermCredential
)

// Term represents one parsed search token or operator from a query string.
type Term struct {
	Kind    TermKind
	Value   string
	Time    time.Time
	Negated bool
	Quoted  bool
	Prefix  bool
}

// Query is the structured result of parsing a user search string (SPEC FR-081).
type Query struct {
	Raw   string
	Terms []Term
}

// IsEmpty reports whether the query contains no search terms.
func (q Query) IsEmpty() bool {
	return len(q.Terms) == 0
}

// Parse tokenizes and parses a search query string according to SPEC FR-081.
// Unknown operators are treated as plain text. Invalid before:/after: dates
// return an error wrapping ErrInvalidQuery.
func Parse(raw string) (Query, error) {
	if len(raw) > MaxQueryBytes {
		return Query{}, fmt.Errorf("%w: query exceeds maximum length of %d bytes", ErrInvalidQuery, MaxQueryBytes)
	}

	q := Query{Raw: raw}
	i := 0
	n := len(raw)

	for i < n {
		// Skip whitespace.
		for i < n {
			r, size := utf8.DecodeRuneInString(raw[i:])
			if !unicode.IsSpace(r) {
				break
			}
			i += size
		}
		if i >= n {
			break
		}

		if len(q.Terms) >= MaxQueryTerms {
			return Query{}, fmt.Errorf("%w: query exceeds maximum of %d terms", ErrInvalidQuery, MaxQueryTerms)
		}

		term, nextIdx, ok, err := parseNextTerm(raw, i)
		if err != nil {
			return Query{}, err
		}
		i = nextIdx
		if ok {
			q.Terms = append(q.Terms, term)
		}
	}

	// SPEC FR-081: prefix match for the last bare word (word*).
	if len(q.Terms) > 0 {
		last := &q.Terms[len(q.Terms)-1]
		if last.Kind == TermText && !last.Negated && !last.Quoted && hasFTSToken(last.Value) {
			last.Prefix = true
		}
	}

	return q, nil
}

func parseNextTerm(s string, start int) (Term, int, bool, error) {
	i := start
	n := len(s)
	negated := false

	if s[i] == '-' && i+1 < n {
		nextRune, _ := utf8.DecodeRuneInString(s[i+1:])
		if !unicode.IsSpace(nextRune) {
			negated = true
			i++
		}
	}

	// Standalone quoted phrase: "exact phrase" or -"exact phrase".
	if i < n && s[i] == '"' {
		val, next := readQuoted(s, i)
		if val == "" {
			return Term{}, next, false, nil
		}
		return Term{
			Kind:    TermText,
			Value:   val,
			Negated: negated,
			Quoted:  true,
		}, next, true, nil
	}

	// Scan ahead to see if this token begins with `<known_op>:`.
	opKey, colonPos, hasOp := scanOperatorPrefix(s, i)
	if hasOp {
		op := strings.ToLower(opKey)
		if isKnownOperator(op) {
			valStart := colonPos + 1
			var (
				val    string
				next   int
				quoted bool
			)
			if valStart < n && s[valStart] == '"' {
				val, next = readQuoted(s, valStart)
				quoted = true
			} else {
				val, next = readUnquoted(s, valStart)
			}

			term, matchedOp, err := buildOperatorTerm(op, val, negated, quoted)
			if err != nil {
				return Term{}, next, false, err
			}
			if matchedOp {
				return term, next, true, nil
			}
			// Known operator with empty or unknown enum value -> treat whole raw token as plain text.
			rawVal := s[i:next]
			if rawVal == "" {
				return Term{}, next, false, nil
			}
			return Term{
				Kind:    TermText,
				Value:   rawVal,
				Negated: negated,
				Quoted:  false,
			}, next, true, nil
		}
	}

	// Plain bare word (may contain ':' for unknown operators or URLs, or quotes).
	val, next := readBareToken(s, i)
	if val == "" {
		return Term{}, next, false, nil
	}
	return Term{
		Kind:    TermText,
		Value:   val,
		Negated: negated,
		Quoted:  false,
	}, next, true, nil
}

func scanOperatorPrefix(s string, start int) (string, int, bool) {
	i := start
	for i < len(s) {
		b := s[i]
		if b == ':' {
			if i == start {
				return "", -1, false
			}
			return s[start:i], i, true
		}
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') {
			i++
			continue
		}
		return "", -1, false
	}
	return "", -1, false
}

func isKnownOperator(op string) bool {
	switch op {
	case "from", "to", "subject", "has", "is", "tag", "before", "after", "credential":
		return true
	default:
		return false
	}
}

func buildOperatorTerm(op, val string, negated, quoted bool) (Term, bool, error) {
	if val == "" {
		return Term{}, false, nil
	}

	switch op {
	case "from":
		return Term{Kind: TermFrom, Value: val, Negated: negated, Quoted: quoted}, true, nil
	case "to":
		return Term{Kind: TermTo, Value: val, Negated: negated, Quoted: quoted}, true, nil
	case "subject":
		return Term{Kind: TermSubject, Value: val, Negated: negated, Quoted: quoted}, true, nil
	case "tag":
		return Term{Kind: TermTag, Value: val, Negated: negated, Quoted: quoted}, true, nil
	case "credential":
		return Term{Kind: TermCredential, Value: val, Negated: negated, Quoted: quoted}, true, nil
	case "has":
		switch strings.ToLower(val) {
		case "attachment":
			return Term{Kind: TermHasAttachment, Value: "attachment", Negated: negated}, true, nil
		case "html":
			return Term{Kind: TermHasHTML, Value: "html", Negated: negated}, true, nil
		case "text":
			return Term{Kind: TermHasText, Value: "text", Negated: negated}, true, nil
		default:
			return Term{}, false, nil
		}
	case "is":
		switch strings.ToLower(val) {
		case "unread":
			return Term{Kind: TermIsUnread, Value: "unread", Negated: negated}, true, nil
		case "read":
			return Term{Kind: TermIsRead, Value: "read", Negated: negated}, true, nil
		default:
			return Term{}, false, nil
		}
	case "before":
		ts, err := parseQueryDate(val)
		if err != nil {
			return Term{}, false, fmt.Errorf("%w: invalid before date %q: %w", ErrInvalidQuery, val, err)
		}
		return Term{Kind: TermBefore, Value: val, Time: ts, Negated: negated}, true, nil
	case "after":
		ts, err := parseQueryDate(val)
		if err != nil {
			return Term{}, false, fmt.Errorf("%w: invalid after date %q: %w", ErrInvalidQuery, val, err)
		}
		return Term{Kind: TermAfter, Value: val, Time: ts, Negated: negated}, true, nil
	default:
		return Term{}, false, nil
	}
}

func parseQueryDate(raw string) (time.Time, error) {
	if len(raw) == len("2006-01-02") {
		if ts, err := time.Parse("2006-01-02", raw); err == nil {
			return ts.UTC(), nil
		}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, errors.New("expected YYYY-MM-DD or RFC 3339 timestamp")
}

// readQuoted reads a double-quoted string starting at s[start] == '"'.
// Supports \" and \\ escapes. An unclosed quote reads to the end of s.
func readQuoted(s string, start int) (string, int) {
	var b strings.Builder
	i := start + 1
	n := len(s)
	for i < n {
		c := s[i]
		if c == '\\' && i+1 < n && (s[i+1] == '"' || s[i+1] == '\\') {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '"' {
			return b.String(), i + 1
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), n
}

func readUnquoted(s string, start int) (string, int) {
	i := start
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return s[start:i], i
}

// readBareToken reads a non-operator token until whitespace, keeping any quoted
// suffix (e.g. foo:"bar baz") attached to the same token.
func readBareToken(s string, start int) (string, int) {
	var b strings.Builder
	i := start
	n := len(s)
	for i < n {
		if s[i] == '"' {
			quoted, next := readQuoted(s, i)
			b.WriteString(quoted)
			i = next
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) {
			break
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String(), i
}

func hasFTSToken(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

package search

import (
	"strings"
	"time"
	"unicode"
)

const messageSelectColumns = `m.pk,
    m.id,
    m.project_id,
    m.smtp_credential_id,
    m.received_at,
    m.subject,
    m.from_name,
    m.from_addr,
    m.to_json,
    m.cc_json,
    m.bcc_json,
    m.reply_to_json,
    m.envelope_from,
    m.envelope_to_json,
    m.message_id_header,
    m.date_header,
    m.snippet,
    m.has_html,
    m.has_text,
    m.text_generated,
    m.attachment_count,
    m.size_bytes,
    m.blob_path,
    m.seen,
    m.remote_ip,
    m.helo,
    m.auth_user,
    m.tls,
    m.parse_warnings_json`

// BuildOptions specifies non-query filters and pagination bounds combined into
// the generated message search SQL.
type BuildOptions struct {
	Tag          string
	CredentialID *int64
	BeforeID     string
	AfterID      string
	SinceTime    *time.Time
	MessageID    string
	Limit        int64
}

// Build constructs a parameterized SELECT statement over messages (and messages_fts
// when FTS terms are present) strictly scoped to projectID (ARCHITECTURE 3.7, SPEC FR-080/FR-081).
// User input is placed exclusively in the returned args slice.
func Build(projectID int64, q Query, opts BuildOptions) (string, []any) {
	return buildQuery("SELECT "+messageSelectColumns, projectID, q, opts, true)
}

// BuildDeleteSelect constructs a parameterized SELECT statement returning
// (pk, id, blob_path, size_bytes, seen) for all messages in projectID matching q.
// Used by filtered delete-all (DELETE /api/v1/projects/{project}/messages?query=...).
func BuildDeleteSelect(projectID int64, q Query) (string, []any) {
	return buildQuery("SELECT m.pk, m.id, m.blob_path, m.size_bytes, m.seen", projectID, q, BuildOptions{}, false)
}

func buildQuery(selectClause string, projectID int64, q Query, opts BuildOptions, ordered bool) (string, []any) {
	var (
		posFTS     []string
		negFTS     []string
		where      []string
		args       []any
		impossible bool
	)

	// Always scope the outer query to projectID first (tenant isolation).
	where = append(where, "m.project_id = ?")
	args = append(args, projectID)

	for _, term := range q.Terms {
		switch term.Kind {
		case TermText, TermFrom, TermTo, TermSubject:
			expr, ok := buildFTSExpr(term)
			if !ok {
				// A positive search term containing no FTS-indexable tokens can never match.
				if !term.Negated {
					impossible = true
				}
				continue
			}
			if term.Negated {
				negFTS = append(negFTS, expr)
			} else {
				posFTS = append(posFTS, expr)
			}

		case TermHasAttachment:
			if term.Negated {
				where = append(where, "m.attachment_count = 0")
			} else {
				where = append(where, "m.attachment_count > 0")
			}

		case TermHasHTML:
			if term.Negated {
				where = append(where, "m.has_html = 0")
			} else {
				where = append(where, "m.has_html = 1")
			}

		case TermHasText:
			if term.Negated {
				where = append(where, "m.has_text = 0")
			} else {
				where = append(where, "m.has_text = 1")
			}

		case TermIsUnread:
			if term.Negated {
				where = append(where, "m.seen = 1")
			} else {
				where = append(where, "m.seen = 0")
			}

		case TermIsRead:
			if term.Negated {
				where = append(where, "m.seen = 0")
			} else {
				where = append(where, "m.seen = 1")
			}

		case TermTag:
			if term.Negated {
				where = append(where, "NOT EXISTS (SELECT 1 FROM message_tags mt JOIN tags t ON t.id = mt.tag_id AND t.project_id = mt.project_id WHERE mt.project_id = ? AND mt.message_pk = m.pk AND t.name = ? COLLATE NOCASE)")
			} else {
				where = append(where, "EXISTS (SELECT 1 FROM message_tags mt JOIN tags t ON t.id = mt.tag_id AND t.project_id = mt.project_id WHERE mt.project_id = ? AND mt.message_pk = m.pk AND t.name = ? COLLATE NOCASE)")
			}
			args = append(args, projectID, term.Value)

		case TermBefore:
			if term.Negated {
				where = append(where, "m.received_at >= ?")
			} else {
				where = append(where, "m.received_at < ?")
			}
			args = append(args, term.Time.UTC().UnixMilli())

		case TermAfter:
			if term.Negated {
				where = append(where, "m.received_at < ?")
			} else {
				where = append(where, "m.received_at >= ?")
			}
			args = append(args, term.Time.UTC().UnixMilli())

		case TermCredential:
			if term.Negated {
				where = append(where, "NOT EXISTS (SELECT 1 FROM smtp_credentials sc WHERE sc.project_id = ? AND sc.id = m.smtp_credential_id AND sc.label = ? COLLATE NOCASE)")
			} else {
				where = append(where, "EXISTS (SELECT 1 FROM smtp_credentials sc WHERE sc.project_id = ? AND sc.id = m.smtp_credential_id AND sc.label = ? COLLATE NOCASE)")
			}
			args = append(args, projectID, term.Value)
		}
	}

	if opts.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM message_tags mt JOIN tags t ON t.id = mt.tag_id AND t.project_id = mt.project_id WHERE mt.project_id = ? AND mt.message_pk = m.pk AND t.name = ? COLLATE NOCASE)")
		args = append(args, projectID, opts.Tag)
	}
	if opts.CredentialID != nil {
		where = append(where, "m.smtp_credential_id = ?")
		args = append(args, *opts.CredentialID)
	}
	if opts.BeforeID != "" {
		where = append(where, "m.id < ?")
		args = append(args, opts.BeforeID)
	}
	if opts.AfterID != "" {
		where = append(where, "m.id > ?")
		args = append(args, opts.AfterID)
	}
	if opts.SinceTime != nil {
		where = append(where, "m.received_at >= ?")
		args = append(args, opts.SinceTime.UTC().UnixMilli())
	}
	if opts.MessageID != "" {
		where = append(where, "m.id = ?")
		args = append(args, opts.MessageID)
	}

	var b strings.Builder
	b.WriteString(selectClause)
	b.WriteString("\nFROM messages m")

	if len(posFTS) > 0 && !impossible {
		b.WriteString("\nJOIN messages_fts ON messages_fts.rowid = m.pk")
		// Insert positive FTS MATCH clause right after project_id predicate.
		ftsMatch := strings.Join(posFTS, " ")
		where = append(where, "messages_fts MATCH ?")
		args = append(args, ftsMatch)
	}

	if impossible {
		where = append(where, "1 = 0")
	} else {
		for _, expr := range negFTS {
			where = append(where, "m.pk NOT IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)")
			args = append(args, expr)
		}
	}

	b.WriteString("\nWHERE ")
	b.WriteString(strings.Join(where, "\n  AND "))

	if ordered {
		b.WriteString("\nORDER BY m.id DESC")
	}
	if opts.Limit > 0 {
		b.WriteString("\nLIMIT ?")
		args = append(args, opts.Limit)
	}

	return b.String(), args
}

// buildFTSExpr converts a single FTS term into a safely quoted FTS5 MATCH fragment.
// Returns ("", false) when the cleaned value contains no FTS5-indexable tokens.
func buildFTSExpr(term Term) (string, bool) {
	cleaned := sanitizeFTSInput(term.Value)
	if cleaned == "" {
		return "", false
	}

	// Escape double quotes per FTS5 string literal syntax.
	escaped := strings.ReplaceAll(cleaned, `"`, `""`)
	literal := `"` + escaped + `"`
	if term.Prefix {
		literal += "*"
	}

	switch term.Kind {
	case TermFrom:
		return "from_text : " + literal, true
	case TermTo:
		return "to_text : " + literal, true
	case TermSubject:
		return "subject : " + literal, true
	default:
		return literal, true
	}
}

// sanitizeFTSInput strips NUL bytes and trims characters that are never part of
// unicode61 tokens from the edges so FTS5 never encounters an empty phrase or a
// trailing separator before '*'. Returns "" if no letter or digit remains.
func sanitizeFTSInput(raw string) string {
	noNul := strings.ReplaceAll(raw, "\x00", " ")
	trimmed := strings.TrimFunc(noNul, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if !hasFTSToken(trimmed) {
		return ""
	}
	return trimmed
}

package search_test

import (
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// FuzzParseAndBuild verifies FR-081: the search parser and SQL builder never
// panic on arbitrary input, never place user text outside bind parameters, and
// always produce SQL that SQLite FTS5 executes without syntax errors.
func FuzzParseAndBuild(f *testing.F) {
	seeds := []string{
		"",
		"hello",
		"joao fatura",
		`"exact phrase"`,
		`-"negated phrase"`,
		`from:alice subject:"order 12" has:attachment -tag:spam after:2026-11-01`,
		`before:2026-11-01T00:00:00Z after:2026-10-01`,
		`before:invalid`,
		`is:unread -is:read has:html -has:text`,
		`to:"Bob <bob@example.com>"`,
		`subject:"unclosed quote`,
		`"""`,
		`---`,
		`??? !!! ***`,
		`a* b* "c*"`,
		`OR AND NOT NEAR (foo)`,
		`'; DROP TABLE messages; --`,
		`from: to: subject: tag: has: is: before: after:`,
		`foo:bar baz:"qux \"quoted\""`,
		"\x00\xff\xfe\"'`:*()-",
		"café naïve résumé 日本語 Кириллица",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	st := testutil.NewStore(f)

	f.Fuzz(func(t *testing.T, input string) {
		// 1. Parse + Build must never panic.
		q, err := search.Parse(input)
		if err != nil {
			return
		}

		sqlStr, args := search.Build(1, q, search.BuildOptions{Limit: 10})

		// 2. Verify user text never leaks outside bind parameters by checking
		// an appended unique sentinel cannot appear in the generated SQL.
		const sentinel = "FUZZSENTINEL777"
		if qWithSentinel, sErr := search.Parse(input + " " + sentinel); sErr == nil {
			sSQL, _ := search.Build(1, qWithSentinel, search.BuildOptions{Limit: 10})
			if strings.Contains(sSQL, sentinel) {
				t.Fatalf("SQL leaked user text outside bind args:\ninput: %q\nsql: %s", input, sSQL)
			}
		}

		// 3. Every built query must execute cleanly against real SQLite FTS5.
		rows, execErr := st.ReaderDB().QueryContext(t.Context(), sqlStr, args...)
		if execErr != nil {
			t.Fatalf("SQLite rejected built query:\ninput: %q\nsql: %s\nargs: %#v\nerr: %v", input, sqlStr, args, execErr)
		}
		_ = rows.Close()
	})
}

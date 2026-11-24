package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	sqlcNameRe       = regexp.MustCompile(`^--\s*name:\s*([A-Za-z0-9_]+)\s*:`)
	isolationAnnotRe = regexp.MustCompile(`(?i)^--\s*isolation:\s*global\s*\(\s*\S[^)]*\)\s*$`)
	tenantTableRefRe = regexp.MustCompile(`(?i)\b(?:from|join|into|update)\s+(?:main\.)?"?(messages|attachments|tags|message_tags|smtp_credentials|invites|project_members)"?(?:\s+(?:as\s+)?([a-zA-Z_][a-zA-Z0-9_]*))?`)
	insertStmtRe     = regexp.MustCompile(`(?i)^\s*(?:with\b[\s\S]*?\)\s*)?insert\b`)
	insertColsRe     = regexp.MustCompile(`(?is)\binto\s+(?:main\.)?"?[a-zA-Z_][a-zA-Z0-9_]*"?\s*\(([^)]*)\)`)
	unqualProjPredRe = regexp.MustCompile(`(?i)(?:^|[^.\w])project_id\s*(?:=|in\b)`)
	usingProjRe      = regexp.MustCompile(`(?i)\busing\s*\([^)]*\bproject_id\b`)
	qualProjEqQualRe = regexp.MustCompile(`(?i)\b([a-zA-Z_][a-zA-Z0-9_]*)\.project_id\s*=\s*([a-zA-Z_][a-zA-Z0-9_]*)\.project_id\b`)
	qualProjPredRe   = regexp.MustCompile(`(?i)\b([a-zA-Z_][a-zA-Z0-9_]*)\.project_id\s*(?:=\s*(?:\?|sqlc\.arg\b|\$|:[a-zA-Z_])|in\s*\()`)
	blockCommentRe   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

var sqlKeywords = map[string]struct{}{
	"where": {}, "set": {}, "on": {}, "using": {}, "join": {}, "left": {}, "right": {},
	"inner": {}, "outer": {}, "cross": {}, "full": {}, "natural": {}, "order": {},
	"group": {}, "limit": {}, "offset": {}, "having": {}, "union": {}, "intersect": {},
	"except": {}, "returning": {}, "values": {}, "select": {}, "and": {}, "or": {},
}

type tenantTableRef struct {
	table string
	alias string
}

func (r tenantTableRef) qualifier() string {
	if r.alias != "" {
		return r.alias
	}
	return r.table
}

func findTenantTableRefs(sqlText string) []tenantTableRef {
	matches := tenantTableRefRe.FindAllStringSubmatch(sqlText, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	refs := make([]tenantTableRef, 0, len(matches))
	for _, m := range matches {
		tbl := strings.ToLower(m[1])
		alias := strings.ToLower(strings.TrimSpace(m[2]))
		if _, isKW := sqlKeywords[alias]; isKW {
			alias = ""
		}
		ref := tenantTableRef{table: tbl, alias: alias}
		key := ref.table + ":" + ref.alias
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}

type parsedQuery struct {
	file        string
	name        string
	globalOK    bool
	rawSQL      string
	strippedSQL string
}

func parseSQLQueries(file, content string) []parsedQuery {
	lines := strings.Split(content, "\n")
	var (
		queries       []parsedQuery
		current       *parsedQuery
		pendingGlobal bool
	)

	flush := func() {
		if current != nil {
			current.strippedSQL = stripSQLComments(current.rawSQL)
			queries = append(queries, *current)
			current = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if m := sqlcNameRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			current = &parsedQuery{
				file:     file,
				name:     m[1],
				globalOK: pendingGlobal,
			}
			pendingGlobal = false
			continue
		}

		if isolationAnnotRe.MatchString(trimmed) {
			if current != nil && strings.TrimSpace(stripSQLComments(current.rawSQL)) == "" {
				current.globalOK = true
			} else {
				pendingGlobal = true
			}
			continue
		}

		if trimmed == "" {
			if current == nil {
				pendingGlobal = false
			}
			continue
		}

		if current != nil {
			current.rawSQL += line + "\n"
		}
	}
	flush()
	return queries
}

func stripSQLComments(s string) string {
	noBlock := blockCommentRe.ReplaceAllString(s, " ")
	lines := strings.Split(noBlock, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func checkQueryIsolation(q parsedQuery) error {
	refs := findTenantTableRefs(q.strippedSQL)
	if len(refs) == 0 {
		return nil
	}
	if q.globalOK {
		return nil
	}

	if insertStmtRe.MatchString(q.strippedSQL) {
		colsMatch := insertColsRe.FindStringSubmatch(q.strippedSQL)
		if colsMatch != nil && regexp.MustCompile(`(?i)\bproject_id\b`).MatchString(colsMatch[1]) {
			// If INSERT ... SELECT references additional tenant tables beyond the target, verify those too.
			if len(refs) == 1 {
				return nil
			}
			refs = refs[1:]
		} else {
			return fmt.Errorf("%s (%s): INSERT into tenant table %q must include project_id in column list or '-- isolation: global (<reason>)'", q.file, q.name, refs[0].table)
		}
	}

	if usingProjRe.MatchString(q.strippedSQL) {
		return nil
	}

	// Collect all qualifiers directly filtered by project_id = ? / IN (...).
	constrained := make(map[string]bool)
	for _, m := range qualProjPredRe.FindAllStringSubmatch(q.strippedSQL, -1) {
		constrained[strings.ToLower(m[1])] = true
	}

	// When only a single tenant table is referenced, an unqualified project_id predicate applies to it.
	if len(refs) == 1 && unqualProjPredRe.MatchString(q.strippedSQL) {
		constrained[refs[0].qualifier()] = true
		constrained[refs[0].table] = true
	}

	// Propagate project_id constraint across explicit equijoins (a.project_id = b.project_id).
	edges := make(map[string][]string)
	for _, m := range qualProjEqQualRe.FindAllStringSubmatch(q.strippedSQL, -1) {
		a, b := strings.ToLower(m[1]), strings.ToLower(m[2])
		edges[a] = append(edges[a], b)
		edges[b] = append(edges[b], a)
	}
	for changed := true; changed; {
		changed = false
		for a, neighbors := range edges {
			if !constrained[a] {
				continue
			}
			for _, b := range neighbors {
				if !constrained[b] {
					constrained[b] = true
					changed = true
				}
			}
		}
	}

	for _, ref := range refs {
		if !constrained[ref.qualifier()] && !constrained[ref.table] {
			return fmt.Errorf("%s (%s): tenant table %q (qualifier %q) must filter by project_id or carry '-- isolation: global (<reason>)'", q.file, q.name, ref.table, ref.qualifier())
		}
	}
	return nil
}

func TestQueriesTenantIsolation(t *testing.T) {
	t.Parallel()

	queriesFS := os.DirFS("queries")
	files, err := fs.Glob(queriesFS, "*.sql")
	if err != nil {
		t.Fatalf("glob queries: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no SQL query files found under queries/")
	}

	totalQueries := 0
	for _, name := range files {
		data, err := fs.ReadFile(queriesFS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		path := filepath.Join("queries", name)
		queries := parseSQLQueries(path, string(data))
		if len(queries) == 0 {
			t.Errorf("%s: contains no named sqlc queries", path)
			continue
		}
		for _, q := range queries {
			totalQueries++
			if err := checkQueryIsolation(q); err != nil {
				t.Error(err)
			}
		}
	}
	if totalQueries == 0 {
		t.Fatalf("expected at least one named query")
	}
}

func TestIsolationLinterRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{
			name: "non-tenant table passes",
			sql: `-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;`,
			wantErr: false,
		},
		{
			name: "tenant select with project_id filter passes",
			sql: `-- name: GetMessage :one
SELECT id, subject FROM messages WHERE project_id = ? AND id = ?;`,
			wantErr: false,
		},
		{
			name: "tenant select missing project_id fails",
			sql: `-- name: BadGetMessage :one
SELECT id, subject FROM messages WHERE id = ?;`,
			wantErr: true,
		},
		{
			name: "tenant select with project_id only in projection fails",
			sql: `-- name: SneakyGetMessage :one
SELECT id, project_id FROM messages WHERE id = ?;`,
			wantErr: true,
		},
		{
			name: "tenant select with project_id only in comment fails",
			sql: `-- name: CommentOnly :one
SELECT id FROM messages -- WHERE project_id = ?
WHERE id = ?;`,
			wantErr: true,
		},
		{
			name: "tenant select with global annotation after name passes",
			sql: `-- name: LookupCredential :one
-- isolation: global (SMTP AUTH looks up credential by unique username)
SELECT id, project_id, password_enc FROM smtp_credentials WHERE username = ?;`,
			wantErr: false,
		},
		{
			name: "tenant select with global annotation before name passes",
			sql: `-- isolation: global (admin cleanup)
-- name: DeleteExpiredInvites :exec
DELETE FROM invites WHERE expires_at < ?;`,
			wantErr: false,
		},
		{
			name: "tenant select with empty global reason fails",
			sql: `-- name: EmptyReason :one
-- isolation: global ()
SELECT id FROM messages WHERE id = ?;`,
			wantErr: true,
		},
		{
			name: "tenant insert with project_id passes",
			sql: `-- name: InsertTag :one
INSERT INTO tags (project_id, name, color, created_at) VALUES (?, ?, ?, ?) RETURNING *;`,
			wantErr: false,
		},
		{
			name: "tenant insert without project_id fails",
			sql: `-- name: BadInsertAttachment :exec
INSERT INTO attachments (message_pk, part_path) VALUES (?, ?);`,
			wantErr: true,
		},
		{
			name: "tenant update with project_id passes",
			sql: `-- name: MarkSeen :exec
UPDATE messages SET seen = 1 WHERE project_id = ? AND id = ?;`,
			wantErr: false,
		},
		{
			name: "tenant delete without project_id fails",
			sql: `-- name: BadDeleteTag :exec
DELETE FROM tags WHERE id = ?;`,
			wantErr: true,
		},
		{
			name: "multi-table tenant join filtering only one table fails",
			sql: `-- name: BadJoinMissingSecondFilter :many
SELECT m.id FROM messages m JOIN attachments a ON a.message_pk = m.pk WHERE m.project_id = ?;`,
			wantErr: true,
		},
		{
			name: "multi-table tenant join with equijoin on project_id passes",
			sql: `-- name: GoodJoinEqui :many
SELECT m.id FROM messages m JOIN attachments a ON a.message_pk = m.pk AND a.project_id = m.project_id WHERE m.project_id = ?;`,
			wantErr: false,
		},
		{
			name: "multi-table tenant join with explicit filter on both passes",
			sql: `-- name: GoodJoinBothFiltered :many
SELECT t.name FROM tags t JOIN message_tags mt ON mt.tag_id = t.id WHERE t.project_id = ? AND mt.project_id = ?;`,
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			qs := parseSQLQueries("test.sql", tc.sql)
			if len(qs) != 1 {
				t.Fatalf("expected 1 parsed query, got %d", len(qs))
			}
			err := checkQueryIsolation(qs[0])
			if tc.wantErr && err == nil {
				t.Fatalf("expected isolation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected isolation error: %v", err)
			}
		})
	}
}

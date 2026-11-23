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
	tenantTableRe    = regexp.MustCompile(`(?i)\b(?:from|join|into|update)\s+(?:main\.)?"?(messages|attachments|tags|message_tags|smtp_credentials|invites|project_members)"?\b`)
	insertStmtRe     = regexp.MustCompile(`(?i)^\s*(?:with\b[\s\S]*?\)\s*)?insert\b`)
	projectIDWordRe  = regexp.MustCompile(`(?i)\bproject_id\b`)
	projectIDPredRe  = regexp.MustCompile(`(?i)(?:\bproject_id\s*(?:=|in\b)|using\s*\([^)]*\bproject_id\b)`)
	blockCommentRe   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

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
	m := tenantTableRe.FindStringSubmatch(q.strippedSQL)
	if m == nil {
		return nil
	}
	if q.globalOK {
		return nil
	}

	table := strings.ToLower(m[1])
	if insertStmtRe.MatchString(q.strippedSQL) {
		if projectIDWordRe.MatchString(q.strippedSQL) {
			return nil
		}
		return fmt.Errorf("%s (%s): INSERT into tenant table %q must include project_id or '-- isolation: global (<reason>)'", q.file, q.name, table)
	}

	if projectIDPredRe.MatchString(q.strippedSQL) {
		return nil
	}
	return fmt.Errorf("%s (%s): query on tenant table %q must filter by project_id or carry '-- isolation: global (<reason>)'", q.file, q.name, table)
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

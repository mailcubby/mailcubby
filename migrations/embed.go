// Package migrations embeds the SQLite goose SQL migration files.
package migrations

import "embed"

// FS holds all embedded SQL migration files.
//
//go:embed *.sql
var FS embed.FS

package migrations

import "embed"

// Files contains Samo's ordered SQLite migrations.
//
//go:embed *.sql
var Files embed.FS

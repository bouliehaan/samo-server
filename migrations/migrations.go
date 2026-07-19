// Package migrations embeds samo-server's ordered PostgreSQL migrations.
//
// The lineage is append-only: 0001_init.sql is the consolidated schema frozen
// at the moment the SQLite backend was retired (it was originally generated
// from the cumulative SQLite migrations, and existing databases have already
// applied it — never edit it). Every schema change since is a new numbered
// file: copy the next number, write plain Postgres DDL, and ApplyMigrations
// delivers it to old and new databases alike.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed postgres/*.sql
var root embed.FS

// Files contains the ordered Postgres migrations, rooted so ApplyMigrations
// sees "0001_init.sql" etc. at the FS root.
var Files fs.FS = mustSub(root, "postgres")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("migrations: embed postgres subtree: " + err.Error())
	}
	return sub
}

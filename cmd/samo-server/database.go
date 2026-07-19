package main

import (
	"context"
	"database/sql"
	"strings"

	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

// openAndMigrate opens the write handle and brings the schema up to date.
// Callers own the returned handle and must Close it.
func openAndMigrate(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	db, err := storage.Open(ctx, cfg.DBDSN)
	if err != nil {
		return nil, err
	}
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// redactDSN hides the password when echoing a DSN into the log.
func redactDSN(dsn string) string {
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		if slash := strings.Index(dsn, "://"); slash >= 0 && slash+3 < at {
			creds := dsn[slash+3 : at]
			if colon := strings.Index(creds, ":"); colon >= 0 {
				return dsn[:slash+3] + creds[:colon] + ":****" + dsn[at:]
			}
		}
	}
	return dsn
}

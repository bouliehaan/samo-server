# Samo is Postgres-only

Samo used to run on SQLite, then briefly on either engine while the Postgres
port was proven out. That transition is complete: **current builds require
PostgreSQL** (`SAMO_DB_DSN=postgres://...`), and the SQLite backend, the
schema generator, and the built-in data migrator have been removed.

## Fresh install

Nothing to migrate. From the `samo-server` folder:

```bash
cp .env.example .env    # edit POSTGRES_PASSWORD, media paths
docker compose up -d --build
```

Open http://localhost:6969 and finish setup in the browser. Schema setup and
upgrades are automatic: the server applies `migrations/postgres/*.sql` on
every boot.

## Still on an old SQLite install?

Migrate with a **pre-removal build**, then upgrade. The last builds that
carried the dual backend include a `migrate-postgres` subcommand that copies
every table from `samo.db` into Postgres inside one transaction and verifies
every table's row count before declaring success:

```bash
# from a checkout that still contains cmd/samo-server/migrate_postgres.go
git log --oneline -- cmd/samo-server/migrate_postgres.go   # find such a commit
docker compose up -d db
docker compose run --rm server migrate-postgres --sqlite /data/samo.db
```

The old step-by-step guide travels with those checkouts (this file, in their
version of the repo). Once the copy verifies and the server runs clean on
Postgres, upgrade to a current build and delete the old `samo.db`.

## Running the tests

The suite runs against a real PostgreSQL — every test gets its own database
cloned from a migrated template, so tests exercise exactly the dialect and
driver production uses:

```bash
make test          # starts a disposable postgres:16 container on :55432, runs go test ./...
```

Or point the tests at your own throwaway server:

```bash
export SAMO_TEST_PG_DSN='postgres://samo:samo@localhost:5432/samo?sslmode=disable'
go test ./...
```

The account needs permission to CREATE/DROP databases (the container's
default superuser does). Never point this at a database you care about.

## Adding a schema change

The migration lineage lives in `migrations/postgres/` and is append-only:

1. Add the next numbered file, e.g. `0005_my_change.sql`, containing plain
   PostgreSQL DDL/DML. Make it idempotent-friendly where cheap
   (`IF NOT EXISTS`, `ON CONFLICT DO NOTHING`) — but it runs exactly once per
   database either way, recorded in `schema_migrations`.
2. Never edit an existing migration: databases that already applied it will
   never re-run it, so edits silently fork reality. (`0001_init.sql` is the
   frozen consolidated schema from the SQLite era — same rule.)
3. `make test` — the test template rebuilds itself automatically whenever the
   migration set changes.

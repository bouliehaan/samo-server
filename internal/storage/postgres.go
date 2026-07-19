package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
)

// pgDriverName is the database/sql driver we register: pgx's standard-library
// driver, wrapped so that every query string has its `?` placeholders rewritten
// to `$1, $2, ...` before pgx sees it. See rewrite.go for why.
const pgDriverName = "samo-pgx"

func init() {
	sql.Register(pgDriverName, rewriteDriver{base: stdlib.GetDefaultDriver()})
}

// ---------------------------------------------------------------------------
// database/sql driver wrapper: pgx + `?` → `$N` rewriting
// ---------------------------------------------------------------------------

type rewriteDriver struct{ base driver.Driver }

func (d rewriteDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &rewriteConn{Conn: c}, nil
}

// OpenConnector preserves pgx's DriverContext support so the DSN is parsed once
// per pool rather than once per connection.
func (d rewriteDriver) OpenConnector(name string) (driver.Connector, error) {
	if dc, ok := d.base.(driver.DriverContext); ok {
		inner, err := dc.OpenConnector(name)
		if err != nil {
			return nil, err
		}
		return rewriteConnector{inner: inner, drv: d}, nil
	}
	return dsnConnector{name: name, drv: d}, nil
}

type rewriteConnector struct {
	inner driver.Connector
	drv   rewriteDriver
}

func (c rewriteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rewriteConn{Conn: conn}, nil
}

func (c rewriteConnector) Driver() driver.Driver { return c.drv }

// dsnConnector is the fallback when the base driver lacks DriverContext.
type dsnConnector struct {
	name string
	drv  rewriteDriver
}

func (c dsnConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.drv.Open(c.name)
}

func (c dsnConnector) Driver() driver.Driver { return c.drv }

// rewriteConn wraps a pgx driver.Conn and rewrites placeholders on every path
// that carries a query string. Everything else delegates to the embedded conn.
type rewriteConn struct {
	driver.Conn
}

var (
	_ driver.QueryerContext     = (*rewriteConn)(nil)
	_ driver.ExecerContext      = (*rewriteConn)(nil)
	_ driver.ConnPrepareContext = (*rewriteConn)(nil)
	_ driver.ConnBeginTx        = (*rewriteConn)(nil)
	_ driver.Pinger             = (*rewriteConn)(nil)
	_ driver.SessionResetter    = (*rewriteConn)(nil)
	_ driver.Validator          = (*rewriteConn)(nil)
	_ driver.NamedValueChecker  = (*rewriteConn)(nil)
)

func (c *rewriteConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(rewritePlaceholders(query))
}

func (c *rewriteConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, rewritePlaceholders(query))
	}
	return c.Conn.Prepare(rewritePlaceholders(query))
}

func (c *rewriteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, rewritePlaceholders(query), args)
	}
	return nil, driver.ErrSkip
}

func (c *rewriteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, rewritePlaceholders(query), args)
	}
	return nil, driver.ErrSkip
}

func (c *rewriteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *rewriteConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *rewriteConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *rewriteConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// CheckNamedValue defers argument type handling to pgx, which accepts a far
// wider set of Go types than database/sql's default checker. Without this,
// database/sql would reject values pgx could otherwise encode.
func (c *rewriteConn) CheckNamedValue(nv *driver.NamedValue) error {
	// PostgreSQL strictly rejects null bytes (\x00) in text columns. Since Go
	// strings and SQLite allow them, they can easily slip in from ID3 tags or
	// APIs. Strip them here so we don't crash the transaction.
	if s, ok := nv.Value.(string); ok && strings.Contains(s, "\x00") {
		nv.Value = strings.ReplaceAll(s, "\x00", "")
	}

	if chk, ok := c.Conn.(driver.NamedValueChecker); ok {
		return chk.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

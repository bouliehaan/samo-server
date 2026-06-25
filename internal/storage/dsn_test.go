package storage

import (
	"context"
	"database/sql"
	"testing"
)

func TestOpenBusyTimeoutOnPooledConnection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if max := db.Stats().MaxOpenConnections; max != 16 {
		t.Fatalf("max open connections = %d, want 16", max)
	}

	check := func(conn *sql.Conn) error {
		var timeout int
		return conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout)
	}

	conn1, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := check(conn1); err != nil {
		t.Fatal(err)
	}
	var t1 int
	if err := conn1.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&t1); err != nil {
		t.Fatal(err)
	}
	if t1 < 60000 {
		t.Fatalf("busy_timeout on conn1 = %d, want >= 60000", t1)
	}
	if err := conn1.Close(); err != nil {
		t.Fatal(err)
	}

	conn2, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	var t2 int
	if err := conn2.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&t2); err != nil {
		t.Fatal(err)
	}
	if t2 < 60000 {
		t.Fatalf("busy_timeout on conn2 = %d, want >= 60000", t2)
	}
}

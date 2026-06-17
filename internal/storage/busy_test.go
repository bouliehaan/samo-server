package storage

import (
	"errors"
	"testing"
)

func TestIsBusy(t *testing.T) {
	if IsBusy(nil) {
		t.Fatal("nil should not be busy")
	}
	if !IsBusy(errors.New("database is locked (5) (SQLITE_BUSY)")) {
		t.Fatal("expected busy")
	}
	if !IsBusy(errors.New("database table is locked (6) (SQLITE_LOCKED)")) {
		t.Fatal("expected table lock to be busy")
	}
	if !IsBusy(errors.New("constraint while opening database: code 5")) {
		t.Fatal("expected numeric sqlite busy code to be busy")
	}
	if IsBusy(errors.New("constraint failed")) {
		t.Fatal("constraint should not be busy")
	}
}

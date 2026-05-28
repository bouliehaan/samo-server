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
	if IsBusy(errors.New("constraint failed")) {
		t.Fatal("constraint should not be busy")
	}
}

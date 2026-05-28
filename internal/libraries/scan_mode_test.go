package libraries

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestResolveScanMode(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db, scanner.New(db))
	musicDir := filepath.Join(root, "music")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateLibraryInput{
		Name: "Music",
		Kind: KindMusic,
		Path: musicDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	scanLib := toScannerLibrary(created)

	if mode := service.resolveScanMode(ctx, TriggerStartup, "", []scanner.Library{scanLib}); mode != ScanModeFull {
		t.Fatalf("never-scanned startup mode = %q, want full", mode)
	}
	if mode := service.resolveScanMode(ctx, TriggerAPI, "", []scanner.Library{scanLib}); mode != ScanModeQuick {
		t.Fatalf("api mode = %q, want quick", mode)
	}
	if mode := service.resolveScanMode(ctx, TriggerAPI, ScanModeFull, []scanner.Library{scanLib}); mode != ScanModeFull {
		t.Fatalf("explicit full api mode = %q, want full", mode)
	}
	if mode := service.resolveScanMode(ctx, TriggerStartup, ScanModeQuick, []scanner.Library{scanLib}); mode != ScanModeQuick {
		t.Fatalf("explicit quick mode = %q, want quick", mode)
	}

	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = ? WHERE id = ?`, now.Format(time.RFC3339), created.ID); err != nil {
		t.Fatal(err)
	}
	if mode := service.resolveScanMode(ctx, TriggerStartup, "", []scanner.Library{scanLib}); mode != ScanModeQuick {
		t.Fatalf("previously scanned startup mode = %q, want quick", mode)
	}
	if mode := service.resolveScanMode(ctx, TriggerFilesystem, "", []scanner.Library{scanLib}); mode != ScanModeQuick {
		t.Fatalf("filesystem mode = %q, want quick", mode)
	}
}

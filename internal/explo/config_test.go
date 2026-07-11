package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

// newConfigTestService builds a Service wired to a migrated+seeded DB with the
// given environment folder/key defaults. fpcalc is a non-empty sentinel (the
// config-layer methods only check that a path is set, they never exec it).
func newConfigTestService(t *testing.T, db *sql.DB, envDirs []string, envKey string) *Service {
	t.Helper()
	return NewService(ServiceOptions{
		DB:             db,
		Dirs:           envDirs,
		AcoustIDAPIKey: envKey,
		FpcalcPath:     "/fake/fpcalc",
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:      playlists.New(db),
	})
}

func TestConfigReportsEnvironmentSource(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	svc := newConfigTestService(t, db, []string{exploDir}, "env-key")

	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "environment" {
		t.Fatalf("Source = %q, want environment", cfg.Source)
	}
	if cfg.Folder != exploDir {
		t.Fatalf("Folder = %q, want %q", cfg.Folder, exploDir)
	}
	if !cfg.HasAPIKey || !cfg.FpcalcReady || !cfg.Enabled {
		t.Fatalf("expected fully enabled env config, got %#v", cfg)
	}
	if !svc.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
}

func TestSaveConfigPersistsFolderAndFallsBackToEnvKey(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	// No env folder - the admin picks it in the UI. Env supplies only the key.
	svc := newConfigTestService(t, db, nil, "env-key")
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.Enabled() {
		t.Fatal("expected disabled before a folder is configured")
	}

	cfg, err := svc.SaveConfig(ctx, AppConfigInput{Folder: exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "ui" || cfg.Folder != exploDir || !cfg.Enabled {
		t.Fatalf("post-save config = %#v", cfg)
	}
	if !svc.Enabled() {
		t.Fatal("Enabled() = false after save, want true")
	}
	if got := svc.effectiveDirs(); len(got) != 1 || got[0] != exploDir {
		t.Fatalf("effectiveDirs = %v, want [%s]", got, exploDir)
	}
	// The blank UI key must NOT copy the env secret into the DB - env stays the
	// source of truth.
	var storedKey string
	if err := db.QueryRowContext(ctx, `SELECT acoustid_api_key FROM explo_config WHERE id = 1`).Scan(&storedKey); err != nil {
		t.Fatal(err)
	}
	if storedKey != "" {
		t.Fatalf("stored key = %q, want empty (env fallback)", storedKey)
	}
	if svc.effectiveKey() != "env-key" {
		t.Fatalf("effectiveKey = %q, want env-key", svc.effectiveKey())
	}
	// The newly-configured folder's tracks are now pipeline candidates.
	candidates, err := svc.findCandidateTracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
}

func TestSaveConfigRequiresFolder(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	svc := newConfigTestService(t, db, nil, "env-key")
	if _, err := svc.SaveConfig(ctx, AppConfigInput{Folder: "   "}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestSaveConfigRequiresKeyWhenNoEnvOrPersisted(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	svc := newConfigTestService(t, db, nil, "") // no env key
	if _, err := svc.SaveConfig(ctx, AppConfigInput{Folder: exploDir}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestSaveConfigStoresExplicitKeyButNeverEchoesIt(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	svc := newConfigTestService(t, db, nil, "") // no env key; key comes from UI

	const secret = "ui-secret-key"
	cfg, err := svc.SaveConfig(ctx, AppConfigInput{Folder: exploDir, APIKey: secret})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasAPIKey || !cfg.Enabled {
		t.Fatalf("config = %#v", cfg)
	}
	if svc.effectiveKey() != secret {
		t.Fatalf("effectiveKey = %q, want %q", svc.effectiveKey(), secret)
	}
	var storedKey string
	if err := db.QueryRowContext(ctx, `SELECT acoustid_api_key FROM explo_config WHERE id = 1`).Scan(&storedKey); err != nil {
		t.Fatal(err)
	}
	if storedKey != secret {
		t.Fatalf("stored key = %q, want %q", storedKey, secret)
	}
	// The AcoustID secret must never be serialized back to an admin client.
	blob, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatalf("serialized config leaked the API key: %s", blob)
	}
}

func TestClearConfigDisablesEvenWithEnvVarsSet(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	// Env fully configures the feature...
	svc := newConfigTestService(t, db, []string{exploDir}, "env-key")
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if !svc.Enabled() {
		t.Fatal("expected enabled via env before clear")
	}
	// ...but an explicit UI clear pauses it regardless.
	cfg, err := svc.ClearConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.Source != "ui" {
		t.Fatalf("post-clear config = %#v", cfg)
	}
	if svc.Enabled() {
		t.Fatal("Enabled() = true after clear, want false")
	}
	if got := svc.effectiveDirs(); len(got) != 0 {
		t.Fatalf("effectiveDirs = %v, want empty after clear", got)
	}
}

func TestSaveConfigFolderOverridesEnvDir(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	svc := newConfigTestService(t, db, []string{"/music/somewhere-else"}, "env-key")
	if _, err := svc.SaveConfig(ctx, AppConfigInput{Folder: exploDir}); err != nil {
		t.Fatal(err)
	}
	if got := svc.effectiveDirs(); len(got) != 1 || got[0] != exploDir {
		t.Fatalf("effectiveDirs = %v, want [%s]", got, exploDir)
	}
}

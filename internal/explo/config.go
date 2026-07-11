package explo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrDisabled is returned when the explo service has no database and so
	// can't load or persist configuration.
	ErrDisabled = errors.New("explo integration is not available")
	// ErrInvalidConfig is returned when a SaveConfig request is missing a
	// required field (a folder, or an AcoustID API key with no env fallback).
	ErrInvalidConfig = errors.New("invalid explo configuration")
)

// AppConfig is the admin-facing view of the explo configuration returned by
// the config API. The AcoustID key is never echoed back - only whether one is
// set - so the secret can't leak back out through the settings screen.
type AppConfig struct {
	// Enabled reflects whether the pipeline would actually run with the
	// current config (folder + key + fpcalc all present and not paused).
	Enabled     bool       `json:"enabled"`
	Folder      string     `json:"folder"`
	HasAPIKey   bool       `json:"hasApiKey"`
	FpcalcReady bool       `json:"fpcalcReady"`
	Source      string     `json:"source,omitempty"` // "ui" | "environment"
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
}

// AppConfigInput is the admin-supplied settings payload. An empty APIKey means
// "keep whatever key is already effective" (previously persisted, or the env
// var), so the folder can be changed without re-entering the secret.
type AppConfigInput struct {
	Folder string `json:"folder"`
	APIKey string `json:"apiKey"`
}

type exploConfigRecord struct {
	Enabled   bool
	Folder    string
	APIKey    string
	UpdatedAt time.Time
}

func loadExploConfig(ctx context.Context, db *sql.DB) (exploConfigRecord, bool, error) {
	var enabled int
	var folder, apiKey, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT enabled, folder, acoustid_api_key, updated_at
		FROM explo_config
		WHERE id = 1`).Scan(&enabled, &folder, &apiKey, &updatedAt)
	if err == sql.ErrNoRows {
		return exploConfigRecord{}, false, nil
	}
	if err != nil {
		return exploConfigRecord{}, false, fmt.Errorf("load explo config: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		parsed = time.Now().UTC()
	}
	return exploConfigRecord{
		Enabled:   enabled != 0,
		Folder:    strings.TrimSpace(folder),
		APIKey:    strings.TrimSpace(apiKey),
		UpdatedAt: parsed,
	}, true, nil
}

func saveExploConfig(ctx context.Context, db *sql.DB, enabled bool, folder, apiKey string) (exploConfigRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO explo_config (id, enabled, folder, acoustid_api_key, updated_at)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			folder = excluded.folder,
			acoustid_api_key = excluded.acoustid_api_key,
			updated_at = excluded.updated_at`,
		boolToInt(enabled),
		strings.TrimSpace(folder),
		strings.TrimSpace(apiKey),
		now,
	)
	if err != nil {
		return exploConfigRecord{}, fmt.Errorf("save explo config: %w", err)
	}
	record, _, err := loadExploConfig(ctx, db)
	return record, err
}

// LoadConfig resolves the effective folder + AcoustID key from the persisted
// explo_config row, falling back to the environment-provided values when no
// row exists (or leaving the feature disabled when a row exists but is turned
// off). Call once at startup and again after any SaveConfig/ClearConfig so the
// live pipeline picks up the change without a restart.
func (s *Service) LoadConfig(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	record, ok, err := loadExploConfig(ctx, s.db)
	if err != nil {
		return err
	}
	if !ok {
		// No UI override yet: use the environment values as-is.
		s.applyConfig(s.envDirs, s.envKey, "environment", nil)
		return nil
	}
	updatedAt := record.UpdatedAt
	if !record.Enabled {
		// Explicitly paused from the UI - honor "off" even if env vars are set.
		s.applyConfig(nil, "", "ui", &updatedAt)
		return nil
	}
	dirs := s.envDirs
	if record.Folder != "" {
		dirs = []string{record.Folder}
	}
	key := s.envKey
	if record.APIKey != "" {
		key = record.APIKey
	}
	s.applyConfig(dirs, key, "ui", &updatedAt)
	return nil
}

// DisabledReason names the FIRST missing prerequisite keeping a configured
// explo folder feature off — the boot log's answer to "explo never runs and
// never says why". The old boot message only fired when the folder came from
// SAMO_EXPLO_DIRS, so a folder configured through the web UI that was missing
// fpcalc (or its API key) disabled itself in total silence. Returns "" when
// the feature is enabled OR has simply never been configured anywhere.
func (s *Service) DisabledReason(ctx context.Context) string {
	if s == nil || s.db == nil {
		return ""
	}
	if s.Enabled() {
		return ""
	}
	record, ok, err := loadExploConfig(ctx, s.db)
	if err != nil {
		return fmt.Sprintf("config read failed: %v", err)
	}
	folder := ""
	hasKey := s.envKey != ""
	if ok {
		if !record.Enabled {
			if record.Folder != "" || len(s.envDirs) > 0 {
				return "paused from the web UI (explo settings toggle is off)"
			}
			return ""
		}
		folder = record.Folder
		hasKey = hasKey || record.APIKey != ""
	}
	if folder == "" && len(s.envDirs) > 0 {
		folder = s.envDirs[0]
	}
	if folder == "" {
		// Never configured — nothing to warn about.
		return ""
	}
	if strings.TrimSpace(s.fpcalcPath) == "" {
		return "fpcalc not found (redeploy so bin/fpcalc sits beside samo-server, or set SAMO_FPCALC_PATH)"
	}
	if !hasKey {
		return "AcoustID API key missing (set it in the explo web UI or SAMO_ACOUSTID_API_KEY)"
	}
	if s.metadataApply == nil || s.playlists == nil {
		return "service dependencies not wired"
	}
	return "unknown (folder, key, and fpcalc all look present)"
}

// Config returns the admin-facing view of the current configuration for the
// settings API. It reads the persisted row (if any), overlaying environment
// fallbacks for blank fields so the reported state matches what the pipeline
// actually uses.
func (s *Service) Config(ctx context.Context) (AppConfig, error) {
	if s == nil || s.db == nil {
		return AppConfig{}, ErrDisabled
	}
	fpcalcReady := strings.TrimSpace(s.fpcalcPath) != ""
	record, ok, err := loadExploConfig(ctx, s.db)
	if err != nil {
		return AppConfig{}, err
	}
	if ok {
		updatedAt := record.UpdatedAt
		folder := record.Folder
		if folder == "" && len(s.envDirs) > 0 {
			folder = s.envDirs[0]
		}
		hasKey := record.APIKey != "" || s.envKey != ""
		return AppConfig{
			Enabled:     record.Enabled && folder != "" && hasKey && fpcalcReady,
			Folder:      folder,
			HasAPIKey:   hasKey,
			FpcalcReady: fpcalcReady,
			Source:      "ui",
			UpdatedAt:   &updatedAt,
		}, nil
	}
	folder := ""
	if len(s.envDirs) > 0 {
		folder = s.envDirs[0]
	}
	hasKey := s.envKey != ""
	return AppConfig{
		Enabled:     folder != "" && hasKey && fpcalcReady,
		Folder:      folder,
		HasAPIKey:   hasKey,
		FpcalcReady: fpcalcReady,
		Source:      "environment",
	}, nil
}

// SaveConfig persists an admin-chosen folder (and optionally an AcoustID key)
// and re-resolves the live config. An empty APIKey keeps the currently
// effective key (previously persisted, or the env var) so the folder can be
// updated without re-entering the secret. The key is only written to the
// database when it was actually supplied here - otherwise a blank key is
// stored and the env var remains the source, keeping the secret out of the DB.
func (s *Service) SaveConfig(ctx context.Context, input AppConfigInput) (AppConfig, error) {
	if s == nil || s.db == nil {
		return AppConfig{}, ErrDisabled
	}
	folder := strings.TrimSpace(input.Folder)
	if folder == "" {
		return AppConfig{}, fmt.Errorf("%w: a folder is required", ErrInvalidConfig)
	}
	persistKey := strings.TrimSpace(input.APIKey)
	if persistKey == "" {
		// Preserve any previously-persisted key; otherwise leave it blank so
		// the env var stays the source of truth.
		if record, ok, err := loadExploConfig(ctx, s.db); err != nil {
			return AppConfig{}, err
		} else if ok {
			persistKey = record.APIKey
		}
	}
	effectiveKey := persistKey
	if effectiveKey == "" {
		effectiveKey = s.envKey
	}
	if effectiveKey == "" {
		return AppConfig{}, fmt.Errorf("%w: an AcoustID API key is required (set it here or via SAMO_ACOUSTID_API_KEY)", ErrInvalidConfig)
	}
	if _, err := saveExploConfig(ctx, s.db, true, folder, persistKey); err != nil {
		return AppConfig{}, err
	}
	if err := s.LoadConfig(ctx); err != nil {
		return AppConfig{}, err
	}
	return s.Config(ctx)
}

// ClearConfig disables the feature from the UI and forgets the persisted
// folder/key. This pauses the pipeline even if SAMO_EXPLO_DIRS is still set,
// matching the "off means off" behavior of the Last.fm settings.
func (s *Service) ClearConfig(ctx context.Context) (AppConfig, error) {
	if s == nil || s.db == nil {
		return AppConfig{}, ErrDisabled
	}
	if _, err := saveExploConfig(ctx, s.db, false, "", ""); err != nil {
		return AppConfig{}, err
	}
	if err := s.LoadConfig(ctx); err != nil {
		return AppConfig{}, err
	}
	return s.Config(ctx)
}

// applyConfig atomically swaps in a new effective folder set + AcoustID key.
// Guarded by cfgMu so a SaveConfig from an HTTP handler can't race a
// ProcessNewTracks run reading the folders/key.
func (s *Service) applyConfig(dirs []string, key, source string, updatedAt *time.Time) {
	cleaned := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if d := strings.TrimSpace(dir); d != "" {
			cleaned = append(cleaned, d)
		}
	}
	s.cfgMu.Lock()
	s.dirs = cleaned
	s.acoustidKey = strings.TrimSpace(key)
	s.cfgSource = source
	s.cfgUpdatedAt = updatedAt
	s.cfgMu.Unlock()
}

func (s *Service) effectiveDirs() []string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return append([]string(nil), s.dirs...)
}

func (s *Service) effectiveKey() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.acoustidKey
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

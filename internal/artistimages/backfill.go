package artistimages

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const (
	BackfillStatusPending   = "pending"
	BackfillStatusRunning   = "running"
	BackfillStatusCompleted = "completed"
	BackfillStatusCancelled = "cancelled"
	BackfillStatusFailed    = "failed"

	BackfillModeMissing = "missing"
	BackfillModeAll     = "all"
)

type BackfillJob struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Mode       string     `json:"mode"`
	Total      int        `json:"total"`
	Processed  int        `json:"processed"`
	Found      int        `json:"found"`
	Failed     int        `json:"failed"`
	Skipped    int        `json:"skipped"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type BackfillResult struct {
	Job BackfillJob `json:"job"`
}

func (s *Service) SetBackgroundContext(ctx context.Context) {
	if s == nil {
		return
	}
	s.bgCtx = ctx
}

func (s *Service) FetchArtistsByIDs(ctx context.Context, artistIDs []string) {
	if s == nil || !s.Enabled() || len(artistIDs) == 0 {
		return
	}
	ids := append([]string(nil), artistIDs...)
	bgCtx := s.bgCtx
	if bgCtx == nil {
		bgCtx = context.Background()
	}
	go func() {
		for _, artistID := range ids {
			artistID = strings.TrimSpace(artistID)
			if artistID == "" {
				continue
			}
			artist, err := loadMusicArtistByID(bgCtx, s.db, artistID)
			if err != nil {
				continue
			}
			s.backfillArtist(bgCtx, artist)
		}
	}()
}

func loadMusicArtistByID(ctx context.Context, db *sql.DB, artistID string) (catalog.MusicArtist, error) {
	var item catalog.MusicArtist
	var imagesJSON, externalJSON string
	err := db.QueryRowContext(ctx, `
		SELECT id, name, sort_name, images_json, external_ids_json
		FROM music_artists
		WHERE id = ?`, artistID).Scan(&item.ID, &item.Name, &item.SortName, &imagesJSON, &externalJSON)
	if err != nil {
		return catalog.MusicArtist{}, err
	}
	decodeJSONField(imagesJSON, &item.Images)
	decodeJSONField(externalJSON, &item.ExternalIDs)
	return item, nil
}

func (s *Service) StartBackfill(ctx context.Context, mode string) (BackfillResult, error) {
	if s == nil || !s.Enabled() {
		return BackfillResult{}, ErrBackfillNotAvailable
	}
	mode = normalizeBackfillMode(mode)

	s.backfillMu.Lock()
	if s.activeBackfill != nil && isBackfillActive(s.activeBackfill.job.Status) {
		job := s.activeBackfill.job
		s.backfillMu.Unlock()
		return BackfillResult{Job: job}, nil
	}
	s.backfillMu.Unlock()

	artists, err := listMusicArtistsForBackfill(ctx, s.db)
	if err != nil {
		return BackfillResult{}, err
	}

	targets := make([]catalog.MusicArtist, 0, len(artists))
	for _, artist := range artists {
		if backfillTarget(mode, artist) {
			targets = append(targets, artist)
		}
	}

	job := BackfillJob{
		ID:        newBackfillJobID(),
		Status:    BackfillStatusRunning,
		Mode:      mode,
		Total:     len(targets),
		StartedAt: time.Now().UTC(),
	}

	bgCtx := s.bgCtx
	if bgCtx == nil {
		bgCtx = context.Background()
	}
	runCtx, cancel := context.WithCancel(bgCtx)

	s.backfillMu.Lock()
	s.activeBackfill = &backfillRunner{
		job:    job,
		cancel: cancel,
	}
	s.backfillMu.Unlock()

	go s.executeBackfill(runCtx, targets)

	return BackfillResult{Job: job}, nil
}

func (s *Service) GetBackfillJob() (BackfillJob, bool) {
	if s == nil {
		return BackfillJob{}, false
	}
	s.backfillMu.Lock()
	defer s.backfillMu.Unlock()
	if s.activeBackfill != nil {
		return s.activeBackfill.job, true
	}
	if s.lastBackfill != nil {
		return *s.lastBackfill, true
	}
	return BackfillJob{}, false
}

func (s *Service) CancelBackfill() (BackfillJob, error) {
	if s == nil {
		return BackfillJob{}, ErrBackfillNotAvailable
	}
	s.backfillMu.Lock()
	runner := s.activeBackfill
	if runner == nil || !isBackfillActive(runner.job.Status) {
		s.backfillMu.Unlock()
		return BackfillJob{}, ErrBackfillNotRunning
	}
	cancel := runner.cancel
	s.backfillMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.waitForBackfillTerminal(context.Background(), 5*time.Second)
}

type backfillRunner struct {
	job    BackfillJob
	cancel context.CancelFunc
}

func (s *Service) executeBackfill(ctx context.Context, artists []catalog.MusicArtist) {
	var runErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("artist image backfill panic: %v", recovered)
		}
		s.finishBackfill(ctx, runErr)
	}()

	for _, artist := range artists {
		select {
		case <-ctx.Done():
			runErr = context.Canceled
			return
		default:
		}

		outcome := s.backfillArtist(ctx, artist)
		s.backfillMu.Lock()
		if s.activeBackfill == nil {
			s.backfillMu.Unlock()
			return
		}
		s.activeBackfill.job.Processed++
		switch outcome {
		case backfillFound:
			s.activeBackfill.job.Found++
		case backfillFailed:
			s.activeBackfill.job.Failed++
		case backfillSkipped:
			s.activeBackfill.job.Skipped++
		}
		s.backfillMu.Unlock()
	}
}

type backfillOutcome int

const (
	backfillFound backfillOutcome = iota
	backfillFailed
	backfillSkipped
)

func (s *Service) backfillArtist(ctx context.Context, artist catalog.MusicArtist) backfillOutcome {
	if hasLocalArtistImage(artist.Images) {
		return backfillSkipped
	}
	if cached, ok, err := s.loadCachedCover(ctx, artist.ID); err == nil && ok {
		_ = s.persistArtistImages(ctx, artist.ID, cached, "cache")
		s.patchCatalog(artist.ID, cached)
		return backfillFound
	}
	images, found := s.fetchAndPersist(ctx, artist)
	if found && len(images) > 0 {
		return backfillFound
	}
	return backfillFailed
}

func (s *Service) finishBackfill(ctx context.Context, runErr error) {
	s.backfillMu.Lock()
	defer s.backfillMu.Unlock()
	if s.activeBackfill == nil {
		return
	}
	job := s.activeBackfill.job
	now := time.Now().UTC()
	job.FinishedAt = &now
	switch {
	case errors.Is(runErr, context.Canceled):
		job.Status = BackfillStatusCancelled
		if job.Error == "" {
			job.Error = "cancelled"
		}
	case runErr != nil:
		job.Status = BackfillStatusFailed
		job.Error = runErr.Error()
	default:
		job.Status = BackfillStatusCompleted
	}
	s.lastBackfill = &job
	s.activeBackfill = nil
}

func (s *Service) waitForBackfillTerminal(ctx context.Context, timeout time.Duration) (BackfillJob, error) {
	deadline := time.Now().Add(timeout)
	for {
		s.backfillMu.Lock()
		runner := s.activeBackfill
		last := s.lastBackfill
		s.backfillMu.Unlock()
		if runner == nil {
			if last != nil {
				return *last, nil
			}
			return BackfillJob{}, ErrBackfillNotRunning
		}
		if !isBackfillActive(runner.job.Status) || time.Now().After(deadline) {
			return runner.job, nil
		}
		select {
		case <-ctx.Done():
			return BackfillJob{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func listMusicArtistsForBackfill(ctx context.Context, db *sql.DB) ([]catalog.MusicArtist, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, sort_name, images_json, external_ids_json
		FROM music_artists
		ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list music artists for backfill: %w", err)
	}
	defer rows.Close()

	var items []catalog.MusicArtist
	for rows.Next() {
		var item catalog.MusicArtist
		var imagesJSON, externalJSON string
		if err := rows.Scan(&item.ID, &item.Name, &item.SortName, &imagesJSON, &externalJSON); err != nil {
			return nil, fmt.Errorf("scan music artist for backfill: %w", err)
		}
		decodeJSONField(imagesJSON, &item.Images)
		decodeJSONField(externalJSON, &item.ExternalIDs)
		items = append(items, item)
	}
	return items, rows.Err()
}

func decodeJSONField(raw string, dst any) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), dst)
}

func backfillTarget(mode string, artist catalog.MusicArtist) bool {
	switch normalizeBackfillMode(mode) {
	case BackfillModeAll:
		return true
	default:
		return !hasLocalArtistImage(artist.Images)
	}
}

func normalizeBackfillMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case BackfillModeAll:
		return BackfillModeAll
	default:
		return BackfillModeMissing
	}
}

func isBackfillActive(status string) bool {
	switch status {
	case BackfillStatusPending, BackfillStatusRunning:
		return true
	default:
		return false
	}
}

func newBackfillJobID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "artistimg_" + hex.EncodeToString(buf[:])
}

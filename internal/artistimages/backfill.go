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
	"github.com/bouliehaan/samo-server/internal/events"
	"github.com/bouliehaan/samo-server/internal/safego"
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

	safego.Go("artist image backfill", func() { s.executeBackfill(runCtx, targets) })

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
	job         BackfillJob
	cancel      context.CancelFunc
	lastPublish time.Time
}

// backfillPublishInterval bounds how often progress reaches subscribers.
const backfillPublishInterval = 750 * time.Millisecond

// SetEventHub attaches the live-update fan-out. Optional: a nil hub is a
// working no-op, so a backfill runs identically with no dashboard connected.
func (s *Service) SetEventHub(hub *events.Hub) {
	s.events = hub
}

// publishBackfill broadcasts a job snapshot. Best effort — a backfill must
// never fail because nobody was listening.
func (s *Service) publishBackfill(job BackfillJob) {
	if s == nil || s.events == nil {
		return
	}
	s.events.Publish(events.Event{Type: events.TypeArtistImages, Data: backfillJobPayload{Job: &job}})
}

// backfillJobPayload matches the shape GET /music/artists/images/backfill
// returns, so the dashboard applies an event and a fetch through one path.
type backfillJobPayload struct {
	Job *BackfillJob `json:"job"`
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
		snapshot := s.activeBackfill.job
		due := time.Since(s.activeBackfill.lastPublish) >= backfillPublishInterval
		if due {
			s.activeBackfill.lastPublish = time.Now()
		}
		s.backfillMu.Unlock()

		// Throttled: a cached-cover run can chew through hundreds of artists a
		// second, and one event each would be far chattier than the 2s poll
		// this replaced. The terminal snapshot in finishBackfill is never
		// throttled, so the UI always lands on the true final state.
		if due {
			s.publishBackfill(snapshot)
		}
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
	if s.activeBackfill == nil {
		s.backfillMu.Unlock()
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
	s.backfillMu.Unlock()

	// Unthrottled and outside the lock: this is the snapshot that flips the UI
	// out of "running", so it must never be the one that gets dropped.
	s.publishBackfill(job)
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
	// Explo-only artists are excluded: the backfill would spend external API
	// quota making silo'd artists more prominent. An artist counts as explo
	// when they have attributable tracks (track or album-artist credit) and
	// every one of them is explo — the SQL twin of the catalog projection's
	// deriveExploArtists.
	rows, err := db.QueryContext(ctx, `
		SELECT ma.id, ma.name, ma.sort_name, ma.images_json, ma.external_ids_json
		FROM music_artists ma
		WHERE NOT (
		  EXISTS (
		    SELECT 1 FROM music_track_artists mta
		    JOIN music_tracks mt ON mt.id = mta.track_id
		    WHERE mta.artist_id = ma.id
		    UNION
		    SELECT 1 FROM music_album_artists maa
		    JOIN music_tracks mt ON mt.album_id = maa.album_id
		    WHERE maa.artist_id = ma.id
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM music_track_artists mta
		    JOIN music_tracks mt ON mt.id = mta.track_id
		    WHERE mta.artist_id = ma.id AND mt.is_explo = 0
		    UNION
		    SELECT 1 FROM music_album_artists maa
		    JOIN music_tracks mt ON mt.album_id = maa.album_id
		    WHERE maa.artist_id = ma.id AND mt.is_explo = 0
		  )
		)
		ORDER BY ma.name COLLATE NOCASE`)
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

// Package explo automatically identifies and organizes tracks dropped by an
// external "explo" style weekly playlist exporter into a configured folder.
// Those files typically arrive with no usable tags, so left alone they show
// up as "Unknown Artist" entries flooding the Recently Added shelves. This
// package fingerprints them (chromaprint/fpcalc), identifies them against
// AcoustID/MusicBrainz, stores the result as a normal metadata override
// (never touching the file - the exporter owns that folder), routes them
// into a system-managed playlist, and hides their album(s) from Recently
// Added. It never moves, renames, or rewrites tags on the underlying files.
package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
)

// exploWriteAttempts is how many times an explo DB write retries on transient
// Postgres contention (deadlock victim 40P01 / serialization failure 40001)
// before giving up. explo is a slow background batch (throttled AcoustID +
// MusicBrainz lookups), so it can afford to be patient when a concurrent scan
// touches the same rows. Same storage.Retry mechanism the scanner uses.
const exploWriteAttempts = 20

// acoustidMinInterval keeps calls under AcoustID's published "no more than 3
// requests per second" guideline.
const acoustidMinInterval = 350 * time.Millisecond

// DefaultPlaylistName is the system-managed playlist explo tracks are routed
// into on both clients. Renamed from "Explo" (2026-07-09, Jacob's ask);
// reconcileExploPlaylist adopts by the system flag, not the name, so servers
// that created the playlist under the old default get renamed in place.
const DefaultPlaylistName = "Explore"

type ServiceOptions struct {
	DB             *sql.DB
	Dirs           []string // absolute folder paths to treat as explo drops
	AcoustIDAPIKey string
	FpcalcPath     string
	HTTPClient     *http.Client
	MetadataApply  *metadata.MetadataApplyService
	// Metadata drives the fallback identification path (filename + duration
	// -gated text search) for tracks AcoustID can't identify. Optional: when
	// nil, unmatched-by-AcoustID tracks just stay unmatched, same as before
	// this fallback existed.
	Metadata  *metadata.Service
	Playlists *playlists.Service
	// Covers is the local cover store the cover engine verifies every
	// download into (and stores generated placeholder tiles in). Without it
	// the cover backfill does not run.
	Covers CoverStore
	// ReloadCatalog refreshes the live in-memory catalog projection after a
	// batch of overrides/playlist changes lands. Same callback main.go wires
	// into the HTTP handlers after a manual metadata apply.
	ReloadCatalog func(context.Context) error
	PlaylistName  string
	Logger        func(string, ...any)
}

type Service struct {
	db            *sql.DB
	fpcalcPath    string
	httpClient    *http.Client
	metadataApply *metadata.MetadataApplyService
	metadata      *metadata.Service
	playlists     *playlists.Service
	reloadCatalog func(context.Context) error
	playlistName  string
	logger        func(string, ...any)

	// envDirs / envKey are the immutable environment-provided values, used as
	// the fallback when no explo_config row overrides them.
	envDirs []string
	envKey  string

	// cfgMu guards the effective (currently-live) folder set + AcoustID key,
	// which LoadConfig/SaveConfig swap in at runtime and ProcessNewTracks reads.
	cfgMu        sync.RWMutex
	dirs         []string
	acoustidKey  string
	cfgSource    string
	cfgUpdatedAt *time.Time

	covers CoverStore

	rateMu       sync.Mutex
	lastAcoustID time.Time
	mbPacer      requestPacer
	itunesPacer  requestPacer
	deezerPacer  requestPacer

	// processMu serializes ProcessNewTracks runs. OnScanComplete can fire in
	// quick succession (file-watcher debounce during a large drop), and
	// without this a second run could pick the same not-yet-recorded tracks
	// and waste rate-limited AcoustID calls identifying them twice.
	processMu sync.Mutex

	// backfillMu serializes cover-backfill runs. Kept separate from processMu
	// so a slow, network-bound backfill doesn't block scan-triggered processing.
	backfillMu sync.Mutex

	// idleMu/lastIdleStatus deduplicate the "nothing due this pass" log line.
	// With the periodic ticker driving passes every 30 minutes, an unchanged
	// idle status would otherwise print ~48 identical lines a day; it still
	// logs whenever the summary CHANGES (and on the first pass after boot),
	// which is when it carries information.
	idleMu         sync.Mutex
	lastIdleStatus string
}

func NewService(options ServiceOptions) *Service {
	logger := options.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	playlistName := strings.TrimSpace(options.PlaylistName)
	if playlistName == "" {
		playlistName = DefaultPlaylistName
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	dirs := make([]string, 0, len(options.Dirs))
	for _, dir := range options.Dirs {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	key := strings.TrimSpace(options.AcoustIDAPIKey)
	return &Service{
		db:            options.DB,
		fpcalcPath:    strings.TrimSpace(options.FpcalcPath),
		httpClient:    httpClient,
		metadataApply: options.MetadataApply,
		metadata:      options.Metadata,
		playlists:     options.Playlists,
		covers:        options.Covers,
		reloadCatalog: options.ReloadCatalog,
		playlistName:  playlistName,
		logger:        logger,
		// Effective config starts at the environment values; LoadConfig may
		// later overlay a persisted UI override.
		envDirs:     dirs,
		envKey:      key,
		dirs:        dirs,
		acoustidKey: key,
		cfgSource:   "environment",
	}
}

// Enabled reports whether the explo pipeline has everything it needs to run.
// Any missing piece (no folder configured, no AcoustID key, fpcalc not
// resolved) disables the feature entirely rather than half-running it.
func (s *Service) Enabled() bool {
	if s == nil || s.db == nil || s.metadataApply == nil || s.playlists == nil ||
		strings.TrimSpace(s.fpcalcPath) == "" {
		return false
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return len(s.dirs) > 0 && s.acoustidKey != ""
}

// Result summarizes one ProcessNewTracks run.
type Result struct {
	Scanned   int
	Matched   int
	Unmatched int
	Errored   int
	// Hidden is how many albums this run newly pulled out of Recently Added.
	Hidden int64
}

// ProcessNewTracks looks for tracks under the configured explo folder(s)
// that haven't been through the pipeline yet, fingerprints + identifies
// each, applies any match as a metadata override, adds all of them
// (matched or not - the point is corralling every drop, identification is a
// bonus) to the system Explo playlist, and hides any album that is now
// entirely explo-sourced from Recently Added. Safe to call repeatedly; each
// track is only ever processed once (tracked in the explo_tracks table).
func (s *Service) ProcessNewTracks(ctx context.Context) (Result, error) {
	result := Result{}
	if !s.Enabled() {
		return result, nil
	}
	s.processMu.Lock()
	defer s.processMu.Unlock()

	candidates, err := s.findCandidateTracks(ctx)
	if err != nil {
		return result, fmt.Errorf("find explo candidates: %w", err)
	}
	result.Scanned = len(candidates)
	if len(candidates) > 0 {
		// Announce up front: a batch takes minutes (fpcalc + throttled
		// lookups per track) and the completion line alone reads as a
		// never-started pass to anyone tailing the journal.
		s.logger("explo: identifying %d track(s)", len(candidates))

		// Corral the drop BEFORE the slow identify loop. The loop below can
		// run for many minutes on a weekly drop; without this the fresh
		// albums sit in Recently Added (and every listing surface) as
		// untagged, artless entries for that whole window — the exact flood
		// this feature exists to prevent. Path-derived, so it needs no
		// identification to be correct.
		dirs := s.effectiveDirs()
		flagged, _, flagErr := s.reconcileExploTracks(ctx, dirs)
		if flagErr != nil {
			s.logger("explo: %v", flagErr)
		}
		hiddenEarly, _, hideErr := s.reconcileHiddenAlbums(ctx, dirs)
		if hideErr != nil {
			s.logger("explo: %v", hideErr)
		}
		result.Hidden += hiddenEarly
		if (flagged > 0 || hiddenEarly > 0) && s.reloadCatalog != nil {
			if err := s.reloadCatalog(ctx); err != nil {
				s.logger("explo: catalog reload failed: %v", err)
			}
		}
	}

	for _, candidate := range candidates {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		match, matched, procErr := s.identifyWithFallback(ctx, candidate)
		switch {
		case procErr != nil:
			result.Errored++
			s.logger("explo: identify failed for %q: %v", candidate.path, procErr)
			if err := s.recordProcessed(ctx, candidate.trackID, "error", identifiedTrack{}, procErr.Error()); err != nil {
				s.logger("explo: record processed failed for %s: %v", candidate.trackID, err)
			}
			continue
		case !matched:
			result.Unmatched++
			if err := s.recordProcessed(ctx, candidate.trackID, "unmatched", identifiedTrack{}, ""); err != nil {
				s.logger("explo: record processed failed for %s: %v", candidate.trackID, err)
			}
			continue
		}

		result.Matched++
		if err := s.applyMatch(ctx, candidate.trackID, candidate.albumID, match); err != nil {
			s.logger("explo: apply metadata failed for %s: %v", candidate.trackID, err)
		}
		status := "matched"
		if match.Source != "acoustid" {
			status = "matched-fallback"
		}
		if err := s.recordProcessed(ctx, candidate.trackID, status, match, ""); err != nil {
			s.logger("explo: record processed failed for %s: %v", candidate.trackID, err)
		}
		// A freshly identified drop may still carry a placeholder from an
		// earlier unmatched cover pass; re-open its cover state so the next
		// BackfillCovers resolves REAL art instead of leaving the placeholder in
		// place (findCoverTargets would otherwise see it as already-attempted).
		if err := s.resetTrackCoverState(ctx, candidate.trackID); err != nil {
			s.logger("explo: reset cover state failed for %s: %v", candidate.trackID, err)
		}
	}

	// Re-derive which albums belong out of Recently Added, the ledger, and the
	// playlist (existence + membership) from the folder that's *currently*
	// configured. Fully self-correcting: narrowing (or clearing) the explo
	// folder un-hides albums that are no longer under it and drops their
	// tracks back out of the Explo playlist, while fresh ledger rows from the
	// loop above become playlist members. Runs every pass, even when nothing
	// new was found.
	hidden, unhidden, otherChanged, err := s.syncExploState(ctx, s.effectiveDirs())
	if err != nil {
		s.logger("explo: reconcile failed: %v", err)
	}
	result.Hidden += hidden

	if (result.Scanned > 0 || hidden > 0 || unhidden > 0 || otherChanged) && s.reloadCatalog != nil {
		if err := s.reloadCatalog(ctx); err != nil {
			s.logger("explo: catalog reload failed: %v", err)
		}
	}

	if result.Scanned > 0 || hidden > 0 || unhidden > 0 {
		s.logger("explo: processed %d track(s) (%d matched, %d unmatched, %d errored); hid %d, un-hid %d album(s) in Recently Added",
			result.Scanned, result.Matched, result.Unmatched, result.Errored, hidden, unhidden)
	} else {
		s.logIdlePassStatus(ctx)
	}
	return result, nil
}

// logIdlePassStatus explains an identify pass that did no work. An idle pass
// is indistinguishable from one that never ran unless it says why nothing
// happened — which, on a freshly enabled pipeline, is usually one of two
// things the operator can only fix if they're TOLD:
//
//   - the configured folder matches zero library tracks (wrong path, or a
//     path outside any scanned library) — the single most common reason
//     "the backfill never starts", and previously 100% silent, and
//   - every folder track is already identified, or mid-backoff after a failed
//     identification (AcoustID has no fingerprint for a fresh release yet).
//
// Always emits exactly one line when the feature is enabled, so "explo:
// folder feature enabled" is never again followed by nothing. Runs after
// syncExploState, so the ledger only holds current-folder rows.
func (s *Service) logIdlePassStatus(ctx context.Context) {
	dirs := s.effectiveDirs()
	clause, args := exploPathClause(dirs)
	query := fmt.Sprintf(`
		SELECT
		  COUNT(*),
		  COALESCE(SUM(CASE WHEN et.status IN ('matched', 'matched-fallback') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts <  %[1]d THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts >= %[1]d THEN 1 ELSE 0 END), 0),
		  COALESCE(MIN(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts < %[1]d THEN %[2]s END), '')
		FROM music_tracks mt
		JOIN media_files mf ON mf.track_id = mt.id
		LEFT JOIN explo_tracks et ON et.track_id = mt.id
		WHERE %[3]s`,
		exploMaxIdentifyAttempts, exploNextDueTimeExpr("et.processed_at"), clause)
	var inFolder, identified, waiting, retired int
	var nextDue string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&inFolder, &identified, &waiting, &retired, &nextDue); err != nil {
		s.logger("explo: status query failed: %v", err)
		return
	}

	folder := "(none)"
	if len(dirs) > 0 {
		folder = dirs[0]
	}
	var line string
	if inFolder == 0 {
		line = fmt.Sprintf("explo: folder %q matches 0 tracks in the library — check the path is correct and inside a scanned library", folder)
	} else {
		parts := []string{fmt.Sprintf("%d identified", identified)}
		if waiting > 0 {
			parts = append(parts, fmt.Sprintf("%d awaiting retry (next due %s UTC)", waiting, nextDue))
		}
		if retired > 0 {
			parts = append(parts, fmt.Sprintf("%d retired after %d failed attempts", retired, exploMaxIdentifyAttempts))
		}
		line = fmt.Sprintf("explo: nothing due this pass — %d track(s) under %q: %s", inFolder, folder, strings.Join(parts, ", "))
	}

	s.idleMu.Lock()
	repeat := line == s.lastIdleStatus
	s.lastIdleStatus = line
	s.idleMu.Unlock()
	if !repeat {
		s.logger("%s", line)
	}
}

// exploPathClause builds a SQL predicate matching media_files under any of the
// configured explo folders, plus its bound args. With no folders it returns a
// constant-false predicate ("0") so callers that AND on it hide/keep nothing
// and callers that negate it (un-hide, prune) act on everything - i.e. "explo
// off" means "nothing is explo," which is exactly what we want for recovery.
func exploPathClause(dirs []string) (string, []any) {
	if len(dirs) == 0 {
		// A boolean literal, because the predicate lands in WHERE/AND
		// positions where Postgres requires a boolean expression.
		return "FALSE", nil
	}
	clauses := make([]string, 0, len(dirs))
	args := make([]any, 0, len(dirs))
	for _, dir := range dirs {
		clauses = append(clauses, `mf.path ILIKE ? ESCAPE '\'`)
		args = append(args, likePrefix(dir)+"%")
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args
}

// reconcileHiddenAlbums makes music_albums.hidden_from_recently_added match the
// CURRENTLY configured explo folder(s), in BOTH directions:
//   - hide an album whose every file-backed track lives under an explo folder
//     (and has at least one), and
//   - un-hide any album that is hidden but is no longer fully under an explo
//     folder - crucially, this is what makes a too-broad folder recoverable:
//     point the folder at the real drop subfolder (or clear it) and every album
//     that isn't actually under it comes back into Recently Added.
//
// Hiding is derived from the file PATH, not from the explo_tracks ledger, so it
// is immediate (a fresh drop is hidden on the next scan, before AcoustID even
// runs) and self-correcting (it can't get wedged by stale ledger rows). It only
// touches rows whose flag actually needs to flip, and bumps updated_at on those
// so the Android mirror (which delta-syncs on updated_at) re-pulls them.
// Returns how many albums it newly hid and newly un-hid.
func (s *Service) reconcileHiddenAlbums(ctx context.Context, dirs []string) (hidden, unhidden int64, err error) {
	match, args := exploPathClause(dirs)

	hideSQL := fmt.Sprintf(`
		UPDATE music_albums
		SET hidden_from_recently_added = 1, updated_at = CURRENT_TIMESTAMP
		WHERE hidden_from_recently_added = 0
		  AND EXISTS (
		    SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
		    WHERE mt.album_id = music_albums.id AND %s)
		  AND NOT EXISTS (
		    SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
		    WHERE mt.album_id = music_albums.id AND NOT %s)`, match, match)
	var res sql.Result
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		var retryErr error
		res, retryErr = s.db.ExecContext(ctx, hideSQL, append(append([]any{}, args...), args...)...)
		return retryErr
	}); err != nil {
		return 0, 0, fmt.Errorf("explo hide albums: %w", err)
	}
	hidden, _ = res.RowsAffected()

	unhideSQL := fmt.Sprintf(`
		UPDATE music_albums
		SET hidden_from_recently_added = 0, updated_at = CURRENT_TIMESTAMP
		WHERE hidden_from_recently_added = 1
		  AND (
		    NOT EXISTS (
		      SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
		      WHERE mt.album_id = music_albums.id AND %s)
		    OR EXISTS (
		      SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
		      WHERE mt.album_id = music_albums.id AND NOT %s))`, match, match)
	var res2 sql.Result
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		var retryErr error
		res2, retryErr = s.db.ExecContext(ctx, unhideSQL, append(append([]any{}, args...), args...)...)
		return retryErr
	}); err != nil {
		return hidden, 0, fmt.Errorf("explo unhide albums: %w", err)
	}
	unhidden, _ = res2.RowsAffected()
	return hidden, unhidden, nil
}

// reconcileExploTracks makes music_tracks.is_explo match the CURRENTLY
// configured explo folder(s), in both directions, exactly like the album
// flag: a track is explo iff its media file lives under an explo folder.
// This is the per-track silo marker the catalog projection reads — album
// hiding keeps Recently Added clean, but only a track-level fact lets the
// list/browse/search surfaces exclude explo content without path joins.
// Bumps updated_at on flipped rows so delta-syncing clients re-pull them.
// Returns how many rows it newly flagged and un-flagged.
func (s *Service) reconcileExploTracks(ctx context.Context, dirs []string) (flagged, unflagged int64, err error) {
	match, args := exploPathClause(dirs)

	flagSQL := fmt.Sprintf(`
		UPDATE music_tracks
		SET is_explo = 1, updated_at = CURRENT_TIMESTAMP
		WHERE is_explo = 0
		  AND EXISTS (
		    SELECT 1 FROM media_files mf
		    WHERE mf.track_id = music_tracks.id AND %s)`, match)
	var res sql.Result
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		var retryErr error
		res, retryErr = s.db.ExecContext(ctx, flagSQL, args...)
		return retryErr
	}); err != nil {
		return 0, 0, fmt.Errorf("explo flag tracks: %w", err)
	}
	flagged, _ = res.RowsAffected()

	unflagSQL := fmt.Sprintf(`
		UPDATE music_tracks
		SET is_explo = 0, updated_at = CURRENT_TIMESTAMP
		WHERE is_explo = 1
		  AND NOT EXISTS (
		    SELECT 1 FROM media_files mf
		    WHERE mf.track_id = music_tracks.id AND %s)`, match)
	var res2 sql.Result
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		var retryErr error
		res2, retryErr = s.db.ExecContext(ctx, unflagSQL, args...)
		return retryErr
	}); err != nil {
		return flagged, 0, fmt.Errorf("explo unflag tracks: %w", err)
	}
	unflagged, _ = res2.RowsAffected()
	return flagged, unflagged, nil
}

// pruneVanishedFiles deletes tracks whose file has disappeared from an explo
// folder. The explo exporter ROTATES its weekly drop — last week's files are
// deleted from the folder, not moved — so a media_files row under the folder
// whose path no longer exists on disk is genuinely gone. Left alone it lingers
// as a ghost: fpcalc errors on it every pass ("No such file"), it never leaves
// the ledger, and it pads the Explore playlist with an untitled entry.
//
// Deleting the music_tracks row cascades (ON DELETE CASCADE) to media_files AND
// explo_tracks, so the ledger, playlist, and library views all self-correct on
// the same pass without a separate library rescan. Safe by construction: it is
// scoped to the configured explo folder(s) and only ever deletes a row whose
// file os.Stat has just confirmed is ErrNotExist — a present file, or any
// ambiguous stat error (permission, I/O, mount hiccup), is left untouched, so it
// can never delete a real or merely-unreachable track. Returns how many it pruned.
func (s *Service) pruneVanishedFiles(ctx context.Context, dirs []string) (int, error) {
	if len(dirs) == 0 {
		return 0, nil
	}
	clause, args := exploPathClause(dirs)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT mt.id, mf.path
		FROM music_tracks mt
		JOIN media_files mf ON mf.track_id = mt.id
		WHERE %s`, clause), args...)
	if err != nil {
		return 0, fmt.Errorf("explo prune-vanished query: %w", err)
	}
	type candidate struct{ trackID, path string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.trackID, &c.path); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	pruned := 0
	for _, c := range candidates {
		select {
		case <-ctx.Done():
			return pruned, ctx.Err()
		default:
		}
		if _, statErr := os.Stat(c.path); !errors.Is(statErr, os.ErrNotExist) {
			// File present, or an ambiguous error — never prune on doubt.
			continue
		}
		if err := storage.Retry(ctx, exploWriteAttempts, func() error {
			_, retryErr := s.db.ExecContext(ctx, `DELETE FROM music_tracks WHERE id = ?`, c.trackID)
			return retryErr
		}); err != nil {
			s.logger("explo: prune vanished track %s failed: %v", c.trackID, err)
			continue
		}
		pruned++
	}
	if pruned > 0 {
		s.logger("explo: pruned %d file(s) rotated out of the drop folder", pruned)
	}
	return pruned, nil
}

// pruneExploLedger drops explo_tracks rows for any track no longer under a
// configured explo folder, so a too-broad folder that swept in real tracks can
// be undone by narrowing (or clearing) it. With no folders it clears the whole
// ledger.
func (s *Service) pruneExploLedger(ctx context.Context, dirs []string) error {
	match, args := exploPathClause(dirs)
	query := fmt.Sprintf(`
		DELETE FROM explo_tracks
		WHERE track_id NOT IN (
		  SELECT mt.id FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
		  WHERE %s)`, match)
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, execErr := s.db.ExecContext(ctx, query, args...)
		return execErr
	}); err != nil {
		return fmt.Errorf("explo prune ledger: %w", err)
	}
	return nil
}

// reconcileExploPlaylist makes the system Explo playlist match the ledger:
// every explo_tracks row that still resolves to a catalog track is a member
// (matched or not - the queue corrals every drop), anything else is dropped,
// and the playlist is (re)created when it is missing but should have members.
// Membership is re-DERIVED from the ledger instead of incrementally patched,
// the same principle as the path-based hidden flags, so a lost, emptied, or
// damaged playlist heals on the next pass. It also repairs ownership: a
// playlist created under the internal bootstrap admin (the old
// FirstAdminOwnerID zero-created_at bug) is invisible to every real user, so
// it gets re-owned to a human admin. Returns whether anything was written.
func (s *Service) reconcileExploPlaylist(ctx context.Context) (bool, error) {
	// The ledger is already pruned to the configured folders, and the join
	// drops rows whose track has left the catalog, so `want` is exactly the
	// membership the playlist should hold, in arrival order.
	rows, err := s.db.QueryContext(ctx, `
		SELECT et.track_id FROM explo_tracks et
		JOIN music_tracks mt ON mt.id = et.track_id
		ORDER BY et.processed_at, et.track_id`)
	if err != nil {
		return false, fmt.Errorf("explo playlist ledger query: %w", err)
	}
	var want []string
	wantSet := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		want = append(want, id)
		wantSet[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	ownerID, err := playlists.FirstAdminOwnerID(ctx, s.db)
	if err != nil {
		return false, fmt.Errorf("resolve playlist owner: %w", err)
	}

	// Recognize the playlist by the system flag alone, regardless of owner OR
	// name, so a row created under the wrong owner or an older default name
	// ("Explo" before 2026-07-09) is adopted and repaired rather than
	// duplicated. Prefer an exact name match if several system rows ever
	// coexist; otherwise the oldest wins.
	var playlistID, currentOwner, currentName, trackIDsJSON string
	err = s.db.QueryRowContext(ctx, `
		SELECT id, owner_id, name, track_ids_json FROM music_playlists
		WHERE system = 1
		ORDER BY (name = ?) DESC, created_at LIMIT 1`,
		s.playlistName).Scan(&playlistID, &currentOwner, &currentName, &trackIDsJSON)
	switch {
	case err == sql.ErrNoRows:
		if len(want) == 0 {
			return false, nil
		}
		if _, createErr := s.playlists.Create(ctx, ownerID, playlists.CreateInput{
			Name:     s.playlistName,
			Public:   false,
			TrackIDs: want,
			System:   true,
		}); createErr != nil {
			return false, fmt.Errorf("create explo playlist: %w", createErr)
		}
		return true, nil
	case err != nil:
		return false, fmt.Errorf("load explo playlist: %w", err)
	}

	changed := false
	// Ownership repair: only ever adopts away from the internal bootstrap
	// account - a playlist a human admin owns is left alone.
	if currentOwner == users.BootstrapUserID && ownerID != users.BootstrapUserID {
		if err := storage.Retry(ctx, exploWriteAttempts, func() error {
			_, retryErr := s.db.ExecContext(ctx, `
				UPDATE music_playlists SET owner_id = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`, ownerID, playlistID)
			return retryErr
		}); err != nil {
			return false, fmt.Errorf("re-own explo playlist: %w", err)
		}
		changed = true
	}

	// Name repair: a row created under an older configured/default name is
	// renamed in place (adoption above is name-agnostic), keeping its id,
	// members, and client references stable.
	if currentName != s.playlistName {
		if err := storage.Retry(ctx, exploWriteAttempts, func() error {
			_, retryErr := s.db.ExecContext(ctx, `
				UPDATE music_playlists SET name = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`, s.playlistName, playlistID)
			return retryErr
		}); err != nil {
			return false, fmt.Errorf("rename explo playlist: %w", err)
		}
		changed = true
	}

	var current []string
	_ = json.Unmarshal([]byte(trackIDsJSON), &current)
	// Keep the existing order for tracks that stay, append newcomers in ledger
	// order, drop everything the ledger no longer vouches for.
	next := make([]string, 0, len(want))
	seen := map[string]struct{}{}
	for _, id := range current {
		if _, ok := wantSet[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		next = append(next, id)
	}
	for _, id := range want {
		if _, dup := seen[id]; !dup {
			next = append(next, id)
		}
	}
	if slices.Equal(next, current) {
		return changed, nil
	}
	// Membership goes through the system-only write path: the public
	// Update/Delete refuse system playlists (a client edit of a re-derived
	// queue would only be silently reverted here on the next pass), so the
	// reconciler cannot use them - and must not, since it acts as the server,
	// not as any owner.
	if _, err := s.playlists.SetSystemTracks(ctx, playlistID, next); err != nil {
		return changed, fmt.Errorf("update explo playlist: %w", err)
	}
	return true, nil
}

// syncExploState reconciles all persisted explo side-effects (track flags,
// hidden album flags, the ledger, and the playlist) to the given folder set.
// Callers pass the currently-effective dirs; an empty set fully un-does
// everything. Returns how many albums were newly hidden/un-hidden and
// whether anything else (track flags, playlist) changed.
func (s *Service) syncExploState(ctx context.Context, dirs []string) (hidden, unhidden int64, otherChanged bool, err error) {
	if err := s.pruneExploLedger(ctx, dirs); err != nil {
		s.logger("explo: %v", err)
	}
	flagged, unflagged, tracksErr := s.reconcileExploTracks(ctx, dirs)
	if tracksErr != nil {
		s.logger("explo: %v", tracksErr)
	}
	playlistChanged, playlistErr := s.reconcileExploPlaylist(ctx)
	if playlistErr != nil {
		s.logger("explo: %v", playlistErr)
	}
	hidden, unhidden, err = s.reconcileHiddenAlbums(ctx, dirs)
	return hidden, unhidden, playlistChanged || flagged > 0 || unflagged > 0, err
}

// PruneRotatedOutFiles removes ghost tracks left behind when the explo exporter
// rotates its weekly drop (deletes old files), then reconciles the derived
// state. Kept OUT of syncExploState/ProcessNewTracks on purpose: it hits the
// real filesystem (os.Stat), whereas those run against synthetic fixtures in
// tests. Wired into the boot + post-scan goroutines in main.go next to
// ProcessNewTracks. No-op (and reload-free) when nothing was rotated out.
func (s *Service) PruneRotatedOutFiles(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	s.processMu.Lock()
	defer s.processMu.Unlock()
	dirs := s.effectiveDirs()
	pruned, err := s.pruneVanishedFiles(ctx, dirs)
	if err != nil {
		return pruned, err
	}
	if pruned == 0 {
		return 0, nil
	}
	// The cascade already dropped the ledger/media_files rows; re-derive the
	// playlist + hidden flags so the Explore queue and Recently Added drop them.
	if _, _, _, err := s.syncExploState(ctx, dirs); err != nil {
		s.logger("explo: reconcile after prune failed: %v", err)
	}
	if s.reloadCatalog != nil {
		if err := s.reloadCatalog(ctx); err != nil {
			s.logger("explo: catalog reload after prune failed: %v", err)
		}
	}
	return pruned, nil
}

// ReconcileRecentlyAdded re-syncs explo's persisted state to the current folder
// config on demand - at startup and right after the config changes - without
// waiting for a fresh drop. Deliberately NOT gated on Enabled(): when the admin
// clears/narrows the folder (or the key/fpcalc go missing) this is exactly when
// the un-hide + prune must run to recover Recently Added. Reloads the catalog
// only if something actually changed.
func (s *Service) ReconcileRecentlyAdded(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.processMu.Lock()
	defer s.processMu.Unlock()
	hidden, unhidden, otherChanged, err := s.syncExploState(ctx, s.effectiveDirs())
	if err != nil {
		return err
	}
	if (hidden > 0 || unhidden > 0 || otherChanged) && s.reloadCatalog != nil {
		s.logger("explo: reconcile hid %d, un-hid %d album(s) in Recently Added", hidden, unhidden)
		return s.reloadCatalog(ctx)
	}
	return nil
}

func (s *Service) identify(ctx context.Context, path string) (identifiedTrack, bool, error) {
	fp, err := fingerprintFile(ctx, s.fpcalcPath, path)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	s.throttleAcoustID(ctx)
	return lookupAcoustID(ctx, s.httpClient, s.effectiveKey(), fp)
}

// identifyWithFallback tries AcoustID first (the primary, highest-confidence
// method - "what does this audio sound like"), and only when that can't
// identify the file (no match OR the lookup itself errored, e.g. fpcalc
// choked on an unusual codec or the API had a hiccup) falls back to a
// filename+duration-gated text search. This is a "do our best" path, not a
// substitute for AcoustID: a wrong-song mistake is a much worse outcome than
// staying unmatched, so the fallback only ever accepts a candidate whose
// reported duration actually matches the file (see identifyByTextSearch).
//
// A recovered fallback match returns matched=true, err=nil - it fully
// replaces the original AcoustID failure. If AcoustID errored AND the
// fallback also found nothing, the original AcoustID error is what's
// returned (so a real infrastructure problem, like a revoked API key,
// stays visible in the error field instead of silently downgrading to a
// plain "unmatched").
func (s *Service) identifyWithFallback(ctx context.Context, candidate candidateTrack) (identifiedTrack, bool, error) {
	match, matched, err := s.identify(ctx, candidate.path)
	if matched {
		return match, true, nil
	}

	fallback, fallbackMatched, fallbackErr := s.identifyByTextSearch(ctx, candidate.path, candidate.title, candidate.artist, candidate.durationSeconds)
	if fallbackErr != nil {
		s.logger("explo: fallback text search failed for %q: %v", candidate.path, fallbackErr)
	}
	if fallbackMatched {
		return fallback, true, nil
	}
	return identifiedTrack{}, false, err
}

// throttleAcoustID blocks until acoustidMinInterval has passed since the
// last call, so a large backfill batch can't hammer the free API.
func (s *Service) throttleAcoustID(ctx context.Context) {
	s.rateMu.Lock()
	wait := acoustidMinInterval - time.Since(s.lastAcoustID)
	if wait > 0 {
		s.rateMu.Unlock()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		s.rateMu.Lock()
	}
	s.lastAcoustID = time.Now()
	s.rateMu.Unlock()
}

func (s *Service) applyMatch(ctx context.Context, trackID, albumID string, match identifiedTrack) error {
	trackCandidate := metadata.SearchResult{
		Provider:  match.Source,
		MediaType: "recording",
		Title:     match.Title,
		ExternalIDs: catalog.ExternalIDs{
			MusicBrainzRecordingID: match.MusicBrainzRecordingID,
		},
	}
	if match.Artist != "" {
		trackCandidate.Authors = []catalog.ContributorRef{{Name: match.Artist}}
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
			TargetKind:         string(metadata.ApplyTargetMusicTrack),
			TargetID:           trackID,
			Candidate:          trackCandidate,
			Fields:             []string{"title", "displayArtist", "externalIds"},
			DeferCatalogReload: true,
		})
		return err
	}); err != nil {
		return fmt.Errorf("apply track metadata: %w", err)
	}

	if albumID == "" {
		return nil
	}
	if strings.TrimSpace(match.Album) == "" && strings.TrimSpace(match.Artist) == "" {
		// Nothing identifiable to write at the album level. (Covers are NOT
		// applied here at all anymore: the cover engine in covers.go owns
		// them, verifies every download, and retries on a ladder - attaching
		// an unverified CAA URL here is how albums used to end up serving a
		// dead external redirect as their "art".)
		return nil
	}
	albumCandidate := metadata.SearchResult{
		Provider:  match.Source,
		MediaType: "album",
		Title:     match.Album,
		// ID keeps the apply-layer validation satisfied when the match has an
		// artist but no release-group title; only the fields listed below are
		// ever applied.
		ID: albumID,
	}
	if match.Artist != "" {
		albumCandidate.Authors = []catalog.ContributorRef{{Name: match.Artist}}
	}
	fields := []string{"displayArtist"}
	if strings.TrimSpace(match.Album) != "" {
		fields = append(fields, "title")
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
			TargetKind:         string(metadata.ApplyTargetMusicAlbum),
			TargetID:           albumID,
			Candidate:          albumCandidate,
			Fields:             fields,
			DeferCatalogReload: true,
		})
		return err
	}); err != nil {
		return fmt.Errorf("apply album metadata: %w", err)
	}
	return nil
}

// coverArtArchiveURL builds the Cover Art Archive "front cover" URL for a
// MusicBrainz release-group MBID, or "" if there's no id. CAA 307-redirects
// this to the actual image, which the cover downloader follows.
func coverArtArchiveURL(releaseGroupMBID string) string {
	id := strings.TrimSpace(releaseGroupMBID)
	if id == "" {
		return ""
	}
	return "https://coverartarchive.org/release-group/" + id + "/front-500"
}

func (s *Service) recordProcessed(ctx context.Context, trackID, status string, match identifiedTrack, errText string) error {
	// Upsert: a retried track REPLACES its ledger row with the newest outcome
	// and bumps `attempts`, so findCandidateTracks' retry budget is
	// enforceable. The cover columns are deliberately untouched — the cover
	// engine (covers.go) owns them. The release group MBID IS persisted here
	// so that engine can build Cover Art Archive URLs without re-asking
	// MusicBrainz for an id AcoustID already reported. Wrapped in
	// storage.Retry: a transient Postgres failure (serialization/deadlock)
	// here would otherwise drop the ledger update even when identification
	// succeeded, leaving the track "unmatched" to re-run forever.
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO explo_tracks (
			  track_id, status, acoustid_id, musicbrainz_recording_id, musicbrainz_release_group_id, matched_title, matched_artist, score, error, processed_at, attempts
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, 1)
			ON CONFLICT(track_id) DO UPDATE SET
			  status = excluded.status,
			  acoustid_id = excluded.acoustid_id,
			  musicbrainz_recording_id = excluded.musicbrainz_recording_id,
			  musicbrainz_release_group_id = excluded.musicbrainz_release_group_id,
			  matched_title = excluded.matched_title,
			  matched_artist = excluded.matched_artist,
			  score = excluded.score,
			  error = excluded.error,
			  processed_at = excluded.processed_at,
			  attempts = explo_tracks.attempts + 1`,
			trackID, status, match.AcoustID, match.MusicBrainzRecordingID, match.MusicBrainzReleaseGroupID, match.Title, match.Artist, match.Score, errText)
		return err
	})
}

// ReprocessResult summarizes a manual reprocess request.
type ReprocessResult struct {
	IdentificationReset int `json:"identificationReset"`
	CoversReset         int `json:"coversReset"`
}

// Reprocess forces the explo pipeline to re-run for everything already in the
// ledger — the manual escape hatch when tracks are stranded. Two independent
// resets:
//
//  1. Failed identifications (unmatched/error) get their attempt budget and
//     backoff cleared so they re-identify from scratch. Without this a track
//     that failed exploMaxIdentifyAttempts times — e.g. during the AcoustID
//     outage when the meta-encoding bug dropped every recording — is retired
//     FOREVER, with no way back short of raw SQL. This is what makes "retry
//     metadata grabbing" possible from the UI.
//
//  2. Matched tracks keep their identified title/artist but have their cover
//     state reset, so the per-track cover engine re-resolves art. This also
//     migrates data written by the old album-wide cover pass (which painted
//     every drop with one shared cover) onto per-track art.
//
// Ledger rows are reset in place, not deleted, so the Explore playlist
// membership never flickers. The caller kicks ProcessNewTracks + BackfillCovers
// afterward to act on the reset rows.
func (s *Service) Reprocess(ctx context.Context) (ReprocessResult, error) {
	var res ReprocessResult
	if s == nil || s.db == nil {
		return res, ErrDisabled
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		// processed_at is RFC3339 TEXT but exploNextDueTimeExpr casts it
		// ::timestamp, so it must stay a valid timestamp string — NOT '' (that
		// threw "invalid input syntax for type timestamp" and broke the ledger
		// summary). The epoch is the oldest possible value, so the row is
		// immediately eligible regardless of its backoff rung.
		result, err := s.db.ExecContext(ctx, `
			UPDATE explo_tracks
			SET attempts = 0, processed_at = '1970-01-01T00:00:00Z'
			WHERE status IN ('unmatched', 'error')`)
		if err != nil {
			return err
		}
		if n, affErr := result.RowsAffected(); affErr == nil {
			res.IdentificationReset = int(n)
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("reset identification: %w", err)
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		result, err := s.db.ExecContext(ctx, `
			UPDATE explo_tracks
			SET cover_status = '', cover_attempts = 0, cover_attempted_at = ''
			WHERE status IN ('matched', 'matched-fallback') AND cover_status != ''`)
		if err != nil {
			return err
		}
		if n, affErr := result.RowsAffected(); affErr == nil {
			res.CoversReset = int(n)
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("reset covers: %w", err)
	}
	s.logger("explo: reprocess reset %d failed identification(s) and %d cover(s)", res.IdentificationReset, res.CoversReset)
	return res, nil
}

type candidateTrack struct {
	trackID string
	albumID string
	path    string
	// title/artist are the scanner's parsed tags (title + display artist). The
	// text-search fallback prefers them over re-parsing the filename: the
	// scanner already split "Artist - Album - Title.mp3" into clean fields,
	// whereas a crude filename re-parse folds the album into the title and
	// never matches. Either may be empty (a genuinely tag-less drop), in which
	// case the fallback still parses the filename.
	title  string
	artist string
	// durationSeconds is the scanner's (ffprobe-measured) duration, used as
	// the trusted reference for the text-search fallback's duration gate -
	// independent of whether fpcalc/AcoustID ever ran successfully.
	durationSeconds int
}

// Identification retry policy. Explo drops are fresh releases: AcoustID
// frequently has no fingerprint for a song until days or even WEEKS after
// release, so early passes legitimately fail for much of the batch. Failed
// rows retry on a front-loaded backoff; errors share the ladder (transient
// ones heal on the early rungs, persistent ones back off instead of
// re-failing daily). A row retires at the attempt budget so a genuinely
// unidentifiable rip doesn't hit AcoustID forever.
//
// The budget is 10, up from 5 (2026-07-16): with the old 5-attempt budget a
// fresh release's window closed after ~8 days — tracks off brand-new albums
// (DONT TAP THE GLASS was the reported case) burned out before AcoustID knew
// them and then sat retired FOREVER, even once every database had them.
// Raising the budget also retroactively un-retires previously spent rows
// (attempts 5..9 requalify against the new limit) — no migration needed.
const exploMaxIdentifyAttempts = 10

// exploRetryBackoff[i] is how long a row waits after its (i+1)-th failed
// attempt; rows past the end of the table reuse the last wait until the
// budget retires them. Front-loaded so a transiently-failed identify is
// retried within the hour, then settling to weekly: attempts 6..10 wait 7
// days each, stretching total runway to roughly two months of coverage —
// past the point where any real release is identifiable.
var exploRetryBackoff = []string{"1 hour", "6 hours", "1 day", "3 days", "7 days"}

// exploBackoffCaseOver renders a retry ladder as a SQL CASE over an attempts
// column, yielding an interval literal ('1 hour', '6 hours', ...) for
// ::interval casts. Shared by the identify and cover ladders.
func exploBackoffCaseOver(column string, ladder []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CASE %s", column)
	last := len(ladder) - 1
	for i, wait := range ladder[:last] {
		fmt.Fprintf(&b, " WHEN %d THEN '%s'", i+1, wait)
	}
	fmt.Fprintf(&b, " ELSE '%s' END", ladder[last])
	return b.String()
}

func exploBackoffCase(column string) string {
	return exploBackoffCaseOver(column, exploRetryBackoff)
}

// exploNextDueTimeExpr projects a row's next due time forward from its processed_at.
func exploNextDueTimeExpr(column string) string {
	return fmt.Sprintf(`to_char(%s::timestamp + (%s)::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`, column, exploBackoffCase("et.attempts"))
}

// exploEligibilityCheckExpr evaluates if a row's processed_at is old enough to
// retry: a text comparison of RFC3339 UTC strings, which order
// lexicographically exactly as they order in time. The now() operand MUST be
// pinned with AT TIME ZONE 'UTC': the stored column is UTC text, but bare
// to_char(now(), ...) formats in the session TimeZone — on a non-UTC Postgres
// that skewed every retry window by the full UTC offset. Every other
// timestamp site in this codebase (schema defaults, the CURRENT_TIMESTAMP
// rewriter) already pins UTC the same way.
func exploEligibilityCheckExpr(column string) string {
	return fmt.Sprintf(`%s <= to_char((now() AT TIME ZONE 'UTC') - (%s)::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`, column, exploBackoffCase("et.attempts"))
}

// findCandidateTracks returns explo-folder tracks that are due for an
// identification pass: never processed (no explo_tracks row — so re-enabling
// the feature or adding a new SAMO_EXPLO_DIRS entry backfills history), or a
// previous unmatched/error outcome whose retry window has elapsed and whose
// attempt budget isn't spent. Matched rows never re-run.
func (s *Service) findCandidateTracks(ctx context.Context) ([]candidateTrack, error) {
	dirs := s.effectiveDirs()
	if len(dirs) == 0 {
		return nil, nil
	}
	clauses := make([]string, 0, len(dirs))
	args := make([]any, 0, len(dirs))
	for _, dir := range dirs {
		clauses = append(clauses, `mf.path ILIKE ? ESCAPE '\'`)
		args = append(args, likePrefix(dir)+"%")
	}
	query := fmt.Sprintf(`
		SELECT mt.id, COALESCE(mt.album_id, ''), mf.path, COALESCE(mt.title, ''), COALESCE(mt.display_artist, ''), mt.duration_seconds
		FROM music_tracks mt
		JOIN media_files mf ON mf.track_id = mt.id
		LEFT JOIN explo_tracks et ON et.track_id = mt.id
		WHERE (
		  et.track_id IS NULL
		  OR (
		    et.status IN ('unmatched', 'error')
		    AND et.attempts < %d
		    AND %s
		  )
		) AND (%s)
		ORDER BY mt.added_at, mt.id`,
		exploMaxIdentifyAttempts,
		exploEligibilityCheckExpr("et.processed_at"),
		strings.Join(clauses, " OR "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []candidateTrack
	for rows.Next() {
		var candidate candidateTrack
		if err := rows.Scan(&candidate.trackID, &candidate.albumID, &candidate.path, &candidate.title, &candidate.artist, &candidate.durationSeconds); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

// likePrefix escapes LIKE/ILIKE wildcard characters in a filesystem path so
// it can be safely used as a `path ILIKE prefix || '%' ESCAPE '\'` prefix
// match instead of an exact-equality comparison.
func likePrefix(path string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	path = replacer.Replace(strings.TrimRight(path, "/"))
	return path + "/"
}

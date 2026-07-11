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
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/users"
)

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

	rateMu       sync.Mutex
	lastAcoustID time.Time
	mbThrottle   musicbrainzThrottle

	// processMu serializes ProcessNewTracks runs. OnScanComplete can fire in
	// quick succession (file-watcher debounce during a large drop), and
	// without this a second run could pick the same not-yet-recorded tracks
	// and waste rate-limited AcoustID calls identifying them twice.
	processMu sync.Mutex

	// backfillMu serializes cover-backfill runs. Kept separate from processMu
	// so a slow, network-bound backfill doesn't block scan-triggered processing.
	backfillMu sync.Mutex
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
	}

	// Re-derive which albums belong out of Recently Added, the ledger, and the
	// playlist (existence + membership) from the folder that's *currently*
	// configured. Fully self-correcting: narrowing (or clearing) the explo
	// folder un-hides albums that are no longer under it and drops their
	// tracks back out of the Explo playlist, while fresh ledger rows from the
	// loop above become playlist members. Runs every pass, even when nothing
	// new was found.
	hidden, unhidden, playlistChanged, err := s.syncExploState(ctx, s.effectiveDirs())
	if err != nil {
		s.logger("explo: reconcile failed: %v", err)
	}
	result.Hidden = hidden

	if (result.Scanned > 0 || hidden > 0 || unhidden > 0 || playlistChanged) && s.reloadCatalog != nil {
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
		  COALESCE(MIN(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts < %[1]d THEN datetime(et.processed_at, %[2]s) END), '')
		FROM music_tracks mt
		JOIN media_files mf ON mf.track_id = mt.id
		LEFT JOIN explo_tracks et ON et.track_id = mt.id
		WHERE %[3]s`,
		exploMaxIdentifyAttempts, exploBackoffCase("et.attempts", "+"), clause)
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
	if inFolder == 0 {
		s.logger("explo: folder %q matches 0 tracks in the library — check the path is correct and inside a scanned library", folder)
		return
	}

	parts := []string{fmt.Sprintf("%d identified", identified)}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d awaiting retry (next due %s UTC)", waiting, nextDue))
	}
	if retired > 0 {
		parts = append(parts, fmt.Sprintf("%d retired after %d failed attempts", retired, exploMaxIdentifyAttempts))
	}
	s.logger("explo: nothing due this pass — %d track(s) under %q: %s", inFolder, folder, strings.Join(parts, ", "))
}

// exploPathClause builds a SQL predicate matching media_files under any of the
// configured explo folders, plus its bound args. With no folders it returns a
// constant-false predicate ("0") so callers that AND on it hide/keep nothing
// and callers that negate it (un-hide, prune) act on everything - i.e. "explo
// off" means "nothing is explo," which is exactly what we want for recovery.
func exploPathClause(dirs []string) (string, []any) {
	if len(dirs) == 0 {
		return "0", nil
	}
	clauses := make([]string, 0, len(dirs))
	args := make([]any, 0, len(dirs))
	for _, dir := range dirs {
		clauses = append(clauses, `mf.path LIKE ? ESCAPE '\'`)
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
	res, err := s.db.ExecContext(ctx, hideSQL, append(append([]any{}, args...), args...)...)
	if err != nil {
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
	res2, err := s.db.ExecContext(ctx, unhideSQL, append(append([]any{}, args...), args...)...)
	if err != nil {
		return hidden, 0, fmt.Errorf("explo unhide albums: %w", err)
	}
	unhidden, _ = res2.RowsAffected()
	return hidden, unhidden, nil
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
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
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
		if _, err := s.db.ExecContext(ctx, `
			UPDATE music_playlists SET owner_id = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, ownerID, playlistID); err != nil {
			return false, fmt.Errorf("re-own explo playlist: %w", err)
		}
		changed = true
	}

	// Name repair: a row created under an older configured/default name is
	// renamed in place (adoption above is name-agnostic), keeping its id,
	// members, and client references stable.
	if currentName != s.playlistName {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE music_playlists SET name = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, s.playlistName, playlistID); err != nil {
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

// syncExploState reconciles all persisted explo side-effects (hidden flags,
// the ledger, and the playlist) to the given folder set. Callers pass the
// currently-effective dirs; an empty set fully un-does everything. Returns
// how many albums were newly hidden/un-hidden and whether the playlist row
// itself changed (membership, ownership, or creation).
func (s *Service) syncExploState(ctx context.Context, dirs []string) (hidden, unhidden int64, playlistChanged bool, err error) {
	if err := s.pruneExploLedger(ctx, dirs); err != nil {
		s.logger("explo: %v", err)
	}
	playlistChanged, playlistErr := s.reconcileExploPlaylist(ctx)
	if playlistErr != nil {
		s.logger("explo: %v", playlistErr)
	}
	hidden, unhidden, err = s.reconcileHiddenAlbums(ctx, dirs)
	return hidden, unhidden, playlistChanged, err
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
	hidden, unhidden, playlistChanged, err := s.syncExploState(ctx, s.effectiveDirs())
	if err != nil {
		return err
	}
	if (hidden > 0 || unhidden > 0 || playlistChanged) && s.reloadCatalog != nil {
		s.logger("explo: reconcile hid %d, un-hid %d album(s) in Recently Added", hidden, unhidden)
		return s.reloadCatalog(ctx)
	}
	return nil
}

// BackfillCovers fetches album art for already-identified explo albums that
// don't have it yet: the ones matched before cover support existed, plus any
// where an earlier cover fetch failed. It resolves each album's cover from the
// MusicBrainz recording ID stored at match time (no re-fingerprinting, no
// AcoustID re-billing) and downloads it into the local cover store. Safe to
// call repeatedly - each album is attempted once (tracked by
// explo_tracks.cover_status), and it serializes on its own mutex so it never
// blocks scan-triggered processing. Reloads the catalog if it applied any art.
func (s *Service) BackfillCovers(ctx context.Context) error {
	if s == nil || s.db == nil || s.metadataApply == nil {
		return nil
	}
	s.backfillMu.Lock()
	defer s.backfillMu.Unlock()
	applied, err := s.backfillMissingCovers(ctx, s.effectiveDirs())
	if err != nil {
		return err
	}
	if applied > 0 {
		s.logger("explo: backfilled cover art for %d album(s)", applied)
		if s.reloadCatalog != nil {
			return s.reloadCatalog(ctx)
		}
	}
	return nil
}

func (s *Service) backfillMissingCovers(ctx context.Context, dirs []string) (int, error) {
	match, args := exploPathClause(dirs)
	// One representative recording MBID per explo album not yet attempted.
	query := fmt.Sprintf(`
		SELECT mt.album_id, MAX(et.musicbrainz_recording_id)
		FROM explo_tracks et
		JOIN music_tracks mt ON mt.id = et.track_id
		JOIN media_files mf ON mf.track_id = mt.id
		WHERE et.cover_status = ''
		  AND et.musicbrainz_recording_id != ''
		  AND COALESCE(mt.album_id, '') != ''
		  AND %s
		GROUP BY mt.album_id`, match)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("explo backfill query: %w", err)
	}
	type target struct{ albumID, recordingMBID string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.albumID, &t.recordingMBID); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(targets) > 0 {
		// Announce up front: the MusicBrainz throttle stretches a batch over
		// minutes, and the completion line only prints at the very end.
		s.logger("explo: backfilling cover art for %d album(s)", len(targets))
	}

	applied := 0
	for _, t := range targets {
		select {
		case <-ctx.Done():
			return applied, ctx.Err()
		default:
		}
		s.throttleMusicBrainz(ctx)
		rg, err := fetchReleaseGroupID(ctx, s.httpClient, t.recordingMBID)
		if err != nil {
			// Transient - leave cover_status pending so it retries next run.
			s.logger("explo: cover lookup failed for album %s: %v", t.albumID, err)
			continue
		}
		if rg != "" {
			if err := s.applyAlbumCover(ctx, t.albumID, rg); err != nil {
				s.logger("explo: apply cover failed for album %s: %v", t.albumID, err)
			} else {
				applied++
			}
		}
		// Mark every pending explo track on this album as attempted (cover found
		// or not) so we don't re-query MusicBrainz for it on every scan.
		if _, err := s.db.ExecContext(ctx, `
			UPDATE explo_tracks SET cover_status = 'done'
			WHERE cover_status = '' AND track_id IN (SELECT id FROM music_tracks WHERE album_id = ?)`, t.albumID); err != nil {
			s.logger("explo: mark cover_status failed for album %s: %v", t.albumID, err)
		}
	}
	return applied, nil
}

// applyAlbumCover applies a Cover Art Archive front cover (by release-group
// MBID) to an album through the normal override pipeline, which downloads the
// image into the local cover store so it serves same-origin (CSP-safe).
func (s *Service) applyAlbumCover(ctx context.Context, albumID, releaseGroupMBID string) error {
	url := coverArtArchiveURL(releaseGroupMBID)
	if url == "" {
		return nil
	}
	_, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
		TargetKind: string(metadata.ApplyTargetMusicAlbum),
		TargetID:   albumID,
		// ID satisfies the apply validation (needs a Title or ID); only the
		// "cover" field is actually applied, so nothing else on the album moves.
		Candidate:          metadata.SearchResult{Provider: "explo", MediaType: "album", ID: releaseGroupMBID, Cover: &catalog.Image{URL: url}},
		Fields:             []string{"cover"},
		DeferCatalogReload: true,
	})
	return err
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

	fallback, fallbackMatched, fallbackErr := s.identifyByTextSearch(ctx, candidate.path, candidate.durationSeconds)
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
	if _, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
		TargetKind:         string(metadata.ApplyTargetMusicTrack),
		TargetID:           trackID,
		Candidate:          trackCandidate,
		Fields:             []string{"title", "displayArtist", "externalIds"},
		DeferCatalogReload: true,
	}); err != nil {
		return fmt.Errorf("apply track metadata: %w", err)
	}

	if albumID == "" {
		return nil
	}
	albumCandidate := metadata.SearchResult{
		Provider:  match.Source,
		MediaType: "album",
		Title:     match.Album,
	}
	if match.Artist != "" {
		albumCandidate.Authors = []catalog.ContributorRef{{Name: match.Artist}}
	}
	fields := []string{"displayArtist"}
	if strings.TrimSpace(match.Album) != "" {
		fields = append(fields, "title")
	}
	// Fetch album art from the Cover Art Archive so identified explo albums get
	// a real cover instead of a blank tile. The apply pipeline downloads the URL
	// into the local cover store, so it renders same-origin (CSP-safe) and
	// survives even if CAA is later unreachable. A missing cover (CAA 404) just
	// leaves the album art untouched - resolveCoverInCandidate soft-fails.
	if url := coverArtArchiveURL(match.MusicBrainzReleaseGroupID); url != "" {
		albumCandidate.Cover = &catalog.Image{URL: url}
		fields = append(fields, "cover")
	}
	if _, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
		TargetKind:         string(metadata.ApplyTargetMusicAlbum),
		TargetID:           albumID,
		Candidate:          albumCandidate,
		Fields:             fields,
		DeferCatalogReload: true,
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
	// Upsert (not INSERT OR IGNORE): a retried track REPLACES its ledger row
	// with the newest outcome and bumps `attempts`, so findCandidateTracks'
	// retry budget is enforceable. cover_status is deliberately untouched —
	// the cover backfill owns that column.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO explo_tracks (
		  track_id, status, acoustid_id, musicbrainz_recording_id, matched_title, matched_artist, score, error, processed_at, attempts
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, 1)
		ON CONFLICT(track_id) DO UPDATE SET
		  status = excluded.status,
		  acoustid_id = excluded.acoustid_id,
		  musicbrainz_recording_id = excluded.musicbrainz_recording_id,
		  matched_title = excluded.matched_title,
		  matched_artist = excluded.matched_artist,
		  score = excluded.score,
		  error = excluded.error,
		  processed_at = excluded.processed_at,
		  attempts = explo_tracks.attempts + 1`,
		trackID, status, match.AcoustID, match.MusicBrainzRecordingID, match.Title, match.Artist, match.Score, errText)
	return err
}

type candidateTrack struct {
	trackID string
	albumID string
	path    string
	// durationSeconds is the scanner's (ffprobe-measured) duration, used as
	// the trusted reference for the text-search fallback's duration gate -
	// independent of whether fpcalc/AcoustID ever ran successfully.
	durationSeconds int
}

// Identification retry policy. Explo drops are fresh releases: AcoustID
// frequently has no fingerprint for a song until days/weeks after release, so
// the first pass (hours after the drop lands) legitimately fails for much of
// the batch. Failed rows retry on a front-loaded backoff — fresh releases
// usually become identifiable within days, and a flat week-long first wait
// left a whole drop visibly broken while the server had nothing to do.
// Errors share the ladder: transient ones heal on the early rungs, persistent
// ones back off instead of re-failing daily. Either way a row retires at the
// attempt budget so a genuinely unidentifiable rip doesn't hit AcoustID
// forever.
const exploMaxIdentifyAttempts = 5

// exploRetryBackoff[i] is how long a row waits after its (i+1)-th failed
// attempt; rows past the end of the table reuse the last wait until the
// budget retires them.
var exploRetryBackoff = []string{"1 day", "2 days", "4 days", "7 days"}

// exploBackoffCase renders the ladder as a SQL CASE over an attempts column:
// sign "-" for the eligibility check against datetime('now', ...), sign "+"
// to project a row's next due time forward from its processed_at.
func exploBackoffCase(column, sign string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CASE %s", column)
	last := len(exploRetryBackoff) - 1
	for i, wait := range exploRetryBackoff[:last] {
		fmt.Fprintf(&b, " WHEN %d THEN '%s%s'", i+1, sign, wait)
	}
	fmt.Fprintf(&b, " ELSE '%s%s' END", sign, exploRetryBackoff[last])
	return b.String()
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
		clauses = append(clauses, "mf.path LIKE ? ESCAPE '\\'")
		args = append(args, likePrefix(dir)+"%")
	}
	query := fmt.Sprintf(`
		SELECT mt.id, COALESCE(mt.album_id, ''), mf.path, mt.duration_seconds
		FROM music_tracks mt
		JOIN media_files mf ON mf.track_id = mt.id
		LEFT JOIN explo_tracks et ON et.track_id = mt.id
		WHERE (
		  et.track_id IS NULL
		  OR (
		    et.status IN ('unmatched', 'error')
		    AND et.attempts < %d
		    AND et.processed_at <= datetime('now', %s)
		  )
		) AND (%s)
		ORDER BY mt.added_at, mt.id`,
		exploMaxIdentifyAttempts,
		exploBackoffCase("et.attempts", "-"),
		strings.Join(clauses, " OR "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []candidateTrack
	for rows.Next() {
		var candidate candidateTrack
		if err := rows.Scan(&candidate.trackID, &candidate.albumID, &candidate.path, &candidate.durationSeconds); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

// likePrefix escapes SQLite LIKE wildcard characters in a filesystem path so
// it can be safely used as a `path LIKE prefix || '%' ESCAPE '\'` prefix
// match instead of an exact-equality comparison.
func likePrefix(path string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	path = replacer.Replace(strings.TrimRight(path, "/"))
	return path + "/"
}

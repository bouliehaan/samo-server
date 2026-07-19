package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/storage"
)

// Cover retry policy. Unlike identification (which is bounded because a
// never-identified rip should stop burning AcoustID quota), covers get a
// long budget: fresh releases reach Cover Art Archive days or weeks after
// MusicBrainz knows them, and the placeholder keeps the UI whole while we
// wait. 24 attempts across the ladder below spans roughly five months; the
// weekly drop rotation usually deletes the files long before that.
const exploMaxCoverAttempts = 24

// exploCoverBackoff[i] is how long a track waits after its (i+1)-th
// unsuccessful cover pass; rows past the end reuse the last wait. Front-
// loaded like the identify ladder: most misses are "CAA doesn't have it
// YET", so early retries are cheap wins, then it settles to weekly.
var exploCoverBackoff = []string{"30 minutes", "2 hours", "6 hours", "1 day", "3 days", "7 days"}

// Cover state machine, per explo_tracks row. Covers are resolved and applied
// PER TRACK, not per album: every explo drop is an unrelated single, but the
// untagged files all live in one folder, so the scanner groups them into ONE
// path-derived album. An album-wide cover therefore paints every track with a
// single image (the "all explo tracks show the same art" bug) and the playlist
// can only ever composite one distinct cover. Each track carries its own
// MusicBrainz ids from identification, so we resolve and apply its own art —
// distinct rows, a real player cover, and a genuine 2x2 playlist grid.
//
//	''            never attempted (or reset by a reprocess) — due now
//	'pending'     attempted, nothing verified yet, retrying on the ladder
//	'placeholder' generated tile applied so the UI is never blank; still
//	              retrying real sources on the ladder
//	'done'        a cover was VERIFIED as local bytes on disk — terminal
const (
	coverStatusPending     = "pending"
	coverStatusPlaceholder = "placeholder"
	coverStatusDone        = "done"
)

// CoverStore is the slice of the covers service explo needs: verified
// downloads into the local store, and storage for generated placeholders.
// Verification is the point — DownloadFromURL only succeeds when real image
// bytes landed on disk, which is what gates 'done'.
type CoverStore interface {
	DownloadFromURL(ctx context.Context, url string) (*catalog.Image, error)
	StoreGenerated(ctx context.Context, key string, data []byte, mimeType string) (*catalog.Image, error)
}

// coverArtArchiveReleaseGroupURL builds the CAA "front cover by release
// group" URL, or "" if there's no id. CAA 307-redirects to the actual image.
func coverArtArchiveReleaseGroupURL(releaseGroupMBID string) string {
	id := strings.TrimSpace(releaseGroupMBID)
	if id == "" {
		return ""
	}
	return caaBaseURL + "/release-group/" + id + "/front-500"
}

// coverArtArchiveReleaseURL builds the CAA "front cover by release" URL.
// Individual releases frequently have art when their release group doesn't.
func coverArtArchiveReleaseURL(releaseMBID string) string {
	id := strings.TrimSpace(releaseMBID)
	if id == "" {
		return ""
	}
	return caaBaseURL + "/release/" + id + "/front-500"
}

// caaBaseURL is a var so tests can point the whole CAA rung at a stub.
var caaBaseURL = "https://coverartarchive.org"

// coverTarget is one identified explo TRACK due for a cover pass, with
// everything the source chain can use: the persisted MusicBrainz ids from
// identification and the display artist/album/track strings for the text-
// searched rungs. albumID is carried only to overlay the identified album
// title onto those rungs — the cover itself is applied to the track.
type coverTarget struct {
	trackID        string
	albumID        string
	releaseGroupID string
	recordingMBID  string
	artist         string
	album          string
	title          string
	status         string
}

// BackfillCovers fetches art for identified explo tracks that don't have
// verified local art yet. Each due track walks a source chain — Cover Art
// Archive by release group, CAA by individual release, then the iTunes and
// Deezer album/song searches — and only a download that actually landed bytes
// in the local cover store marks the track 'done'. Tracks where every source
// missed get a generated placeholder tile (so the UI is never blank) and keep
// retrying on a front-loaded backoff ladder. Safe to call repeatedly and
// serialized on its own mutex so it never blocks scan-triggered processing.
// Reloads the catalog if it changed anything.
func (s *Service) BackfillCovers(ctx context.Context) error {
	if s == nil || s.db == nil || s.metadataApply == nil || s.covers == nil {
		// Without the cover store nothing can be verified, so running would
		// only burn attempts; the pass waits until main wires the store.
		return nil
	}
	s.backfillMu.Lock()
	defer s.backfillMu.Unlock()
	applied, placeholders, err := s.backfillMissingCovers(ctx, s.effectiveDirs())
	if err != nil {
		return err
	}
	if applied > 0 || placeholders > 0 {
		s.logger("explo: cover pass applied %d real cover(s), %d placeholder(s)", applied, placeholders)
		// The Explore playlist's tile art is DERIVED from its tracks' covers
		// (enrichPlaylistImagesFromTracks composites the first distinct four),
		// but applying a track cover doesn't touch the playlist row. Bump it so
		// the Android mirror — which delta-syncs on updated_at — re-pulls the
		// playlist and finally shows the 2x2 grid instead of a stale single
		// cover from when only one track had art.
		s.touchSystemPlaylists(ctx)
		if s.reloadCatalog != nil {
			return s.reloadCatalog(ctx)
		}
	}
	return nil
}

// touchSystemPlaylists bumps updated_at on server-managed (system) playlists —
// the Explore queue — so delta-syncing clients re-pull their derived cover art
// after a cover pass changes the underlying tracks.
func (s *Service) touchSystemPlaylists(ctx context.Context) {
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `UPDATE music_playlists SET updated_at = CURRENT_TIMESTAMP WHERE system = 1`)
		return err
	}); err != nil {
		s.logger("explo: touch system playlists failed: %v", err)
	}
}

func (s *Service) backfillMissingCovers(ctx context.Context, dirs []string) (applied, placeholders int, err error) {
	targets, err := s.findCoverTargets(ctx, dirs)
	if err != nil {
		return 0, 0, err
	}
	if len(targets) == 0 {
		return 0, 0, nil
	}
	// Announce up front: the throttled source chain stretches a batch over
	// minutes, and the completion line only prints at the very end.
	s.logger("explo: resolving cover art for %d track(s)", len(targets))

	for _, target := range targets {
		select {
		case <-ctx.Done():
			return applied, placeholders, ctx.Err()
		default:
		}

		existing := s.existingTrackCover(ctx, target.trackID)

		// 1. Real local art that is NOT our own placeholder (a
		//    successfully-downloaded cover, scanner sidecar/embedded art, or
		//    an admin upload) → keep it, mark done, touch no network.
		if existing.localPath != "" && !existing.isOwnPlaceholder {
			s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, false)
			continue
		}

		// 2. A URL-only cover left behind by an earlier pass: the local
		//    download failed at the time but the external URL may still render
		//    (via redirect). VERIFY it before doing anything — a live one is
		//    adopted locally (same-origin) and finished; a genuinely dead one
		//    falls through to the chain.
		if existing.overrideURL != "" && existing.localPath == "" {
			if s.verifyCoverURL(ctx, existing.overrideURL) {
				if err := s.applyTrackCover(ctx, target.trackID, existing.overrideURL); err != nil {
					s.logger("explo: re-adopt cover failed for track %s: %v", target.trackID, err)
					s.setTrackCoverStatus(ctx, target.trackID, coverStatusPending, true)
					continue
				}
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, true)
				applied++
				continue
			}
		}

		// 2.5 An unidentified drop (no MusicBrainz ids and no artist/title to
		//     search) can't resolve real art yet — give it its OWN generated
		//     placeholder so it shows a distinct tile instead of borrowing the
		//     playlist's first cover, and so the playlist grid gets four distinct
		//     tiles instead of collapsing to one shared album cover. Identify
		//     keeps retrying on its own ladder; when it lands, resetTrackCoverState
		//     re-opens this row and a later pass replaces the placeholder with
		//     real art.
		if target.recordingMBID == "" && target.releaseGroupID == "" &&
			strings.TrimSpace(target.artist) == "" && strings.TrimSpace(target.title) == "" {
			if existing.isOwnPlaceholder {
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusPlaceholder, true)
			} else if s.applyPlaceholderCover(ctx, target.trackID) {
				placeholders++
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusPlaceholder, true)
			} else {
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusPending, true)
			}
			continue
		}

		// 3. Try the source chain for real art.
		if url := s.resolveCoverURL(ctx, target); url != "" {
			if err := s.applyTrackCover(ctx, target.trackID, url); err != nil {
				s.logger("explo: apply cover failed for track %s: %v", target.trackID, err)
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusPending, true)
				continue
			}
			s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, true)
			applied++
			continue
		}

		// 4. Everything missed. Guarantee a tile so the UI is never blank —
		//    but a placeholder may only ever land where there is no real cover
		//    to lose. By construction we reach here only when the track has no
		//    local art (step 1) and no live URL (step 2, verified dead), so
		//    replacing whatever is there — nothing, a dead URL, or our own
		//    prior placeholder — never destroys a working cover.
		nextStatus := coverStatusPending
		if existing.isOwnPlaceholder {
			nextStatus = coverStatusPlaceholder // already on file, keep as-is
		} else if s.applyPlaceholderCover(ctx, target.trackID) {
			nextStatus = coverStatusPlaceholder
			placeholders++
		}
		s.setTrackCoverStatus(ctx, target.trackID, nextStatus, true)
	}
	return applied, placeholders, nil
}

// findCoverTargets returns one row per identified explo TRACK due for a cover
// pass: never attempted, or past its ladder wait, and under the attempt
// budget. Grouped by track (not album) so each drop resolves its own art from
// its own MusicBrainz ids.
func (s *Service) findCoverTargets(ctx context.Context, dirs []string) ([]coverTarget, error) {
	match, args := exploPathClause(dirs)
	query := fmt.Sprintf(`
		SELECT et.track_id,
		       COALESCE(MAX(mt.album_id), ''),
		       MAX(et.musicbrainz_release_group_id),
		       MAX(et.musicbrainz_recording_id),
		       MAX(et.matched_artist),
		       COALESCE(MAX(ma.title), ''),
		       MAX(et.matched_title),
		       MAX(et.cover_status)
		FROM explo_tracks et
		JOIN music_tracks mt ON mt.id = et.track_id
		JOIN media_files mf ON mf.track_id = mt.id
		LEFT JOIN music_albums ma ON ma.id = mt.album_id
		WHERE et.cover_status IN ('', '%s', '%s')
		  AND et.status IN ('matched', 'matched-fallback', 'unmatched', 'error')
		  AND et.cover_attempts < %d
		  AND (et.cover_attempted_at = '' OR %s)
		  AND %s
		GROUP BY et.track_id`,
		coverStatusPending, coverStatusPlaceholder,
		exploMaxCoverAttempts,
		exploCoverEligibilityExpr("et.cover_attempted_at"),
		match)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("explo cover targets query: %w", err)
	}
	defer rows.Close()

	var targets []coverTarget
	for rows.Next() {
		var t coverTarget
		if err := rows.Scan(&t.trackID, &t.albumID, &t.releaseGroupID, &t.recordingMBID, &t.artist, &t.album, &t.title, &t.status); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The scanner's album title is often drop-folder noise ("2026-28"); the
	// identified title lives in the album's metadata override. Overlay it so
	// the text-searched album rungs query for the real record.
	for index := range targets {
		if title := s.overriddenAlbumTitle(ctx, targets[index].albumID); title != "" {
			targets[index].album = title
		}
	}
	return targets, nil
}

// exploCoverEligibilityExpr evaluates whether a cover attempt's backoff has
// elapsed, comparing the stored RFC3339 UTC text against a UTC-pinned now().
func exploCoverEligibilityExpr(column string) string {
	return fmt.Sprintf(`%s <= to_char((now() AT TIME ZONE 'UTC') - (%s)::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		column, exploBackoffCaseOver("et.cover_attempts", exploCoverBackoff))
}

// resolveCoverURL walks the source chain and returns the first URL whose
// image VERIFIABLY downloaded into the local cover store, or "" when every
// source missed. Order is trust-descending: CAA is keyed by the exact
// MusicBrainz identity that identified the audio, while iTunes/Deezer are
// text searches gated by a strict name match.
func (s *Service) resolveCoverURL(ctx context.Context, target coverTarget) string {
	releaseGroupID := strings.TrimSpace(target.releaseGroupID)
	var releaseIDs []string
	refsLoaded := false

	// Rows matched before the release group was persisted (or via the text
	// fallback) resolve it from the recording — one throttled MusicBrainz
	// call, same as the old pipeline did for every track.
	if releaseGroupID == "" && strings.TrimSpace(target.recordingMBID) != "" {
		s.throttleMusicBrainz(ctx)
		refs, err := fetchRecordingReleaseRefs(ctx, s.httpClient, target.recordingMBID)
		if err != nil {
			s.logger("explo: musicbrainz release lookup failed for track %s: %v", target.trackID, err)
		} else {
			releaseGroupID = refs.ReleaseGroupID
			releaseIDs = refs.ReleaseIDs
			refsLoaded = true
		}
	}

	if url := coverArtArchiveReleaseGroupURL(releaseGroupID); url != "" {
		if s.verifyCoverURL(ctx, url) {
			return url
		}
	}

	// CAA by individual release. If the release group came from the ledger
	// we haven't asked MusicBrainz for the release list yet — do it now,
	// only because the cheaper rung already missed.
	if !refsLoaded && strings.TrimSpace(target.recordingMBID) != "" {
		s.throttleMusicBrainz(ctx)
		refs, err := fetchRecordingReleaseRefs(ctx, s.httpClient, target.recordingMBID)
		if err != nil {
			s.logger("explo: musicbrainz release lookup failed for track %s: %v", target.trackID, err)
		} else {
			releaseIDs = refs.ReleaseIDs
		}
	}
	for index, releaseID := range releaseIDs {
		if index >= 3 {
			// A popular recording can sit on dozens of releases; three
			// attempts bounds the pass without giving up the common case
			// (the first releases listed are the canonical ones).
			break
		}
		if url := coverArtArchiveReleaseURL(releaseID); url != "" {
			if s.verifyCoverURL(ctx, url) {
				return url
			}
		}
	}

	// Album-level text searches first (they return the album's own art when
	// the album identity is sound), then song-level searches — the
	// compilation-proof rungs. A classic hit's MusicBrainz recording often
	// lives only on sampler release groups, so every album-identity rung
	// above yields nothing (or worse, sampler art the name gate rejects);
	// searching by artist + TRACK title returns the canonical release's
	// artwork directly. This is what fixes "Ordinary World" and "I Love the
	// Nightlife" rendering artless forever.
	if url := s.verifiedTextSearchCover(ctx, target.trackID, "itunes album", func(ctx context.Context) (string, error) {
		s.itunesPacer.wait(ctx, itunesMinInterval)
		return lookupITunesAlbumCover(ctx, s.httpClient, target.artist, target.album)
	}); url != "" {
		return url
	}
	if url := s.verifiedTextSearchCover(ctx, target.trackID, "deezer album", func(ctx context.Context) (string, error) {
		s.deezerPacer.wait(ctx, deezerMinInterval)
		return lookupDeezerAlbumCover(ctx, s.httpClient, target.artist, target.album)
	}); url != "" {
		return url
	}
	if url := s.verifiedTextSearchCover(ctx, target.trackID, "itunes song", func(ctx context.Context) (string, error) {
		s.itunesPacer.wait(ctx, itunesMinInterval)
		return lookupITunesTrackCover(ctx, s.httpClient, target.artist, target.title)
	}); url != "" {
		return url
	}
	return s.verifiedTextSearchCover(ctx, target.trackID, "deezer track", func(ctx context.Context) (string, error) {
		s.deezerPacer.wait(ctx, deezerMinInterval)
		return lookupDeezerTrackCover(ctx, s.httpClient, target.artist, target.title)
	})
}

// verifiedTextSearchCover runs one text-search rung and returns its URL only
// when the image verifiably downloaded into the local cover store.
func (s *Service) verifiedTextSearchCover(ctx context.Context, trackID, rung string, lookup func(context.Context) (string, error)) string {
	url, err := lookup(ctx)
	if err != nil {
		s.logger("explo: %s cover search failed for track %s: %v", rung, trackID, err)
		return ""
	}
	if url == "" || !s.verifyCoverURL(ctx, url) {
		return ""
	}
	return url
}

// verifyCoverURL is the trust gate for every source: true only when the URL
// downloaded actual image bytes into the local cover store. The later
// metadata apply re-requests the same URL and hits the store's cache, so
// nothing downloads twice — and no override can ever again persist a URL
// that was never seen to work (the old pipeline's "blank tile with a dead
// CAA redirect on file" failure).
func (s *Service) verifyCoverURL(ctx context.Context, url string) bool {
	if s.covers == nil || strings.TrimSpace(url) == "" {
		return false
	}
	image, err := s.covers.DownloadFromURL(ctx, url)
	return err == nil && image != nil && strings.TrimSpace(image.Path) != ""
}

// applyTrackCover persists a VERIFIED cover URL as the track's override
// through the normal apply pipeline (which resolves it from the cover store's
// cache to a local path, so it serves same-origin). Applied to the TRACK, not
// its album, so each explo drop shows its own art.
func (s *Service) applyTrackCover(ctx context.Context, trackID, url string) error {
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
			TargetKind: string(metadata.ApplyTargetMusicTrack),
			TargetID:   trackID,
			// ID satisfies the apply validation (needs a Title or ID); only the
			// "cover" field is applied, so nothing else on the track moves.
			Candidate:          metadata.SearchResult{Provider: "explo", MediaType: "recording", ID: trackID, Cover: &catalog.Image{URL: url}},
			Fields:             []string{"cover"},
			DeferCatalogReload: true,
		})
		return err
	})
}

// applyPlaceholderCover generates and applies the deterministic placeholder
// tile for a track. Returns whether a placeholder is now on file. Keyed by
// track id, so even the fallback tiles differ per drop.
func (s *Service) applyPlaceholderCover(ctx context.Context, trackID string) bool {
	if s.covers == nil {
		return false
	}
	data := placeholderPNG(trackID)
	if len(data) == 0 {
		return false
	}
	image, err := s.covers.StoreGenerated(ctx, "explo-placeholder:"+trackID, data, "image/png")
	if err != nil {
		s.logger("explo: store placeholder failed for track %s: %v", trackID, err)
		return false
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, applyErr := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
			TargetKind: string(metadata.ApplyTargetMusicTrack),
			TargetID:   trackID,
			Candidate: metadata.SearchResult{
				Provider:  "explo",
				MediaType: "recording",
				ID:        trackID,
				Cover:     &catalog.Image{ID: image.ID, Path: image.Path, MimeType: image.MimeType},
			},
			Fields:             []string{"cover"},
			DeferCatalogReload: true,
		})
		return applyErr
	}); err != nil {
		s.logger("explo: apply placeholder failed for track %s: %v", trackID, err)
		return false
	}
	return true
}

// setTrackCoverStatus writes the outcome of a cover pass onto the track's
// explo_tracks row. bumpAttempts is false for the "already had local art"
// fast path, which is a bookkeeping correction, not an attempt.
func (s *Service) setTrackCoverStatus(ctx context.Context, trackID, status string, bumpAttempts bool) {
	bump := 0
	if bumpAttempts {
		bump = 1
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			UPDATE explo_tracks
			SET cover_status = ?, cover_attempts = cover_attempts + %d, cover_attempted_at = CURRENT_TIMESTAMP
			WHERE cover_status != '%s'
			  AND track_id = ?`, bump, coverStatusDone),
			status, trackID)
		return err
	}); err != nil {
		s.logger("explo: mark cover_status failed for track %s: %v", trackID, err)
	}
}

// resetTrackCoverState re-opens a track's cover pass: cleared status, zeroed
// attempts, empty attempted-at. Called when a drop is freshly identified, so a
// placeholder applied while it was unmatched gets replaced with real art on the
// next BackfillCovers instead of being treated as already-attempted.
func (s *Service) resetTrackCoverState(ctx context.Context, trackID string) error {
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE explo_tracks
			SET cover_status = '', cover_attempts = 0, cover_attempted_at = ''
			WHERE track_id = ?`, trackID)
		return err
	})
}

// trackCoverState is what a track's CURRENT cover looks like to the backfill:
// whether it has a real on-disk file, an external URL, and whether the on-disk
// file is this track's own generated placeholder (which the chain is allowed
// to replace with real art, unlike any genuine cover).
type trackCoverState struct {
	localPath        string
	overrideURL      string
	isOwnPlaceholder bool
}

// existingTrackCover inspects the track's effective cover. Precedence mirrors
// the catalog projection: a cover override is what clients are actually shown,
// so when one exists it alone is judged; only in its absence does scanner-
// resolved art on the track row count. Crucially it surfaces a URL-only
// override as overrideURL rather than pretending the track has no cover — the
// caller must verify that URL before ever replacing it.
func (s *Service) existingTrackCover(ctx context.Context, trackID string) trackCoverState {
	var state trackCoverState
	var fieldsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT fields_json FROM metadata_overrides WHERE target_kind = 'music-track' AND target_id = ?`,
		trackID).Scan(&fieldsJSON)
	if err == nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(fieldsJSON), &fields) == nil {
			if coverRaw, ok := fields["cover"]; ok {
				state.overrideURL, state.localPath = firstImageURLAndPathOnDisk(string(coverRaw))
				if state.localPath != "" {
					state.isOwnPlaceholder = s.isPlaceholderCoverPath(ctx, trackID, state.localPath)
				}
				return state // override is authoritative
			}
		}
	}

	var imagesJSON string
	if err := s.db.QueryRowContext(ctx,
		`SELECT images_json FROM music_tracks WHERE id = ?`, trackID).Scan(&imagesJSON); err == nil {
		_, state.localPath = firstImageURLAndPathOnDisk(imagesJSON)
		// Scanner/embedded art is never our generated placeholder.
	}
	return state
}

// isPlaceholderCoverPath reports whether the given local cover path is this
// track's own generated placeholder tile. StoreGenerated is idempotent and
// keyed deterministically, so regenerating yields the stored entry's path
// without re-writing anything — a cheap identity probe that needs no schema.
func (s *Service) isPlaceholderCoverPath(ctx context.Context, trackID, path string) bool {
	if s.covers == nil {
		return false
	}
	data := placeholderPNG(trackID)
	if len(data) == 0 {
		return false
	}
	image, err := s.covers.StoreGenerated(ctx, "explo-placeholder:"+trackID, data, "image/png")
	if err != nil || image == nil {
		return false
	}
	return strings.TrimSpace(image.Path) != "" && image.Path == path
}

// firstImageURLAndPathOnDisk decodes a JSON image list (or single image) and
// returns the first non-empty external URL and the first local path that
// exists as a non-empty file. Either may be "".
func firstImageURLAndPathOnDisk(rawJSON string) (url, path string) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return "", ""
	}
	var images []catalog.Image
	if err := json.Unmarshal([]byte(rawJSON), &images); err != nil {
		var single catalog.Image
		if json.Unmarshal([]byte(rawJSON), &single) != nil {
			return "", ""
		}
		images = []catalog.Image{single}
	}
	for _, image := range images {
		if url == "" {
			if u := strings.TrimSpace(image.URL); u != "" {
				url = u
			}
		}
		if path == "" {
			if p := strings.TrimSpace(image.Path); p != "" {
				if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
					path = p
				}
			}
		}
	}
	return url, path
}

// overriddenAlbumTitle returns the identified album title from the album's
// metadata override, or "" when none is on file. Used only to give the text-
// searched album rungs a real album name to query.
func (s *Service) overriddenAlbumTitle(ctx context.Context, albumID string) string {
	if strings.TrimSpace(albumID) == "" {
		return ""
	}
	var fieldsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT fields_json FROM metadata_overrides WHERE target_kind = 'music-album' AND target_id = ?`,
		albumID).Scan(&fieldsJSON)
	if err != nil {
		if err != sql.ErrNoRows {
			s.logger("explo: album override read failed for %s: %v", albumID, err)
		}
		return ""
	}
	var fields struct {
		Title string `json:"title"`
	}
	if json.Unmarshal([]byte(fieldsJSON), &fields) != nil {
		return ""
	}
	return strings.TrimSpace(fields.Title)
}

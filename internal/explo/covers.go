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
	"github.com/bouliehaan/samo-server/internal/musicrelease"
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

// deezerAlbumURL is the root of Deezer's album API, whose /image redirects to
// the album's cover.
var deezerAlbumURL = "https://api.deezer.com/album"

// requestedDeezerCoverURL is Deezer's 1000px cover of an album requested from
// Deezer (deezer-<n>), the cover Search for new showed; "" for any other.
func requestedDeezerCoverURL(albumID string) string {
	if id := DeezerID(albumID); id != "" {
		return deezerAlbumURL + "/" + id + "/image?size=xl"
	}
	return ""
}

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
		album := s.coverAlbumOf(ctx, target.trackID, target.releaseGroupID, target.artist, target.album)
		albumArt, hasAlbumArt := s.albumCover(ctx, album, target.trackID)

		// 0. A track of an album requested whole wears the album's cover once
		//    it has one, over any art of its own: the album is one record, and
		//    the sharer's embedded art on one of its tracks is no reason for
		//    that track to look like another.
		if album.requested != "" && hasAlbumArt && s.adoptCover(ctx, target.trackID, existing, albumArt, true) {
			applied++
			continue
		}

		// 1. Real local art that is NOT our own placeholder (a
		//    successfully-downloaded cover, scanner sidecar/embedded art, or
		//    an admin upload) → keep it, mark done, touch no network — and
		//    it is the album's cover now if the album had none.
		if existing.real() {
			s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, false)
			applied += s.shareAlbumCover(ctx, target.trackID, album)
			continue
		}

		// 2. A URL-only cover left behind by an earlier pass: the local
		//    download failed at the time but the external URL may still render
		//    (via redirect). VERIFY it before doing anything — a live one is
		//    adopted locally (same-origin) and finished; a genuinely dead one
		//    falls through to the chain.
		if existing.overrideURL != "" && existing.localPath == "" {
			if s.verifyCoverURL(ctx, existing.overrideURL) {
				if err := s.applyTrackCover(ctx, target.trackID, catalog.Image{URL: existing.overrideURL}); err != nil {
					s.logger("explo: re-adopt cover failed for track %s: %v", target.trackID, err)
					s.setTrackCoverStatus(ctx, target.trackID, coverStatusPending, true)
					continue
				}
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, true)
				applied++
				applied += s.shareAlbumCover(ctx, target.trackID, album)
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

		// 2.75 Another drop identified as the same album already has its
		//      art: share it, with no network at all (see albumCover).
		if hasAlbumArt && s.adoptCover(ctx, target.trackID, existing, albumArt, true) {
			applied++
			continue
		}

		// 2.9 A track of an album requested whole from Deezer wears Deezer's
		//     cover of it, the one Search for new showed beside the album. An
		//     album only Deezer lists has no release group for the chain below
		//     to look art up by: on 2026-10-03 Czarface's "Czarface Meets
		//     Frankie Pulitzer" sat at "Fetching cover art" for every track
		//     while its cover showed on the search page.
		if url := requestedDeezerCoverURL(album.requested); url != "" && s.verifyCoverURL(ctx, url) {
			if err := s.applyTrackCover(ctx, target.trackID, catalog.Image{URL: url}); err == nil {
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, true)
				applied++
				applied += s.shareAlbumCover(ctx, target.trackID, album)
				continue
			}
		}

		// 3. Try the source chain for real art. The first track of an album
		//    to get through gives it to the rest at once.
		if url := s.resolveCoverURL(ctx, target); url != "" {
			if err := s.applyTrackCover(ctx, target.trackID, catalog.Image{URL: url}); err != nil {
				s.logger("explo: apply cover failed for track %s: %v", target.trackID, err)
				s.setTrackCoverStatus(ctx, target.trackID, coverStatusPending, true)
				continue
			}
			s.setTrackCoverStatus(ctx, target.trackID, coverStatusDone, true)
			applied++
			applied += s.shareAlbumCover(ctx, target.trackID, album)
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
		       MAX(et.matched_album),
		       MAX(et.matched_title),
		       MAX(et.cover_status)
		FROM explo_tracks et
		JOIN music_tracks mt ON mt.id = et.track_id
		JOIN media_files mf ON mf.track_id = mt.id
		WHERE et.cover_status IN ('', '%s', '%s')
		  AND et.status IN ('matched', 'matched-fallback', 'unmatched', 'error')
		  AND et.cover_attempts < %d
		  AND (et.cover_attempted_at = '' OR %s)
		  AND %s
		GROUP BY et.track_id
		ORDER BY EXISTS (
		  SELECT 1 FROM explo_requests er WHERE er.track_id = et.track_id AND er.state = 'identifying'
		) DESC, et.track_id`,
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

	// Prefer the per-track ledger. Older rows may have the identified album
	// only in an override; never use the scanner's unverified album tag.
	for index := range targets {
		if title := s.overriddenAlbumTitle(ctx, targets[index].albumID); targets[index].album == "" && title != "" && title != "Unknown Album" {
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

// An album wears one cover. Covers are resolved per track (see the state
// machine above), but tracks identified as one album — above all the tracks of
// an album requested whole — are not strangers: the first real cover any of
// them lands is the album's cover. Every other track of the album with no real
// art takes it that moment (shareAlbumCover), and a track that comes up for a
// pass later takes it before asking any source (albumCover), so the source
// chain runs per track only until one track of the album gets through.
//
// Before this, each track fetched its own copy of the same cover and only
// looked at its siblings on its own next try. A source that refused some of
// those fetches — Deezer's image CDN refuses the VPN in fits — left those
// tracks on placeholders for the length of their backoff, and a request kept
// after its hour carried the placeholder into the library: real art on one
// track of Puer Aeternus, a gradient tile on the next.
//
// "Album" is never the path-derived album the scanner groups a drop folder
// into (the reason covers are per track at all). It is the album a track was
// requested as part of, else the album identification put it on: the same
// release group, or the same album and artist names.
type coverAlbum struct {
	// requested is the album id of the whole-album request a drop was
	// downloaded for, once identification credited the drop to that album.
	requested string
	// The identified album, for any other drop.
	releaseGroupID, artist, title string
}

// albumTrack is another track of a cover album: a drop, with its cover
// status, or the library copy of a requested track, with none.
type albumTrack struct {
	id          string
	coverStatus string
}

// requestedAlbumTrackSQL holds, over explo_requests er and the drop's ledger
// row et, when a drop is the track of the album it was requested with:
// identified, and credited to that album as pinRequestedAlbum, pinLedgerAlbum
// and adoptCatalogIdentity credit it. A wrong download keeps its real
// identity, so it never takes the cover of the album it is not on.
const requestedAlbumTrackSQL = `et.status IN ('matched', 'matched-fallback')
	AND (lower(et.matched_album) = lower(er.album)
	  OR (et.musicbrainz_release_group_id <> '' AND et.musicbrainz_release_group_id = er.album_id))`

// coverAlbumOf is the album trackID shares its cover with, given the
// identified album on its ledger row.
func (s *Service) coverAlbumOf(ctx context.Context, trackID, releaseGroupID, artist, album string) coverAlbum {
	var requested string
	err := s.db.QueryRowContext(ctx, `
		SELECT er.album_id FROM explo_requests er JOIN explo_tracks et ON et.track_id = er.track_id
		WHERE er.track_id = ? AND er.album_id <> '' AND `+requestedAlbumTrackSQL+`
		ORDER BY er.updated_at DESC LIMIT 1`, trackID).Scan(&requested)
	switch {
	case err == nil:
		return coverAlbum{requested: requested}
	case err != sql.ErrNoRows:
		s.logger("explo: requested album lookup failed for track %s: %v", trackID, err)
	}
	releaseGroupID, artist, album = strings.TrimSpace(releaseGroupID), strings.TrimSpace(artist), strings.TrimSpace(album)
	if musicrelease.CompilationTitle(album) {
		album = ""
	}
	return coverAlbum{releaseGroupID: releaseGroupID, artist: artist, title: album}
}

// albumTracks lists the album's tracks other than trackID: first the drops
// whose cover pass finished, in the order they finished, then the rest. For a
// requested album that includes the library copies of its kept tracks — a
// copy kept before the album had a cover has none, and nothing else will ever
// give it one — but never a track the library already had, which keeps the
// cover it came with.
func (s *Service) albumTracks(ctx context.Context, album coverAlbum, trackID string) []albumTrack {
	var rows *sql.Rows
	var err error
	switch {
	case album.requested != "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, status FROM (
			  SELECT er.track_id AS id, et.cover_status AS status, et.cover_attempted_at AS at
			  FROM explo_requests er JOIN explo_tracks et ON et.track_id = er.track_id
			  WHERE er.album_id = ? AND `+requestedAlbumTrackSQL+`
			  UNION ALL
			  SELECT library_track_id, '', '' FROM explo_requests
			  WHERE album_id = ? AND track_id <> '' AND library_track_id <> ''
			) tracks
			WHERE id <> ?
			ORDER BY status = ? DESC, at, id`,
			album.requested, album.requested, trackID, coverStatusDone)
	case album.releaseGroupID != "" || (album.title != "" && album.artist != ""):
		rows, err = s.db.QueryContext(ctx, `
			SELECT et.track_id, et.cover_status FROM explo_tracks et
			WHERE et.track_id <> ?
			  AND ((? <> '' AND et.musicbrainz_release_group_id = ?)
			    OR (? <> '' AND ? <> '' AND lower(et.matched_album) = lower(?) AND lower(et.matched_artist) = lower(?)))
			ORDER BY et.cover_status = ? DESC, et.cover_attempted_at, et.track_id`,
			trackID, album.releaseGroupID, album.releaseGroupID,
			album.title, album.artist, album.title, album.artist, coverStatusDone)
	default:
		return nil
	}
	if err != nil {
		s.logger("explo: album tracks lookup failed for track %s: %v", trackID, err)
		return nil
	}
	defer rows.Close()
	var tracks []albumTrack
	seen := map[string]bool{}
	for rows.Next() {
		var track albumTrack
		if rows.Scan(&track.id, &track.coverStatus) != nil || seen[track.id] {
			continue // a drop and its kept copy can be one track
		}
		seen[track.id] = true
		tracks = append(tracks, track)
	}
	return tracks
}

// albumCover is the cover trackID's album already wears: the real art of the
// first of its other tracks to have any.
func (s *Service) albumCover(ctx context.Context, album coverAlbum, trackID string) (trackCoverState, bool) {
	for _, track := range s.albumTracks(ctx, album, trackID) {
		if track.coverStatus == coverStatusPending || track.coverStatus == coverStatusPlaceholder {
			continue // still waiting on the sources itself
		}
		if cover := s.existingTrackCover(ctx, track.id); cover.real() {
			return cover, true
		}
	}
	return trackCoverState{}, false
}

// shareAlbumCover gives the real cover trackID wears to every other track of
// its album that has none — drops waiting on the sources or wearing a
// placeholder, and library copies kept without art — and reports how many
// took it. Art a track already has is never replaced here.
func (s *Service) shareAlbumCover(ctx context.Context, trackID string, album coverAlbum) int {
	cover := s.existingTrackCover(ctx, trackID)
	if !cover.real() {
		return 0
	}
	shared := 0
	for _, track := range s.albumTracks(ctx, album, trackID) {
		if track.coverStatus == coverStatusDone {
			continue
		}
		existing := s.existingTrackCover(ctx, track.id)
		if existing.real() {
			continue
		}
		if s.adoptCover(ctx, track.id, existing, cover, false) {
			shared++
		}
	}
	if shared > 0 {
		s.logger("explo: track %s's cover given to %d other track(s) of its album", trackID, shared)
	}
	return shared
}

// adoptCover makes trackID wear cover — another track's real art, already on
// disk, so no network — and finishes its cover pass. Reports whether it now
// wears it.
func (s *Service) adoptCover(ctx context.Context, trackID string, existing, cover trackCoverState, bumpAttempts bool) bool {
	if existing.localPath != cover.localPath {
		if err := s.applyTrackCover(ctx, trackID, cover.image); err != nil {
			s.logger("explo: share album cover with track %s failed: %v", trackID, err)
			return false
		}
	}
	s.setTrackCoverStatus(ctx, trackID, coverStatusDone, bumpAttempts)
	return true
}

// adoptAlbumCover gives a drop the cover its album already wears (albumCover),
// and reports whether it now has real art.
func (s *Service) adoptAlbumCover(ctx context.Context, trackID string) bool {
	var releaseGroupID, artist, album string
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(musicbrainz_release_group_id, ''), COALESCE(matched_artist, ''), COALESCE(matched_album, '')
		FROM explo_tracks WHERE track_id = ?`, trackID).Scan(&releaseGroupID, &artist, &album); err != nil {
		return false
	}
	cover, ok := s.albumCover(ctx, s.coverAlbumOf(ctx, trackID, releaseGroupID, artist, album), trackID)
	return ok && s.adoptCover(ctx, trackID, s.existingTrackCover(ctx, trackID), cover, false)
}

// resolveCoverURL walks the source chain and returns the first URL whose
// image VERIFIABLY downloaded into the local cover store, or "" when every
// source missed. Order is trust-descending: CAA is keyed by the exact
// MusicBrainz identity that identified the audio, while iTunes/Deezer are
// text searches gated by a strict name match.
func (s *Service) resolveCoverURL(ctx context.Context, target coverTarget) string {
	releaseGroupID := strings.TrimSpace(target.releaseGroupID)
	if musicrelease.CompilationTitle(target.album) {
		releaseGroupID, target.album = "", ""
	}
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
			if target.album == "" {
				target.album = refs.ReleaseGroupTitle
			}
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
		} else if refs.ReleaseGroupID == releaseGroupID {
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

	// Song searches must agree with the resolved album as well as the song;
	// a song can also appear on compilations with unrelated artwork.
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
		return lookupITunesTrackCover(ctx, s.httpClient, target.artist, target.title, target.album)
	}); url != "" {
		return url
	}
	return s.verifiedTextSearchCover(ctx, target.trackID, "deezer track", func(ctx context.Context) (string, error) {
		s.deezerPacer.wait(ctx, deezerMinInterval)
		return lookupDeezerTrackCover(ctx, s.httpClient, target.artist, target.title, target.album)
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

// applyTrackCover persists a VERIFIED cover as the track's override through
// the normal apply pipeline (which resolves a URL from the cover store's cache
// to a local path, so it serves same-origin; a cover shared from another track
// arrives with its local path already). Applied to the TRACK, not its album,
// so each explo drop shows its own art.
func (s *Service) applyTrackCover(ctx context.Context, trackID string, cover catalog.Image) error {
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.metadataApply.Apply(ctx, metadata.MetadataApplyRequest{
			TargetKind: string(metadata.ApplyTargetMusicTrack),
			TargetID:   trackID,
			// ID satisfies the apply validation (needs a Title or ID); only the
			// "cover" field is applied, so nothing else on the track moves.
			Candidate:          metadata.SearchResult{Provider: "explo", MediaType: "recording", ID: trackID, Cover: &cover},
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
	// image is the cover entry whose file is localPath, with the URL it came
	// from when it came from one: what another track of the album is given
	// when it shares this cover.
	image catalog.Image
}

// real reports whether the track wears real art: a file on disk that is not
// its own generated placeholder.
func (c trackCoverState) real() bool {
	return c.localPath != "" && !c.isOwnPlaceholder
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
				state.overrideURL, state.image = firstImageURLAndImageOnDisk(string(coverRaw))
				state.localPath = state.image.Path
				if state.localPath != "" {
					state.isOwnPlaceholder = s.isPlaceholderCoverPath(ctx, trackID, state.localPath)
				}
				return state // override is authoritative
			}
		}
	}

	var imagesJSON string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(images_json, '') FROM music_tracks WHERE id = ?`, trackID).Scan(&imagesJSON); err == nil {
		_, state.image = firstImageURLAndImageOnDisk(imagesJSON)
		state.localPath = state.image.Path
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

// firstImageURLAndImageOnDisk decodes a JSON image list (or single image) and
// returns the first non-empty external URL and the first image whose local
// path exists as a non-empty file. Either may be empty.
func firstImageURLAndImageOnDisk(rawJSON string) (url string, onDisk catalog.Image) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return "", catalog.Image{}
	}
	var images []catalog.Image
	if err := json.Unmarshal([]byte(rawJSON), &images); err != nil {
		var single catalog.Image
		if json.Unmarshal([]byte(rawJSON), &single) != nil {
			return "", catalog.Image{}
		}
		images = []catalog.Image{single}
	}
	for _, image := range images {
		if url == "" {
			if u := strings.TrimSpace(image.URL); u != "" {
				url = u
			}
		}
		if onDisk.Path == "" {
			if p := strings.TrimSpace(image.Path); p != "" {
				if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
					onDisk = image
					onDisk.Path = p
				}
			}
		}
	}
	return url, onDisk
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

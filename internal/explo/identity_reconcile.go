package explo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/metadata"
)

// identityCheckBatch caps how many contradicted rows one identity check
// re-identifies. Each costs a fingerprint and a throttled AcoustID call, so
// this bounds the pass at a few minutes; the rest wait for the next boot or
// the next Reprocess, which re-arms the check.
const identityCheckBatch = 50

// identityReport is what one identity check found and did.
type identityReport struct {
	// Checked is how many matched rows contradicted their own file and were
	// re-identified.
	Checked int
	// Changed is how many of those now carry a different identity.
	Changed int
	Changes []identityChange
}

// identityChange is one row whose re-identification came out different.
type identityChange struct {
	TrackID string
	Path    string
	Before  identifiedTrack
	After   identifiedTrack
	// Applied is false on a dry run, and when the catalog shows the track
	// under a title or artist the pipeline never wrote — someone corrected it
	// by hand, and that correction is not overwritten; only the ledger moves.
	Applied bool
	Note    string
}

// ledgerIdentity is a matched row as the ledger recorded it, with the file's
// own evidence beside it.
type ledgerIdentity struct {
	candidate candidateTrack
	status    string
	match     identifiedTrack
}

// identityAgrees reports whether an identification names the file the way
// the file names itself: where its tags (or, failing those, its filename)
// give a title, an artist or an album, the match's must at least overlap it.
// A file that says nothing about itself agrees with anything, and a match
// with no album yet cannot disagree about one.
func identityAgrees(match identifiedTrack, evidence identityEvidence) bool {
	title, artist := evidence.names()
	if len(identityTokens(title)) > 0 && tokenAgreement(title, match.Title) == 0 {
		return false
	}
	if len(identityTokens(artist)) > 0 && tokenAgreement(artist, match.Artist) == 0 {
		return false
	}
	if album := strings.TrimSpace(evidence.Album); len(identityTokens(album)) > 0 && strings.TrimSpace(match.Album) != "" && tokenAgreement(album, match.Album) == 0 {
		return false
	}
	return true
}

// identityContradicted reports whether a ledger row is worth re-identifying:
// its identity disagrees with the file's tags or filename (the album
// included: a track the ledger puts on a sampler while its tag names the
// record is filed by Keep under the sampler), or the file carries a
// MusicBrainz recording id and the ledger settled on a different recording.
// The last case is not always wrong — MusicBrainz holds duplicate recordings
// of most hits — but the file's own recording is the better answer whenever
// AcoustID lists it, and the check costs one lookup. A sharer's sampler tag
// contradicts a correct identity too; that row is re-checked once per boot,
// comes out the same, and changes nothing.
func identityContradicted(row ledgerIdentity) bool {
	evidence := row.candidate.evidence()
	if !identityAgrees(row.match, evidence) {
		return true
	}
	embedded := strings.TrimSpace(evidence.MusicBrainzRecordingID)
	return embedded != "" && !strings.EqualFold(embedded, strings.TrimSpace(row.match.MusicBrainzRecordingID))
}

// identityDiffers reports whether two identifications would put a different
// name, record or recording on the track.
func identityDiffers(a, b identifiedTrack) bool {
	eq := func(x, y string) bool { return strings.TrimSpace(x) == strings.TrimSpace(y) }
	return !eq(a.MusicBrainzRecordingID, b.MusicBrainzRecordingID) ||
		!eq(a.Title, b.Title) || !eq(a.Artist, b.Artist) || !eq(a.Album, b.Album)
}

// reconcileIdentities re-identifies the matched rows whose recorded identity
// contradicts the file itself, and moves the ones that come out different.
//
// The ledger holds what identification reported on the day, and a wrong
// recording choice used to be permanent: matched rows never re-run, so
// "Creep" stayed filed under a one-source mis-tag through every pass until
// the file rotated out — and had already been kept under that name. This
// pass is the other half of choosing recordings by their evidence: the same
// choice, applied to what was chosen before the evidence counted.
//
// Only rows that contradict their own file are touched, and only through the
// ordinary identify path (fingerprint, AcoustID, the text-search fallback),
// so a row that comes out the same costs one lookup and changes nothing. A
// row the identifiers no longer recognise at all keeps its identity: a miss
// today is not evidence that the match was wrong. dryRun runs the lookups
// and reports, writing nothing.
func (s *Service) reconcileIdentities(ctx context.Context, dryRun bool) (identityReport, error) {
	report := identityReport{}
	rows, err := s.contradictedIdentities(ctx)
	if err != nil {
		return report, err
	}
	if len(rows) > identityCheckBatch {
		s.logger("explo: identity check: %d row(s) contradict their files; checking %d this pass", len(rows), identityCheckBatch)
		rows = rows[:identityCheckBatch]
	}
	for _, row := range rows {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		report.Checked++
		match, matched, err := s.identifyWithFallback(ctx, row.candidate)
		if err != nil {
			s.logger("explo: identity check: re-identify failed for %q: %v", row.candidate.path, err)
			continue
		}
		if !matched {
			s.logger("explo: identity check: %q no longer identifies; keeping %s / %s", row.candidate.path, row.match.Artist, row.match.Title)
			continue
		}
		if !identityDiffers(row.match, match) {
			continue
		}
		change := identityChange{
			TrackID: row.candidate.trackID,
			Path:    row.candidate.path,
			Before:  row.match,
			After:   match,
		}
		if dryRun {
			change.Note = "dry run"
		} else {
			applied, note, err := s.applyIdentityChange(ctx, row, match)
			if err != nil {
				s.logger("explo: identity check: could not move %q to %s / %s: %v", row.candidate.path, match.Artist, match.Title, err)
				continue
			}
			change.Applied = applied
			change.Note = note
		}
		report.Changed++
		report.Changes = append(report.Changes, change)
		s.logger("explo: identity check: %q: %s / %s [%s] -> %s / %s [%s]%s",
			row.candidate.path, change.Before.Artist, change.Before.Title, change.Before.Album,
			change.After.Artist, change.After.Title, change.After.Album, noteSuffix(change))
	}
	if report.Checked > 0 {
		s.logger("explo: identity check: %d row(s) re-identified, %d changed", report.Checked, report.Changed)
	}
	return report, nil
}

func noteSuffix(change identityChange) string {
	if change.Note == "" {
		return ""
	}
	return " (" + change.Note + ")"
}

// applyIdentityChange writes a re-identification the way the identify loop
// writes a first one — overrides, ledger, cover state — unless the catalog
// shows the track under a name the pipeline never gave it, in which case
// somebody corrected it by hand and only the ledger is brought up to date.
// Returns whether the overrides were written and a note for the report.
func (s *Service) applyIdentityChange(ctx context.Context, row ledgerIdentity, match identifiedTrack) (bool, string, error) {
	if s.identityEditedByHand(row) {
		if err := s.recordProcessed(ctx, row.candidate.trackID, matchStatus(match), match, ""); err != nil {
			return false, "", err
		}
		return false, "kept manual edit; ledger updated", nil
	}
	if err := s.applyMatch(ctx, row.candidate.trackID, row.candidate.albumID, match); err != nil {
		return false, "", err
	}
	if err := s.recordProcessed(ctx, row.candidate.trackID, matchStatus(match), match, ""); err != nil {
		return false, "", err
	}
	// The cover on file was fetched for the OLD record. The cover engine keeps
	// any real art it finds, so it has to be taken off the track before its
	// state is re-opened, or the wrong album's picture would be kept forever.
	if err := catalogstore.ClearMetadataOverrideFields(ctx, s.db, string(metadata.ApplyTargetMusicTrack), row.candidate.trackID, []string{"cover"}); err != nil && !errors.Is(err, catalog.ErrMetadataOverrideNotFound) {
		s.logger("explo: identity check: could not clear the old cover for %s: %v", row.candidate.trackID, err)
	}
	if err := s.resetTrackCoverState(ctx, row.candidate.trackID); err != nil {
		s.logger("explo: identity check: reset cover state failed for %s: %v", row.candidate.trackID, err)
	}
	return true, "", nil
}

// identityEditedByHand reports whether the catalog currently shows the track
// under a title or artist other than what the ledger says the pipeline
// applied. Without a catalog lookup there is nothing to compare, and the
// pipeline's own write is assumed current.
func (s *Service) identityEditedByHand(row ledgerIdentity) bool {
	if s.trackByID == nil {
		return false
	}
	current, err := s.trackByID(row.candidate.trackID)
	if err != nil {
		return false
	}
	return strings.TrimSpace(current.Title) != strings.TrimSpace(row.match.Title) ||
		strings.TrimSpace(current.DisplayArtist) != strings.TrimSpace(row.match.Artist)
}

func matchStatus(match identifiedTrack) string {
	if match.Source == "acoustid" {
		return "matched"
	}
	return "matched-fallback"
}

// contradictedIdentities lists the matched rows under the configured folders
// whose ledger identity contradicts their own file, oldest first.
func (s *Service) contradictedIdentities(ctx context.Context) ([]ledgerIdentity, error) {
	dirs := s.effectiveDirs()
	if len(dirs) == 0 {
		return nil, nil
	}
	clause, args := exploPathClause(dirs)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT mt.id, COALESCE(mt.album_id, ''), mf.path, COALESCE(mt.title, ''), COALESCE(mt.display_artist, ''),
		       mt.duration_seconds, COALESCE(mt.external_ids_json, ''), COALESCE(mt.album_title, ''),
		       et.status, et.acoustid_id, et.musicbrainz_recording_id, et.musicbrainz_release_group_id,
		       et.matched_title, et.matched_artist, et.matched_album, et.score
		FROM explo_tracks et
		JOIN music_tracks mt ON mt.id = et.track_id
		JOIN media_files mf ON mf.track_id = mt.id
		WHERE et.status IN ('matched', 'matched-fallback') AND %s
		ORDER BY et.processed_at, et.track_id`, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("explo identity check: %w", err)
	}
	defer rows.Close()

	var out []ledgerIdentity
	for rows.Next() {
		var row ledgerIdentity
		var externalIDs string
		if err := rows.Scan(
			&row.candidate.trackID, &row.candidate.albumID, &row.candidate.path, &row.candidate.title, &row.candidate.artist,
			&row.candidate.durationSeconds, &externalIDs, &row.candidate.album,
			&row.status, &row.match.AcoustID, &row.match.MusicBrainzRecordingID, &row.match.MusicBrainzReleaseGroupID,
			&row.match.Title, &row.match.Artist, &row.match.Album, &row.match.Score,
		); err != nil {
			return nil, err
		}
		row.candidate.musicBrainzRecordingID = embeddedRecordingID(externalIDs)
		if s.isDropFolderName(row.candidate.album) {
			row.candidate.album = ""
		}
		if row.status == "matched" {
			row.match.Source = "acoustid"
		} else {
			row.match.Source = "musicbrainz-search"
		}
		if identityContradicted(row) {
			out = append(out, row)
		}
	}
	return out, rows.Err()
}

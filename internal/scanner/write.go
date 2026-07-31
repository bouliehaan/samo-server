package scanner

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/storage"
)

func (s *Scanner) upsertMusicArtist(ctx context.Context, artist catalog.MusicArtist) error {
	if s.overrideIndex != nil {
		var err error
		artist, err = catalogstore.GuardMusicArtist(ctx, s.db, s.overrideIndex, artist)
		if err != nil {
			return err
		}
	}
	created, err := s.store.UpsertMusicArtist(ctx, artist)
	if err != nil {
		return err
	}
	if s.activeScan != nil && created {
		s.activeScan.noteNewArtist(artist.ID)
	}
	return nil
}

func (s *Scanner) upsertMusicAlbum(ctx context.Context, album catalog.MusicAlbum) error {
	if s.overrideIndex != nil {
		var err error
		album, err = catalogstore.GuardMusicAlbum(ctx, s.db, s.overrideIndex, album)
		if err != nil {
			return err
		}
	}
	return s.store.UpsertMusicAlbum(ctx, album)
}

func (s *Scanner) loadAlbumArtistNamesForAlbum(ctx context.Context, albumID string) []string {
	albumID = strings.TrimSpace(albumID)
	if albumID == "" || s.db == nil {
		return nil
	}
	// Best effort: these names only improve the album-artist inference for the
	// track being read. A read failure means we fall back to the tags, which is
	// what an album with no credits yet would do anyway.
	names, err := s.store.AlbumArtistNames(ctx, albumID)
	if err != nil {
		return nil
	}
	return names
}

func (s *Scanner) setAlbumArtists(ctx context.Context, albumID string, artists []catalog.MusicArtist, replace bool) error {
	if s.overrideIndex != nil && s.overrideIndex.HasField(catalog.OverrideKindMusicAlbum, albumID, "artists") {
		return nil
	}
	if len(artists) == 0 {
		return nil
	}
	// replace is false when the artists were inferred rather than read from an
	// explicit album-artist tag. Existing credits then win: a compilation whose
	// first track happens to name one performer must not have the whole album
	// recredited to them.
	if !replace {
		count, err := s.store.CountAlbumArtists(ctx, albumID)
		if err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
	}
	return s.store.ReplaceAlbumArtists(ctx, albumID, artistIDs(artists))
}

func (s *Scanner) upsertMusicTrack(ctx context.Context, track catalog.MusicTrack) error {
	if s.overrideIndex != nil {
		var err error
		track, err = catalogstore.GuardMusicTrack(ctx, s.db, s.overrideIndex, track)
		if err != nil {
			return err
		}
	}
	return s.store.UpsertMusicTrack(ctx, track)
}

func (s *Scanner) setTrackArtists(ctx context.Context, trackID string, artists []catalog.MusicArtist) error {
	if s.overrideIndex != nil && s.overrideIndex.HasField(catalog.OverrideKindMusicTrack, trackID, "artists") {
		return nil
	}
	return s.store.ReplaceTrackArtists(ctx, trackID, artistIDs(artists))
}

func (s *Scanner) upsertAudiobook(ctx context.Context, item catalog.AudiobookItem) (string, error) {
	if s.overrideIndex != nil {
		var err error
		item, err = catalogstore.GuardAudiobook(ctx, s.db, s.overrideIndex, item)
		if err != nil {
			return "", err
		}
	}
	return s.store.UpsertAudiobook(ctx, item)
}

func (s *Scanner) upsertPodcast(ctx context.Context, item catalog.PodcastItem) error {
	if s.overrideIndex != nil {
		var err error
		item, err = catalogstore.GuardPodcast(ctx, s.db, s.overrideIndex, item)
		if err != nil {
			return err
		}
	}
	return s.store.UpsertPodcast(ctx, item)
}

func (s *Scanner) upsertContributor(ctx context.Context, contributor catalog.Contributor) error {
	return s.store.UpsertContributor(ctx, contributor)
}

// setAudiobookContributors replaces an audiobook's contributor list (authors +
// narrators in one slice, distinguished by role). It ALWAYS writes every
// contributor row before inserting the junction row that references it, which
// closes the foreign-key hole that appeared when narrators were linked without
// ever having been written.
func (s *Scanner) setAudiobookContributors(ctx context.Context, audiobookID string, contributors []catalog.ContributorRef) error {
	if s.overrideIndex != nil {
		if s.overrideIndex.HasField(catalog.OverrideKindAudiobook, audiobookID, "authors") ||
			s.overrideIndex.HasField(catalog.OverrideKindAudiobook, audiobookID, "narrators") {
			return nil
		}
	}
	if err := s.store.ClearAudiobookContributors(ctx, audiobookID); err != nil {
		return err
	}
	for _, ref := range contributors {
		if ref.ID == "" {
			continue
		}
		if err := s.store.UpsertContributor(ctx, catalog.Contributor{
			ID:       ref.ID,
			Name:     ref.Name,
			SortName: ref.SortName,
		}); err != nil {
			return err
		}
	}
	for index, ref := range contributors {
		if ref.ID == "" {
			continue
		}
		if err := s.store.LinkAudiobookContributor(ctx, audiobookID, ref.ID, ref.Role, index); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scanner) upsertSeries(ctx context.Context, series catalog.Series) error {
	return s.store.UpsertSeries(ctx, series)
}

func (s *Scanner) setAudiobookSeries(ctx context.Context, audiobookID string, series []catalog.SeriesRef) error {
	if s.overrideIndex != nil && s.overrideIndex.HasField(catalog.OverrideKindAudiobook, audiobookID, "series") {
		return nil
	}
	return s.store.ReplaceAudiobookSeries(ctx, audiobookID, series)
}

func (s *Scanner) upsertPodcastEpisode(ctx context.Context, episode catalog.PodcastEpisode) error {
	if s.overrideIndex != nil {
		var err error
		episode, err = catalogstore.GuardPodcastEpisode(ctx, s.db, s.overrideIndex, episode)
		if err != nil {
			return err
		}
	}
	return s.store.UpsertPodcastEpisode(ctx, episode)
}

// replaceAudiobookChapters rewrites the audiobook's chapter list. Audiobooks
// and podcast episodes have separate chapter tables so the two flows do not
// race each other.
func (s *Scanner) replaceAudiobookChapters(ctx context.Context, audiobookID string, chapters []catalog.AudioChapter) error {
	return s.store.ReplaceAudiobookChapters(ctx, audiobookID, identifyChapters("audiobook", audiobookID, chapters))
}

// identifyChapters assigns each chapter the id it will be stored under.
//
// The id is derived from the owner, the index, the title and the start offset,
// so re-deriving the same chapters yields the same ids and a rescan is a no-op
// rather than a churn of new rows.
func identifyChapters(kind, ownerID string, chapters []catalog.AudioChapter) []catalog.AudioChapter {
	identified := make([]catalog.AudioChapter, 0, len(chapters))
	for _, chapter := range chapters {
		chapter.ID = stableID("chapter", kind, ownerID, fmt.Sprint(chapter.Index), chapter.Title, fmt.Sprint(chapter.StartMs()))
		identified = append(identified, chapter)
	}
	return identified
}

// setAudiobookChapterProvenance records WHERE a book's chapters came from
// (embedded/cue/file/none, or "audnexus"), the ASIN they were resolved from
// when external, and when that sync happened. This is what makes a degraded
// book queryable instead of invisible — and the hook the metadata-apply +
// "refresh chapters" admin path will reuse. asin/syncedAt are empty/nil for
// file-derived chapters.
func (s *Scanner) setAudiobookChapterProvenance(ctx context.Context, audiobookID, source, asin string, syncedAt *time.Time) error {
	return s.store.SetAudiobookChapterProvenance(ctx, audiobookID, source, asin, syncedAt)
}

// setAudioChapterMetrics records the audio chapter analysis confidence (0..1)
// and the input signature the analysis ran on. The signature lets the analysis
// pass skip a book whose files (and analyzer version) are unchanged, so the
// expensive full-file decode happens once per file version rather than on every
// scan.
func (s *Scanner) setAudioChapterMetrics(ctx context.Context, audiobookID string, confidence float64, sig string) error {
	return s.store.SetAudioChapterMetrics(ctx, audiobookID, confidence, sig)
}

func (s *Scanner) replaceEpisodeChapters(ctx context.Context, episodeID string, chapters []catalog.AudioChapter) error {
	return s.store.ReplaceEpisodeChapters(ctx, episodeID, identifyChapters("episode", episodeID, chapters))
}

func (s *Scanner) upsertAudioFile(ctx context.Context, libraryID string, owner audioFileOwner, file catalog.AudioFile, trackPID, contentHash string) error {
	existingID, err := s.mediaFileIDByPath(ctx, file.Path)
	if err != nil {
		return fmt.Errorf("lookup media file by path %q: %w", file.Path, err)
	}
	var prior mediaFileOwnerSnapshot
	if existingID != "" {
		file.ID = existingID
		prior, err = s.mediaFileOwners(ctx, existingID)
		if err != nil {
			return fmt.Errorf("load media file owners for %q: %w", file.Path, err)
		}
	} else if strings.TrimSpace(file.ID) == "" {
		file.ID = stableID("file", file.Path)
	}
	file = finalizeAudioFile(file)

	err = s.store.UpsertMediaFile(ctx, libraryID, owner, file, fileInode(file.Path), trackPID, contentHash)
	if err != nil {
		// A UNIQUE violation here is on path, not id: some other row already
		// owns this path. Reclaim it rather than failing the file.
		if !storage.IsUniqueViolation(err) {
			return fmt.Errorf("upsert audio file %q: %w", file.Path, err)
		}
		if err := s.reclaimMediaFileByPath(ctx, libraryID, owner, file, trackPID, contentHash); err != nil {
			return fmt.Errorf("reclaim media file %q: %w", file.Path, err)
		}
	} else {
		s.noteOwnerChange(ctx, prior, owner, file.Path)
	}
	if s.activeScan != nil {
		s.activeScan.seeFile(file.Path)
	}
	return nil
}

func (s *Scanner) reclaimMediaFileByPath(ctx context.Context, libraryID string, owner audioFileOwner, file catalog.AudioFile, trackPID, contentHash string) error {
	existingID, err := s.mediaFileIDByPath(ctx, file.Path)
	if err != nil {
		return err
	}
	if existingID == "" {
		return sql.ErrNoRows
	}
	file.ID = existingID
	prior, err := s.mediaFileOwners(ctx, existingID)
	if err != nil {
		return err
	}
	file = finalizeAudioFile(file)
	if err := s.store.UpdateMediaFileByPath(ctx, libraryID, owner, file, fileInode(file.Path), trackPID, contentHash); err != nil {
		return err
	}
	s.noteOwnerChange(ctx, prior, owner, file.Path)
	return nil
}

// noteOwnerChange records what a successful media-file write displaced.
//
// Two things follow from a file changing hands. A track that moved id needs its
// migration remembered, so playlists referencing the old id can be rewritten at
// the end of the scan. And an owner left with no files at all is orphaned and
// should go. Neither is worth failing the file over — the row itself is
// correct — so a cleanup failure is logged and the scan continues.
func (s *Scanner) noteOwnerChange(ctx context.Context, prior, owner audioFileOwner, path string) {
	if prior.TrackID != "" && owner.TrackID != "" && prior.TrackID != owner.TrackID {
		s.noteTrackIDMigration(prior.TrackID, owner.TrackID)
	}
	if err := s.cleanupReplacedMediaOwners(ctx, prior, owner); err != nil {
		log.Printf("scanner: cleanup after media file %q: %v", path, err)
	}
}

func (s *Scanner) upsertGenre(ctx context.Context, kind string, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return s.store.UpsertGenre(ctx, kind, name)
}

// RefreshStats recomputes the aggregate count and duration columns that
// catalog reads project to clients (libraries.item_count, music_artists.album_count,
// music_artists.track_count, etc.). The scanner runs this at the tail of every
// scan; call it directly at startup to repair drifted counts on existing data
// (e.g. after migration 016 renamed shelf libraries, or after Cursor's
// refactor changed schemas before the scanner had a chance to re-run).
func (s *Scanner) RefreshStats(ctx context.Context) error {
	if err := s.refreshStats(ctx); err != nil {
		return err
	}
	return s.reconcileMediaFileOwners(ctx)
}

func (s *Scanner) refreshStats(ctx context.Context) error {
	return s.store.RefreshAggregateStats(ctx)
}

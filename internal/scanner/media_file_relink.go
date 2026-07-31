package scanner

import (
	"context"
	"log"
	"path/filepath"
	"strings"
)

// reconcileMediaFileTrackLinks reattaches music media_files rows to the
// music_tracks row implied by track_pid. Quick scans and track-id migrations
// can leave stale track_id values; catalog then serves tracks with no
// audioFiles and streaming returns "no audio files available".
func (s *Scanner) reconcileMediaFileTrackLinks(ctx context.Context) (int, error) {
	links, err := s.store.MediaFilesWithTrackPID(ctx)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, link := range links {
		want := stableID("track", link.LibraryID, link.TrackPID)
		// Nothing to do when the link is already right, and nothing safe to do
		// when the track it implies does not exist — repointing at a missing
		// track would only trade one broken link for another.
		if want == strings.TrimSpace(link.TrackID) || !s.trackIDExists(ctx, want) {
			continue
		}
		if err := s.store.SetMediaFileTrack(ctx, link.FileID, want); err != nil {
			return updated, err
		}
		if old := strings.TrimSpace(link.TrackID); old != "" && old != want {
			s.noteTrackIDMigration(old, want)
		}
		updated++
	}
	if updated > 0 {
		log.Printf("scanner: relinked track_id on %d media file(s)", updated)
	}
	return updated, nil
}

// reconcileLongformMediaOwners fixes audiobook_id / episode_id when the
// catalog row still exists but media_files point at a deleted owner id.
func (s *Scanner) reconcileLongformMediaOwners(ctx context.Context) (int, error) {
	updated := 0
	n, err := s.reconcileAudiobookMediaOwners(ctx)
	if err != nil {
		return updated, err
	}
	updated += n
	n, err = s.reconcilePodcastMediaOwners(ctx)
	if err != nil {
		return updated, err
	}
	updated += n
	if updated > 0 {
		log.Printf("scanner: relinked longform owner on %d media file(s)", updated)
	}
	return updated, nil
}

func (s *Scanner) reconcileAudiobookMediaOwners(ctx context.Context) (int, error) {
	orphans, err := s.store.OrphanAudiobookFiles(ctx)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, orphan := range orphans {
		want, ok := s.audiobookIDForMediaPath(ctx, orphan.LibraryID, orphan.LibraryRoot, orphan.Path)
		if !ok || want == orphan.AudiobookID {
			continue
		}
		if err := s.store.SetMediaFileAudiobook(ctx, orphan.FileID, want); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func (s *Scanner) audiobookIDForMediaPath(ctx context.Context, libraryID, libraryRoot, filePath string) (string, bool) {
	libraryRoot = strings.TrimSpace(libraryRoot)
	filePath = strings.TrimSpace(filePath)
	if libraryRoot == "" || filePath == "" {
		return "", false
	}
	groups := splitAudiobookGroups(groupAudiobooks(libraryRoot, []string{filePath}))
	if len(groups) == 0 {
		return "", false
	}
	want := stableID("audiobook", libraryID, groups[0].Root)
	return want, s.audiobookIDExists(ctx, want)
}

func (s *Scanner) audiobookIDExists(ctx context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	return s.store.AudiobookExists(ctx, id)
}

func (s *Scanner) reconcilePodcastMediaOwners(ctx context.Context) (int, error) {
	orphans, err := s.store.OrphanPodcastFiles(ctx)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, orphan := range orphans {
		wantEpisode, wantPodcast, ok := s.podcastOwnersForMediaPath(ctx, orphan.LibraryID, orphan.LibraryRoot, orphan.Path, orphan.RelativePath)
		if !ok {
			continue
		}
		if wantEpisode == orphan.EpisodeID && wantPodcast == orphan.PodcastID {
			continue
		}
		if err := s.store.SetMediaFilePodcastOwners(ctx, orphan.FileID, wantEpisode, wantPodcast); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func (s *Scanner) podcastOwnersForMediaPath(ctx context.Context, libraryID, libraryRoot, filePath, relPath string) (episodeID, podcastID string, ok bool) {
	libraryRoot = strings.TrimSpace(libraryRoot)
	filePath = strings.TrimSpace(filePath)
	if libraryRoot == "" || filePath == "" {
		return "", "", false
	}
	groups := groupPodcasts(libraryRoot, []string{filePath})
	if len(groups) == 0 {
		return "", "", false
	}
	podcastID = stableID("podcast", libraryID, groups[0].Root)
	if relPath == "" {
		relPath, _ = filepath.Rel(libraryRoot, filePath)
	}
	episodeID = stableID("episode", podcastID, relPath)
	if !s.episodeIDExists(ctx, episodeID) {
		return "", "", false
	}
	return episodeID, podcastID, true
}

func (s *Scanner) episodeIDExists(ctx context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	return s.store.PodcastEpisodeExists(ctx, id)
}

func (s *Scanner) reconcileMediaFileOwners(ctx context.Context) error {
	if _, err := s.reconcileMediaFileTrackLinks(ctx); err != nil {
		return err
	}
	if _, err := s.reconcileLongformMediaOwners(ctx); err != nil {
		return err
	}
	return nil
}

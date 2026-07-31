package scanner

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
)

// runPhaseCrossLibraryMoves searches other libraries for tracks still marked
// missing after within-library reconciliation (Navidrome phase 2 stage 2).
func (s *Scanner) runPhaseCrossLibraryMoves(ctx context.Context, libraries []Library) error {
	if len(libraries) <= 1 {
		return nil
	}
	missingFiles, err := s.store.MissingTrackedFiles(ctx)
	if err != nil {
		return err
	}

	matched := 0
	state := &scanState{}
	for _, missing := range missingFiles {
		found, err := s.findCrossLibraryMatch(ctx, missing)
		if err != nil {
			log.Printf("scanner: cross-library match for %q: %v", missing.Path, err)
			continue
		}
		if found.ID == "" {
			continue
		}
		if err := s.moveMatchedTrack(ctx, missing.LibraryID, found, missing); err != nil {
			log.Printf("scanner: cross-library move %q: %v", missing.Path, err)
			continue
		}
		matched++
		state.noteChange()
	}
	if matched > 0 {
		log.Printf("scanner: cross-library reconciled %d moved track(s)", matched)
	}
	return nil
}

func (s *Scanner) findCrossLibraryMatch(ctx context.Context, missing indexedMediaFile) (indexedMediaFile, error) {
	if mb := s.musicBrainzTrackID(ctx, missing.ID); mb != "" {
		match, ok, err := s.findRecentByMBZTrackID(ctx, missing, mb)
		if err != nil {
			return indexedMediaFile{}, err
		}
		if ok {
			return match, nil
		}
	}
	if missing.ContentHash != "" {
		match, ok, err := s.findRecentByContentHash(ctx, missing)
		if err != nil {
			return indexedMediaFile{}, err
		}
		if ok {
			return match, nil
		}
	}
	return s.findRecentByFileName(ctx, missing)
}

func (s *Scanner) musicBrainzTrackID(ctx context.Context, fileID string) string {
	tagsJSON, err := s.store.EmbeddedTagsJSON(ctx, fileID)
	if err != nil {
		return ""
	}
	var flat map[string]string
	if err := json.Unmarshal([]byte(tagsJSON), &flat); err != nil {
		return ""
	}
	tags := normalizeTags(flat)
	return firstTag(tags, "musicbrainz_trackid", "musicbrainz_recordingid")
}

func (s *Scanner) findRecentByMBZTrackID(ctx context.Context, missing indexedMediaFile, mbzID string) (indexedMediaFile, bool, error) {
	candidates, err := s.store.RecentByMusicBrainzID(ctx, missing.LibraryID, mbzID)
	if err != nil {
		return indexedMediaFile{}, false, err
	}
	return bestCandidate(candidates, missing)
}

func (s *Scanner) findRecentByContentHash(ctx context.Context, missing indexedMediaFile) (indexedMediaFile, bool, error) {
	candidates, err := s.store.RecentByContentHash(ctx, missing.LibraryID, missing.ContentHash)
	if err != nil {
		return indexedMediaFile{}, false, err
	}
	return bestCandidate(candidates, missing)
}

func (s *Scanner) findRecentByFileName(ctx context.Context, missing indexedMediaFile) (indexedMediaFile, error) {
	base := filepath.Base(missing.Path)
	if base == "" {
		return indexedMediaFile{}, nil
	}
	candidates, err := s.store.RecentByFileName(ctx, missing.LibraryID, base)
	if err != nil {
		return indexedMediaFile{}, err
	}
	match, ok, err := bestCandidate(candidates, missing)
	if err != nil || !ok {
		return indexedMediaFile{}, err
	}
	return match, nil
}

// bestCandidate picks the file a missing row most likely moved to.
//
// An exact content-hash match is accepted from any number of candidates. A mere
// same-filename match is accepted only when it is the *only* candidate — with
// several, "track01.mp3" says nothing about which one is the right track01.
func bestCandidate(candidates []indexedMediaFile, missing indexedMediaFile) (indexedMediaFile, bool, error) {
	for _, c := range candidates {
		if mediaFileEquals(missing, c) {
			return c, true, nil
		}
	}
	if len(candidates) == 1 && mediaFileEquivalent(missing, candidates[0]) {
		return candidates[0], true, nil
	}
	return indexedMediaFile{}, false, nil
}

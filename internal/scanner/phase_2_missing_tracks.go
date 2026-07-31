package scanner

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

type missingTrackGroup struct {
	libraryID string
	pid       string
	missing   []indexedMediaFile
	matched   []indexedMediaFile
}

// markUnseenMediaFilesMissing flags rows absent from the current walk so phase 2
// can pair them with newly indexed paths (Navidrome marks missing before phase 2).
func (s *Scanner) markUnseenMediaFilesMissing(ctx context.Context, libraryID string, seenPaths map[string]struct{}) (int, error) {
	seen := buildSeenPathSet(seenPaths)
	paths, err := s.store.PresentTrackedPaths(ctx, libraryID)
	if err != nil {
		return 0, err
	}

	var candidates []string
	for scanned, path := range paths {
		if pathSeen(seen, path) {
			continue
		}
		candidates = append(candidates, path)
		if scanned%5000 == 0 {
			log.Printf("scanner: missing-check library=%q scanned=%d candidates=%d", libraryID, scanned, len(candidates))
			if s.onActivity != nil {
				s.onActivity(fmt.Sprintf("checking moved files… scanned %d", scanned))
			}
		}
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	if s.onActivity != nil {
		s.onActivity(fmt.Sprintf("marking %d possibly-moved file(s)…", len(candidates)))
	}
	log.Printf("scanner: missing-check library=%q marking %d path(s) not in walk", libraryID, len(candidates))

	marked := 0
	const batchSize = 200
	for i := 0; i < len(candidates); i += batchSize {
		if err := ctx.Err(); err != nil {
			return marked, err
		}
		end := i + batchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		n, err := s.store.MarkMediaFilesMissing(ctx, libraryID, candidates[i:end])
		if err != nil {
			return marked, err
		}
		marked += n
		if end%1000 == 0 || end == len(candidates) {
			log.Printf("scanner: missing-check library=%q marked %d/%d", libraryID, end, len(candidates))
		}
	}
	if marked > 0 {
		log.Printf("scanner: marked %d missing media file(s) library=%q", marked, libraryID)
	}
	return marked, nil
}

// runPhaseMissingTracks reconciles moved files using persistent track IDs,
// mirroring Navidrome phase 2.
func (s *Scanner) runPhaseMissingTracks(ctx context.Context, library Library, state *scanState) error {
	if library.Kind != "music" && library.Kind != "mixed" {
		return nil
	}
	groups, err := s.loadMissingTrackGroups(ctx, library.ID)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	if s.onActivity != nil {
		s.onActivity(fmt.Sprintf("reconciling moved tracks in %q (%d groups)…", library.Name, len(groups)))
	}
	log.Printf("scanner: reconciling moved tracks library=%q groups=%d", library.Name, len(groups))

	matched := 0
	for index, group := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index > 0 && index%100 == 0 {
			log.Printf("scanner: reconcile library=%q progress %d/%d matched=%d", library.Name, index, len(groups), matched)
			if s.onActivity != nil {
				s.onActivity(fmt.Sprintf("reconciling moved tracks in %q… %d/%d", library.Name, index, len(groups)))
			}
		}
		n, err := s.processMissingTrackGroup(ctx, group, state)
		if err != nil {
			return err
		}
		matched += n
	}
	if matched > 0 {
		log.Printf("scanner: reconciled %d moved track(s) in library %q", matched, library.Name)
	}
	return nil
}

func (s *Scanner) loadMissingTrackGroups(ctx context.Context, libraryID string) ([]missingTrackGroup, error) {
	rows, err := s.store.TrackedFilesByPID(ctx, libraryID)
	if err != nil {
		return nil, err
	}

	var groups []missingTrackGroup
	var current *missingTrackGroup
	for _, row := range rows {
		if row.TrackPID == "" {
			continue
		}
		if current == nil || current.pid != row.TrackPID {
			if current != nil && len(current.missing) > 0 && len(current.matched) > 0 {
				groups = append(groups, *current)
			}
			current = &missingTrackGroup{libraryID: libraryID, pid: row.TrackPID}
		}
		if row.Missing {
			current.missing = append(current.missing, row)
		} else {
			current.matched = append(current.matched, row)
		}
	}
	if current != nil && len(current.missing) > 0 && len(current.matched) > 0 {
		groups = append(groups, *current)
	}
	return groups, nil
}

func (s *Scanner) processMissingTrackGroup(ctx context.Context, group missingTrackGroup, state *scanState) (int, error) {
	usedMatched := map[string]bool{}
	matched := 0

	for _, missing := range group.missing {
		var exact indexedMediaFile
		var equivalent indexedMediaFile
		for _, candidate := range group.matched {
			if usedMatched[candidate.ID] {
				continue
			}
			if mediaFileEquals(missing, candidate) {
				exact = candidate
				break
			}
			if mediaFileEquivalent(missing, candidate) {
				equivalent = candidate
			}
		}

		target := exact
		if target.ID == "" && len(group.missing) == 1 && len(group.matched) == 1 && !usedMatched[group.matched[0].ID] {
			target = group.matched[0]
		}
		if target.ID == "" && equivalent.ID != "" {
			target = equivalent
		}
		if target.ID == "" {
			continue
		}
		if err := s.moveMatchedTrack(ctx, group.libraryID, target, missing); err != nil {
			return matched, err
		}
		usedMatched[target.ID] = true
		matched++
		state.noteChange()
	}
	return matched, nil
}

// moveMatchedTrack keeps the missing row's id and track_id, moves it to the new
// path, and deletes the duplicate row created at the new location during phase 1.
func (s *Scanner) moveMatchedTrack(ctx context.Context, libraryID string, target, missing indexedMediaFile) error {
	if target.ID == missing.ID {
		return nil
	}

	fields, err := s.store.MovedFileFieldsFor(ctx, target.ID)
	if err != nil {
		return err
	}

	newLibraryID := strings.TrimSpace(target.LibraryID)
	if newLibraryID == "" {
		newLibraryID = libraryID
	}

	// Delete the duplicate row at the new path before moving the canonical row:
	// media_files.path is UNIQUE, so the move would collide with the row phase 1
	// created at the new location.
	if err := s.store.DeleteMediaFileInLibrary(ctx, target.ID, newLibraryID); err != nil {
		return err
	}
	if err := s.store.MoveMediaFile(ctx, missing.ID, newLibraryID, target.Path,
		filepath.Base(target.Path), fileInode(target.Path), fields); err != nil {
		return fmt.Errorf("move matched track %q -> %q: %w", missing.Path, target.Path, err)
	}

	if target.TrackID != "" && target.TrackID != missing.TrackID {
		s.noteTrackIDMigration(target.TrackID, missing.TrackID)
		s.store.DeleteMusicTrackIgnoringError(ctx, target.TrackID)
	}
	return nil
}

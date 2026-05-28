package scanner

import "context"

// runPhaseRefreshAlbums rebuilds album metadata from tracks (Navidrome phase 3).
func (s *Scanner) runPhaseRefreshAlbums(ctx context.Context, library Library, accumulator *scanAccumulator, state *scanState) error {
	switch library.Kind {
	case "music", "mixed":
		if len(accumulator.touchedAlbumIDs) > 0 {
			if err := s.refreshMusicAlbums(ctx, accumulator.touchedAlbumIDs); err != nil {
				return err
			}
		}
		state.noteChange()
	}
	return nil
}

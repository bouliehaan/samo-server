package scanner

import "context"

// runParallelRefreshAndPlaylists runs Navidrome phases 3 and 4.
// Album refresh and M3U import both write to SQLite; running them sequentially
// avoids SQLITE_BUSY flakes on a single shared database connection pool.
func (s *Scanner) runParallelRefreshAndPlaylists(
	ctx context.Context,
	library Library,
	root string,
	accumulator *scanAccumulator,
	state *scanState,
) error {
	if err := s.runPhaseRefreshAlbums(ctx, library, accumulator, state); err != nil {
		return err
	}
	return s.runPhasePlaylists(ctx, library, root, state)
}

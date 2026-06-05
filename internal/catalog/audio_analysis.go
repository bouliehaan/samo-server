package catalog

import (
	"context"
	"database/sql"
)

// AudiobookAudioFiles returns one audiobook's audio files in playback order with
// StartOffsetSeconds and DurationMs populated — the same book-global projection
// the API and player use. Audio chapter analysis needs the exact per-file
// timeline to map detected silences onto book-global positions, so it reuses
// this rather than re-deriving the sort/offset logic and risking divergence.
func AudiobookAudioFiles(ctx context.Context, db *sql.DB, audiobookID string) ([]AudioFile, error) {
	all, err := loadAudioFiles(ctx, db, "audiobook_id")
	if err != nil {
		return nil, err
	}
	return all[audiobookID], nil
}

// AudiobookStoredChapters returns one audiobook's persisted chapters (titles +
// times). Audio analysis uses these purely as the NAME source once the audio has
// decided where — and how many — chapters there are.
func AudiobookStoredChapters(ctx context.Context, db *sql.DB, audiobookID string) ([]AudioChapter, error) {
	all, err := loadAudiobookChapters(ctx, db)
	if err != nil {
		return nil, err
	}
	return all[audiobookID], nil
}

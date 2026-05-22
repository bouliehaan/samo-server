package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type targetSpec struct {
	table  string
	column string
}

var targetSpecs = map[TargetKind]targetSpec{
	TargetMusicArtist:   {table: "music_artists", column: "playback_json"},
	TargetMusicAlbum:    {table: "music_albums", column: "playback_json"},
	TargetMusicTrack:    {table: "music_tracks", column: "playback_json"},
	TargetMusicPlaylist: {table: "music_playlists", column: "playback_json"},
	TargetShelfItem:     {table: "shelf_items", column: "progress_json"},
	TargetShelfEpisode:  {table: "podcast_episodes", column: "progress_json"},
}

func specFor(kind TargetKind) (targetSpec, error) {
	spec, ok := targetSpecs[kind]
	if !ok {
		return targetSpec{}, ErrInvalidTarget
	}
	return spec, nil
}

func loadState(ctx context.Context, db *sql.DB, kind TargetKind, id string) (State, error) {
	spec, err := specFor(kind)
	if err != nil {
		return State{}, err
	}

	query := fmt.Sprintf(`SELECT %s FROM %s WHERE id = ?`, spec.column, spec.table)
	var raw string
	err = db.QueryRowContext(ctx, query, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return State{}, ErrNotFound
	}
	if err != nil {
		return State{}, fmt.Errorf("load playback state: %w", err)
	}

	state := State{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return State{}, fmt.Errorf("decode playback state: %w", err)
		}
	}
	return normalizeState(state), nil
}

func saveState(ctx context.Context, db *sql.DB, kind TargetKind, id string, state State) (State, error) {
	spec, err := specFor(kind)
	if err != nil {
		return State{}, err
	}
	if err := validateState(state); err != nil {
		return State{}, err
	}
	state = normalizeState(state)

	payload, err := json.Marshal(state)
	if err != nil {
		return State{}, fmt.Errorf("encode playback state: %w", err)
	}

	query := fmt.Sprintf(`UPDATE %s SET %s = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, spec.table, spec.column)
	result, err := db.ExecContext(ctx, query, string(payload), id)
	if err != nil {
		return State{}, fmt.Errorf("save playback state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return State{}, fmt.Errorf("save playback rows: %w", err)
	}
	if rows == 0 {
		return State{}, ErrNotFound
	}
	return state, nil
}

func applyPatch(current State, patch PatchInput) State {
	if patch.UserID != nil {
		current.UserID = strings.TrimSpace(*patch.UserID)
	}
	if patch.PlayCount != nil {
		current.PlayCount = *patch.PlayCount
	}
	if patch.SkipCount != nil {
		current.SkipCount = *patch.SkipCount
	}
	if patch.Rating != nil {
		current.Rating = *patch.Rating
	}
	if patch.Starred != nil {
		current.Starred = *patch.Starred
	}
	if patch.Favorite != nil {
		current.Favorite = *patch.Favorite
	}
	if patch.ProgressSeconds != nil {
		current.ProgressSeconds = *patch.ProgressSeconds
	}
	if patch.Completed != nil {
		current.Completed = *patch.Completed
	}
	if patch.LastPlayedAt != nil {
		current.LastPlayedAt = patch.LastPlayedAt
	}
	if patch.LastPositionAt != nil {
		current.LastPositionAt = patch.LastPositionAt
	}
	if patch.IncrementPlayCount {
		current.PlayCount++
	}
	if patch.IncrementSkipCount {
		current.SkipCount++
	}
	now := time.Now().UTC()
	if patch.TouchLastPlayedAt {
		current.LastPlayedAt = &now
	}
	if patch.TouchLastPositionAt {
		current.LastPositionAt = &now
	}
	return normalizeState(current)
}

func normalizeState(state State) catalog.PlaybackState {
	if state.PlayCount < 0 {
		state.PlayCount = 0
	}
	if state.SkipCount < 0 {
		state.SkipCount = 0
	}
	if state.ProgressSeconds < 0 {
		state.ProgressSeconds = 0
	}
	return state
}

func validateState(state State) error {
	if state.Rating < 0 || state.Rating > 5 {
		return fmt.Errorf("%w: rating must be between 0 and 5", ErrInvalidState)
	}
	if state.PlayCount < 0 || state.SkipCount < 0 || state.ProgressSeconds < 0 {
		return fmt.Errorf("%w: counts and progress cannot be negative", ErrInvalidState)
	}
	return nil
}

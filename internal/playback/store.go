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

var targetTables = map[TargetKind]string{
	TargetMusicArtist:   "music_artists",
	TargetMusicAlbum:    "music_albums",
	TargetMusicTrack:    "music_tracks",
	TargetMusicPlaylist: "music_playlists",
	TargetShelfItem:     "shelf_items",
	TargetShelfEpisode:  "podcast_episodes",
}

func tableFor(kind TargetKind) (string, error) {
	table, ok := targetTables[kind]
	if !ok {
		return "", ErrInvalidTarget
	}
	return table, nil
}

func targetExists(ctx context.Context, db *sql.DB, kind TargetKind, id string) error {
	table, err := tableFor(kind)
	if err != nil {
		return err
	}
	var found string
	err = db.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ?`, table), id).Scan(&found)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup playback target: %w", err)
	}
	return nil
}

func loadState(ctx context.Context, db *sql.DB, userID string, kind TargetKind, id string) (State, error) {
	if err := targetExists(ctx, db, kind, id); err != nil {
		return State{}, err
	}
	var raw string
	err := db.QueryRowContext(ctx, `
		SELECT state_json FROM user_playback
		WHERE user_id = ? AND target_kind = ? AND target_id = ?`,
		userID, string(kind), id,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return State{UserID: userID}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("load playback state: %w", err)
	}
	state := State{UserID: userID}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return State{}, fmt.Errorf("decode playback state: %w", err)
		}
	}
	state.UserID = userID
	return normalizeState(state), nil
}

func saveState(ctx context.Context, db *sql.DB, userID string, kind TargetKind, id string, state State) (State, error) {
	if err := targetExists(ctx, db, kind, id); err != nil {
		return State{}, err
	}
	if err := validateState(state); err != nil {
		return State{}, err
	}
	state = normalizeState(state)
	state.UserID = userID

	payload, err := json.Marshal(state)
	if err != nil {
		return State{}, fmt.Errorf("encode playback state: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO user_playback (user_id, target_kind, target_id, state_json, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, target_kind, target_id) DO UPDATE SET
			state_json = excluded.state_json,
			updated_at = excluded.updated_at`,
		userID, string(kind), id, string(payload), now,
	)
	if err != nil {
		return State{}, fmt.Errorf("save playback state: %w", err)
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

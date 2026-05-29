package playback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const listForUserByIDsChunkSize = 400

// ListForUserByIDs returns playback states for the given catalog entity IDs.
// Missing IDs are omitted from the map.
func (s *Service) ListForUserByIDs(
	ctx context.Context,
	userID string,
	kind TargetKind,
	ids []string,
) (map[string]State, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return make(map[string]State), nil
	}

	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return make(map[string]State), nil
	}

	out := make(map[string]State, len(unique))
	for start := 0; start < len(unique); start += listForUserByIDsChunkSize {
		end := start + listForUserByIDsChunkSize
		if end > len(unique) {
			end = len(unique)
		}
		chunk, err := s.listForUserByIDsChunk(ctx, userID, kind, unique[start:end])
		if err != nil {
			return nil, err
		}
		for id, state := range chunk {
			out[id] = state
		}
	}
	return out, nil
}

func (s *Service) listForUserByIDsChunk(
	ctx context.Context,
	userID string,
	kind TargetKind,
	ids []string,
) (map[string]State, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, userID, string(kind))
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT target_id, state_json
		FROM user_playback
		WHERE user_id = ? AND target_kind = ? AND target_id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list playback states by ids: %w", err)
	}
	defer rows.Close()

	out := map[string]State{}
	for rows.Next() {
		var targetID, raw string
		if err := rows.Scan(&targetID, &raw); err != nil {
			return nil, err
		}
		state := State{UserID: userID}
		if strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &state)
		}
		state.UserID = userID
		out[targetID] = normalizeState(state)
	}
	return out, rows.Err()
}

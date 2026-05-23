package playback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ListForUser returns playback states for one target kind keyed by catalog entity ID.
func (s *Service) ListForUser(ctx context.Context, userID string, kind TargetKind) (map[string]State, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return map[string]State{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT target_id, state_json
		FROM user_playback
		WHERE user_id = ? AND target_kind = ?`, userID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("list playback states: %w", err)
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

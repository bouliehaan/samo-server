package playback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AnyListenerByIDs returns the furthest-along playback state for each target,
// across every user on the server.
//
// Deliberately not scoped to a user, unlike ListForUserByIDs. The caller is the
// channel scheduler, and a channel has no user — it is a station the household
// tunes into. "Has anyone here already heard this" is the question worth asking
// before putting an episode on air; asking it per user would mean a channel
// re-airing something for a listener who happens not to be the one it is keyed
// to, which is the same bug from a different angle.
//
// Merging rule: completed if ANY listener completed it, progress is the largest
// any listener reached.
func (s *Service) AnyListenerByIDs(
	ctx context.Context,
	kind TargetKind,
	ids []string,
) (map[string]State, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
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
		return map[string]State{}, nil
	}

	out := make(map[string]State, len(unique))
	for start := 0; start < len(unique); start += listForUserByIDsChunkSize {
		end := start + listForUserByIDsChunkSize
		if end > len(unique) {
			end = len(unique)
		}
		if err := s.anyListenerChunk(ctx, kind, unique[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) anyListenerChunk(
	ctx context.Context,
	kind TargetKind,
	ids []string,
	out map[string]State,
) error {
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, string(kind))
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT target_id, state_json
		FROM user_playback
		WHERE target_kind = ? AND target_id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("list playback states across listeners: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var targetID, raw string
		if err := rows.Scan(&targetID, &raw); err != nil {
			return err
		}
		state := State{}
		if strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &state)
		}
		state = normalizeState(state)

		merged, exists := out[targetID]
		if !exists {
			out[targetID] = state
			continue
		}
		if state.Completed {
			merged.Completed = true
		}
		if state.ProgressSeconds > merged.ProgressSeconds {
			merged.ProgressSeconds = state.ProgressSeconds
		}
		out[targetID] = merged
	}
	return rows.Err()
}

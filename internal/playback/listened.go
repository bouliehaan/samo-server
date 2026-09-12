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
	others, _, err := s.AnyListenerByIDsApart(ctx, kind, ids, "")
	return others, err
}

// AnyListenerByIDsApart is AnyListenerByIDs with one account read separately.
//
// `others` merges every listener except apartUserID exactly as AnyListenerByIDs
// does; `apart` is that one account's own rows, unmerged. One query, because the
// two are asked together every time.
//
// The account set apart is the station's. The channel scheduler records its own
// airings under the reserved server user, so "has the radio already played this"
// and "has a person here already heard this" are both answerable from the same
// table — but they are different questions with different consequences, and
// merging the two into one row is what made the first look like the second: an
// episode the station had aired once, to whoever happened to be in the room,
// read as listened-to by a person and was never offered again, however many
// surfacings it was still owed. An empty apartUserID sets nobody apart.
func (s *Service) AnyListenerByIDsApart(
	ctx context.Context,
	kind TargetKind,
	ids []string,
	apartUserID string,
) (others, apart map[string]State, err error) {
	if s == nil || s.db == nil {
		return nil, nil, ErrDisabled
	}
	apartUserID = strings.TrimSpace(apartUserID)

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
	others, apart = map[string]State{}, map[string]State{}
	if len(unique) == 0 {
		return others, apart, nil
	}

	for start := 0; start < len(unique); start += listForUserByIDsChunkSize {
		end := start + listForUserByIDsChunkSize
		if end > len(unique) {
			end = len(unique)
		}
		if err := s.anyListenerChunk(ctx, kind, unique[start:end], apartUserID, others, apart); err != nil {
			return nil, nil, err
		}
	}
	return others, apart, nil
}

func (s *Service) anyListenerChunk(
	ctx context.Context,
	kind TargetKind,
	ids []string,
	apartUserID string,
	out map[string]State,
	apart map[string]State,
) error {
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, string(kind))
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT user_id, target_id, state_json
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
		var userID, targetID, raw string
		if err := rows.Scan(&userID, &targetID, &raw); err != nil {
			return err
		}
		state := State{}
		if strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &state)
		}
		state = normalizeState(state)

		if apartUserID != "" && userID == apartUserID {
			apart[targetID] = state
			continue
		}
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

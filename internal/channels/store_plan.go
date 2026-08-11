package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Persistence for the three things the programming engine needs to remember
// between decisions: the plan, where in the plan the station currently is, and
// why it made its recent choices.
//
// All three are additive. Nothing here rewrites a column the old scheduler
// used, which is what lets a channel with no plan keep running unchanged while
// the new model is being built out around it.

// ---- the plan ----------------------------------------------------------

// LoadPlan reads a channel's stored plan. ok=false means nobody has written
// one, and the caller should derive it from the channel's own configuration.
func LoadPlan(ctx context.Context, db *sql.DB, channelID string) (Plan, bool, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return Plan{}, false, ErrInvalidID
	}
	var raw string
	err := db.QueryRowContext(ctx,
		`SELECT plan_json FROM channel_programming_plan WHERE channel_id = ?`, channelID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, false, nil
	}
	if err != nil {
		return Plan{}, false, fmt.Errorf("load plan: %w", err)
	}
	plan, err := ParsePlan([]byte(raw))
	if err != nil {
		// A stored plan that no longer parses is worse than no plan: it would
		// take the station off the air. Say so loudly and let the caller fall
		// back to the derived one.
		return Plan{}, false, fmt.Errorf("stored plan for %s is not valid: %w", channelID, err)
	}
	return plan, true, nil
}

// SavePlan validates and stores a plan.
func SavePlan(ctx context.Context, db *sql.DB, channelID string, plan Plan) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ErrInvalidID
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if plan.Version == 0 {
		plan.Version = PlanVersion
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_programming_plan (channel_id, plan_json, version, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET
			plan_json = EXCLUDED.plan_json,
			version = EXCLUDED.version,
			updated_at = EXCLUDED.updated_at`,
		channelID, string(encoded), plan.Version, now,
	)
	if err != nil {
		return fmt.Errorf("save plan: %w", err)
	}
	return nil
}

// DeletePlan drops a stored plan, returning the channel to the plan its own
// sources and slots describe.
func DeletePlan(ctx context.Context, db *sql.DB, channelID string) error {
	if strings.TrimSpace(channelID) == "" {
		return ErrInvalidID
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM channel_programming_plan WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	return nil
}

// ---- where in the plan we are -----------------------------------------

// LoadProgramState reads which block the station is in.
func LoadProgramState(ctx context.Context, db *sql.DB, channelID string) (ProgramState, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ProgramState{}, ErrInvalidID
	}
	var blockID, enteredAt, stateJSON string
	var itemCount int
	err := db.QueryRowContext(ctx,
		`SELECT block_id, entered_at, item_count, state_json FROM channel_program_state WHERE channel_id = ?`,
		channelID).Scan(&blockID, &enteredAt, &itemCount, &stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return ProgramState{}, nil
	}
	if err != nil {
		return ProgramState{}, fmt.Errorf("load programme state: %w", err)
	}
	state := ProgramState{
		BlockID:   blockID,
		EnteredAt: parseStoredTime(enteredAt),
		ItemCount: itemCount,
	}
	// The JSON document is the whole state; the columns are readable copies.
	// A row written before the column existed simply has no cycle position and
	// no queue, which is the correct starting point anyway.
	if stateJSON != "" {
		var stored ProgramState
		if err := json.Unmarshal([]byte(stateJSON), &stored); err == nil {
			stored.BlockID = state.BlockID
			stored.EnteredAt = state.EnteredAt
			stored.ItemCount = state.ItemCount
			return stored, nil
		}
	}
	return state, nil
}

// SaveProgramState records which block the station is in.
//
// Persisted rather than derived because a restart in the middle of a sequence
// should resume it. Without this, every deploy would put the station back to
// the top of whatever block the clock happens to allow, which for a block that
// runs until its pool is exhausted means starting the morning again at 4pm.
func SaveProgramState(ctx context.Context, db *sql.DB, channelID string, state ProgramState) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ErrInvalidID
	}
	entered := ""
	if !state.EnteredAt.IsZero() {
		entered = state.EnteredAt.UTC().Format(time.RFC3339)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode programme state: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_program_state (channel_id, block_id, entered_at, item_count, state_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET
			block_id = EXCLUDED.block_id,
			entered_at = EXCLUDED.entered_at,
			item_count = EXCLUDED.item_count,
			state_json = EXCLUDED.state_json,
			updated_at = EXCLUDED.updated_at`,
		channelID, state.BlockID, entered, state.ItemCount, string(encoded),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save programme state: %w", err)
	}
	return nil
}

// ---- why it played what it played --------------------------------------

// decisionRetention is how many decisions are kept per channel.
//
// Bounded because this is diagnostics, not an archive: a station makes
// something like a decision a minute, and the question it answers — "why did it
// just play that" — has a shelf life of about a day.
const decisionRetention = 400

// SaveDecision records one choice and prunes the old ones.
func SaveDecision(ctx context.Context, db *sql.DB, channelID string, decision Decision) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ErrInvalidID
	}
	id, err := newID("cdec")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode decision: %w", err)
	}
	selected := ""
	if decision.Selected != nil {
		selected = decision.Selected.Ref
	}
	at := decision.At
	if at.IsZero() {
		at = time.Now()
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO channel_decisions (id, channel_id, decided_at, block_id, selected_ref, decision_json)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, channelID, at.UTC().Format(time.RFC3339), decision.BlockID, selected, string(encoded),
	); err != nil {
		return fmt.Errorf("save decision: %w", err)
	}

	// Keep the tail bounded. Deleting by id from a sub-select is portable
	// across the two engines this project has shipped on and does not depend on
	// a window function.
	if _, err := db.ExecContext(ctx, `
		DELETE FROM channel_decisions
		WHERE channel_id = ?
		  AND id NOT IN (
			SELECT id FROM channel_decisions
			WHERE channel_id = ?
			ORDER BY decided_at DESC
			LIMIT ?
		  )`, channelID, channelID, decisionRetention); err != nil {
		return fmt.Errorf("prune decisions: %w", err)
	}
	return nil
}

// RecentDecisions returns the channel's most recent decisions, newest first.
func RecentDecisions(ctx context.Context, db *sql.DB, channelID string, limit int) ([]Decision, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT decision_json FROM channel_decisions
		WHERE channel_id = ?
		ORDER BY decided_at DESC
		LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()
	out := make([]Decision, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		var decision Decision
		if err := json.Unmarshal([]byte(raw), &decision); err != nil {
			continue
		}
		out = append(out, decision)
	}
	return out, rows.Err()
}

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

// decisionRetention is how long decisions are kept.
//
// This record used to be bounded by count — a few hundred rows, on the theory
// that "why did it just play that" has a shelf life of about a day. It does
// not. The question arrives when somebody gets round to asking it, and on
// 2026-09-09 that was five days after the choice in question; the row had
// gone. Time is the budget the record is actually spent against, so time is
// what bounds it.
const decisionRetention = 7 * 24 * time.Hour

// decisionCeiling is the most rows a channel keeps however young they are.
//
// A backstop, not the budget. A station makes a decision every few minutes,
// which puts a week at a few thousand rows; the ceiling exists so that a
// writer gone wrong cannot grow the table without limit before its week is up,
// and sits far enough above anything the streamer's retry loop can produce
// that it never decides what an ordinary channel remembers.
const decisionCeiling = 5000

// decisionRepeatWindow is how soon the same choice, made again, is the previous
// decision repeated rather than a new one.
//
// A station keeps coming back for the same item when the item produces no
// audio: the streamer discards the play-log row, backs off (thirty seconds at
// most) and asks again, and the scheduler — its state rewound, the same slot
// still booked — answers the same. Recorded as a fresh row every time, three
// hours of an unreachable station wrote three hundred and fifty identical rows
// and pruned five days of history to make room for them (2026-09-09, "Lofi
// Weekday", 10:56Z to 14:00Z).
//
// Five minutes clears every retry cadence the streamer has — the backoff, the
// first-byte budget and the stall watchdog, in any combination — while a choice
// that comes round again after actually playing for a while stays the separate
// decision it is.
const decisionRepeatWindow = 5 * time.Minute

// SaveDecision records one choice and prunes the old ones.
//
// The same choice made again inside decisionRepeatWindow — the same block, the
// same selection, or the same way of selecting nothing — is folded into the row
// it repeats: that row moves to the front of the record, its account becomes
// the latest one, and it counts how many times it has been repeated and since
// when. A station retrying a dead source therefore cannot fill the record with
// one row repeated, and nothing about the outage is lost by that: one decision
// reading "retried 349 times since 10:56" says more than three hundred and
// fifty reading the same thing, and the latest account is the one that shows
// the skip rule being relaxed because nothing else could play.
func SaveDecision(ctx context.Context, db *sql.DB, channelID string, decision Decision) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ErrInvalidID
	}
	if decision.At.IsZero() {
		decision.At = time.Now()
	}
	decision.At = decision.At.UTC()
	// Never carried in from the caller: a run is something the store observes.
	decision.Retries = nil

	previous, found, err := latestDecision(ctx, db, channelID)
	if err != nil {
		return err
	}
	if found && decision.repeats(previous.decision) && withinRepeatWindow(previous.decidedAt, decision.At) {
		run := RetrySummary{Count: 1, Since: previous.decidedAt}
		if earlier := previous.decision.Retries; earlier != nil {
			run.Count = earlier.Count + 1
			if !earlier.Since.IsZero() {
				run.Since = earlier.Since
			}
		}
		decision.Retries = &run
		encoded, err := json.Marshal(decision)
		if err != nil {
			return fmt.Errorf("encode decision: %w", err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE channel_decisions SET decided_at = ?, decision_json = ? WHERE id = ?`,
			decision.At.Format(time.RFC3339), string(encoded), previous.id,
		); err != nil {
			return fmt.Errorf("save repeated decision: %w", err)
		}
		return nil
	}

	id, err := newID("cdec")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode decision: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO channel_decisions (id, channel_id, decided_at, block_id, selected_ref, decision_json)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, channelID, decision.At.Format(time.RFC3339), decision.BlockID, decision.selectedRef(), string(encoded),
	); err != nil {
		return fmt.Errorf("save decision: %w", err)
	}
	return pruneDecisions(ctx, db, channelID, decision.At)
}

// storedDecision is the newest row as the coalescing check needs it: the row
// to update, the time the column says it was decided (the sort key, which a
// row written before the record carried its own time may not have in its
// JSON), and the account itself.
type storedDecision struct {
	id        string
	decidedAt time.Time
	decision  Decision
}

// latestDecision reads the channel's newest decision, if it has one.
func latestDecision(ctx context.Context, db *sql.DB, channelID string) (storedDecision, bool, error) {
	var stored storedDecision
	var decidedAt, raw string
	err := db.QueryRowContext(ctx, `
		SELECT id, decided_at, decision_json FROM channel_decisions
		WHERE channel_id = ?
		ORDER BY decided_at DESC
		LIMIT 1`, channelID).Scan(&stored.id, &decidedAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return storedDecision{}, false, nil
	}
	if err != nil {
		return storedDecision{}, false, fmt.Errorf("read latest decision: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &stored.decision); err != nil {
		// A row that no longer parses cannot be the one being repeated.
		return storedDecision{}, false, nil
	}
	stored.decidedAt = parseStoredTime(decidedAt)
	return stored, true, nil
}

// withinRepeatWindow is whether a decision at `now` follows one at `previous`
// closely enough to be the same decision made again. A clock that has gone
// backwards is not a repeat of anything.
func withinRepeatWindow(previous, now time.Time) bool {
	if previous.IsZero() || now.Before(previous) {
		return false
	}
	return now.Sub(previous) <= decisionRepeatWindow
}

// pruneDecisions drops what is older than the retention and, should a channel
// have written more than the ceiling inside it, the oldest beyond that.
func pruneDecisions(ctx context.Context, db *sql.DB, channelID string, now time.Time) error {
	cutoff := now.Add(-decisionRetention).UTC().Format(time.RFC3339)
	// Deleting by id from a sub-select is portable across the two engines this
	// project has shipped on and does not depend on a window function.
	if _, err := db.ExecContext(ctx, `
		DELETE FROM channel_decisions
		WHERE channel_id = ?
		  AND (decided_at < ?
		    OR id NOT IN (
			SELECT id FROM channel_decisions
			WHERE channel_id = ?
			ORDER BY decided_at DESC
			LIMIT ?
		  ))`, channelID, cutoff, channelID, decisionCeiling); err != nil {
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

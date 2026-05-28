package channels

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound  = errors.New("channel resource not found")
	ErrInvalidID = errors.New("invalid identifier")
)

// parseStoredTime accepts both RFC3339 (what we write) and the SQLite
// CURRENT_TIMESTAMP `YYYY-MM-DD HH:MM:SS` format (what legacy/default
// columns produce). Mirrors internal/users.parseStoredTime so callers
// don't have to think about timestamp format drift.
func parseStoredTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, format := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(format, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

// ----- Channel CRUD ----------------------------------------------------

func InsertChannel(ctx context.Context, db *sql.DB, input CreateChannelInput) (Channel, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Channel{}, fmt.Errorf("%w: name required", ErrInvalidID)
	}
	codec := strings.TrimSpace(input.Codec)
	if codec == "" {
		codec = "mp3"
	}
	bitrate := input.BitrateKbps
	if bitrate <= 0 {
		bitrate = 192
	}
	sampleRate := input.SampleRateHz
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	id, err := newID("channel")
	if err != nil {
		return Channel{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO channels (id, name, description, codec, bitrate_kbps, sample_rate_hz, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		id, name, strings.TrimSpace(input.Description), codec, bitrate, sampleRate, now, now,
	)
	if err != nil {
		return Channel{}, fmt.Errorf("insert channel: %w", err)
	}
	return LoadChannel(ctx, db, id)
}

func UpdateChannel(ctx context.Context, db *sql.DB, id string, input UpdateChannelInput) (Channel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Channel{}, ErrInvalidID
	}
	sets := []string{"updated_at = ?"}
	args := []any{time.Now().UTC().Format(time.RFC3339)}
	if input.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*input.Name))
	}
	if input.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*input.Description))
	}
	if input.Codec != nil {
		sets = append(sets, "codec = ?")
		args = append(args, strings.TrimSpace(*input.Codec))
	}
	if input.BitrateKbps != nil {
		sets = append(sets, "bitrate_kbps = ?")
		args = append(args, *input.BitrateKbps)
	}
	if input.SampleRateHz != nil {
		sets = append(sets, "sample_rate_hz = ?")
		args = append(args, *input.SampleRateHz)
	}
	if input.Enabled != nil {
		sets = append(sets, "enabled = ?")
		val := 0
		if *input.Enabled {
			val = 1
		}
		args = append(args, val)
	}
	args = append(args, id)
	result, err := db.ExecContext(ctx, fmt.Sprintf("UPDATE channels SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return Channel{}, fmt.Errorf("update channel: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Channel{}, ErrNotFound
	}
	return LoadChannel(ctx, db, id)
}

func DeleteChannel(ctx context.Context, db *sql.DB, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidID
	}
	result, err := db.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func LoadChannel(ctx context.Context, db *sql.DB, id string) (Channel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Channel{}, ErrInvalidID
	}
	var ch Channel
	var createdAt, updatedAt string
	var enabled int
	err := db.QueryRowContext(ctx, `
		SELECT id, name, description, codec, bitrate_kbps, sample_rate_hz, enabled, created_at, updated_at
		FROM channels WHERE id = ?`, id).Scan(
		&ch.ID, &ch.Name, &ch.Description, &ch.Codec, &ch.BitrateKbps, &ch.SampleRateHz, &enabled, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("load channel: %w", err)
	}
	ch.Enabled = enabled == 1
	ch.CreatedAt = parseStoredTime(createdAt)
	ch.UpdatedAt = parseStoredTime(updatedAt)
	return ch, nil
}

func ListChannels(ctx context.Context, db *sql.DB) ([]Channel, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, description, codec, bitrate_kbps, sample_rate_hz, enabled, created_at, updated_at
		FROM channels ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()
	items := make([]Channel, 0)
	for rows.Next() {
		var ch Channel
		var createdAt, updatedAt string
		var enabled int
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Codec, &ch.BitrateKbps, &ch.SampleRateHz, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		ch.Enabled = enabled == 1
		ch.CreatedAt = parseStoredTime(createdAt)
		ch.UpdatedAt = parseStoredTime(updatedAt)
		items = append(items, ch)
	}
	return items, rows.Err()
}

// ----- Source CRUD -----------------------------------------------------

func InsertSource(ctx context.Context, db *sql.DB, channelID string, input CreateSourceInput) (Source, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return Source{}, ErrInvalidID
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		return Source{}, fmt.Errorf("%w: kind required", ErrInvalidID)
	}
	cfg := input.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return Source{}, fmt.Errorf("marshal source config: %w", err)
	}
	weight := input.Weight
	if weight <= 0 {
		weight = 1
	}
	defaultRotation := 1
	if input.DefaultRotation != nil && !*input.DefaultRotation {
		defaultRotation = 0
	}
	enabled := 1
	if input.Enabled != nil && !*input.Enabled {
		enabled = 0
	}
	id, err := newID("csrc")
	if err != nil {
		return Source{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_sources (id, channel_id, kind, label, config_json, enabled, weight, default_rotation, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, channelID, kind, strings.TrimSpace(input.Label), string(cfgJSON), enabled, weight, defaultRotation, now, now,
	)
	if err != nil {
		return Source{}, fmt.Errorf("insert source: %w", err)
	}
	return LoadSource(ctx, db, id)
}

func UpdateSource(ctx context.Context, db *sql.DB, id string, input UpdateSourceInput) (Source, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Source{}, ErrInvalidID
	}
	sets := []string{"updated_at = ?"}
	args := []any{time.Now().UTC().Format(time.RFC3339)}
	if input.Label != nil {
		sets = append(sets, "label = ?")
		args = append(args, strings.TrimSpace(*input.Label))
	}
	if input.Config != nil {
		cfgJSON, err := json.Marshal(*input.Config)
		if err != nil {
			return Source{}, fmt.Errorf("marshal source config: %w", err)
		}
		sets = append(sets, "config_json = ?")
		args = append(args, string(cfgJSON))
	}
	if input.Weight != nil {
		w := *input.Weight
		if w <= 0 {
			w = 1
		}
		sets = append(sets, "weight = ?")
		args = append(args, w)
	}
	if input.DefaultRotation != nil {
		v := 0
		if *input.DefaultRotation {
			v = 1
		}
		sets = append(sets, "default_rotation = ?")
		args = append(args, v)
	}
	if input.Enabled != nil {
		v := 0
		if *input.Enabled {
			v = 1
		}
		sets = append(sets, "enabled = ?")
		args = append(args, v)
	}
	args = append(args, id)
	result, err := db.ExecContext(ctx, fmt.Sprintf("UPDATE channel_sources SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return Source{}, fmt.Errorf("update source: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Source{}, ErrNotFound
	}
	return LoadSource(ctx, db, id)
}

func DeleteSource(ctx context.Context, db *sql.DB, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidID
	}
	result, err := db.ExecContext(ctx, `DELETE FROM channel_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func LoadSource(ctx context.Context, db *sql.DB, id string) (Source, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Source{}, ErrInvalidID
	}
	src, err := scanSource(db.QueryRowContext(ctx, `
		SELECT id, channel_id, kind, label, config_json, enabled, weight, default_rotation, created_at, updated_at
		FROM channel_sources WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	return src, err
}

func ListChannelSources(ctx context.Context, db *sql.DB, channelID string) ([]Source, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, channel_id, kind, label, config_json, enabled, weight, default_rotation, created_at, updated_at
		FROM channel_sources WHERE channel_id = ?
		ORDER BY created_at ASC`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()
	items := make([]Source, 0)
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, src)
	}
	return items, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSource(row rowScanner) (Source, error) {
	var src Source
	var configJSON, createdAt, updatedAt string
	var enabled, defaultRotation int
	if err := row.Scan(&src.ID, &src.ChannelID, &src.Kind, &src.Label, &configJSON, &enabled, &src.Weight, &defaultRotation, &createdAt, &updatedAt); err != nil {
		return Source{}, fmt.Errorf("scan source: %w", err)
	}
	src.Enabled = enabled == 1
	src.DefaultRotation = defaultRotation == 1
	src.CreatedAt = parseStoredTime(createdAt)
	src.UpdatedAt = parseStoredTime(updatedAt)
	src.Config = map[string]any{}
	if strings.TrimSpace(configJSON) != "" {
		_ = json.Unmarshal([]byte(configJSON), &src.Config)
	}
	return src, nil
}

// ----- Schedule rules ---------------------------------------------------

func InsertScheduleRule(ctx context.Context, db *sql.DB, channelID string, input CreateScheduleRuleInput) (ScheduleRule, error) {
	channelID = strings.TrimSpace(channelID)
	sourceID := strings.TrimSpace(input.SourceID)
	if channelID == "" || sourceID == "" {
		return ScheduleRule{}, fmt.Errorf("%w: channel and source required", ErrInvalidID)
	}
	if input.StartMinute < 0 || input.StartMinute > 1439 || input.EndMinute < 0 || input.EndMinute > 1440 {
		return ScheduleRule{}, fmt.Errorf("%w: start/end must be minute-of-day (0-1440)", ErrInvalidID)
	}
	mask := input.WeekdayMask
	if mask == 0 {
		mask = 127 // default: every day
	}
	priority := input.Priority
	if priority == 0 {
		priority = 100
	}
	enabled := 1
	if input.Enabled != nil && !*input.Enabled {
		enabled = 0
	}
	id, err := newID("csched")
	if err != nil {
		return ScheduleRule{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_schedule_rules (id, channel_id, source_id, label, weekday_mask, start_minute, end_minute, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, channelID, sourceID, strings.TrimSpace(input.Label), mask, input.StartMinute, input.EndMinute, priority, enabled, now,
	)
	if err != nil {
		return ScheduleRule{}, fmt.Errorf("insert schedule rule: %w", err)
	}
	return LoadScheduleRule(ctx, db, id)
}

func DeleteScheduleRule(ctx context.Context, db *sql.DB, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidID
	}
	result, err := db.ExecContext(ctx, `DELETE FROM channel_schedule_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete schedule rule: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func LoadScheduleRule(ctx context.Context, db *sql.DB, id string) (ScheduleRule, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ScheduleRule{}, ErrInvalidID
	}
	rule, err := scanRule(db.QueryRowContext(ctx, `
		SELECT id, channel_id, source_id, label, weekday_mask, start_minute, end_minute, priority, enabled, created_at
		FROM channel_schedule_rules WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleRule{}, ErrNotFound
	}
	return rule, err
}

func ListScheduleRules(ctx context.Context, db *sql.DB, channelID string) ([]ScheduleRule, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, channel_id, source_id, label, weekday_mask, start_minute, end_minute, priority, enabled, created_at
		FROM channel_schedule_rules WHERE channel_id = ?
		ORDER BY priority DESC, start_minute ASC`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list schedule rules: %w", err)
	}
	defer rows.Close()
	items := make([]ScheduleRule, 0)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, rule)
	}
	return items, rows.Err()
}

func scanRule(row rowScanner) (ScheduleRule, error) {
	var rule ScheduleRule
	var createdAt string
	var enabled int
	if err := row.Scan(&rule.ID, &rule.ChannelID, &rule.SourceID, &rule.Label, &rule.WeekdayMask, &rule.StartMinute, &rule.EndMinute, &rule.Priority, &enabled, &createdAt); err != nil {
		return ScheduleRule{}, fmt.Errorf("scan schedule rule: %w", err)
	}
	rule.Enabled = enabled == 1
	rule.CreatedAt = parseStoredTime(createdAt)
	return rule, nil
}

// ----- Play log ---------------------------------------------------------

func RecordPlayStart(ctx context.Context, db *sql.DB, channelID string, item PlaybackItem) (string, error) {
	id, err := newID("cplay")
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_play_log (id, channel_id, source_id, item_ref, title, artist, kind, started_at, duration_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, channelID, item.SourceID, item.ItemRef, item.Title, item.Artist, item.Kind, now, item.DurationSeconds,
	)
	if err != nil {
		return "", fmt.Errorf("record play start: %w", err)
	}
	return id, nil
}

func RecordPlayEnd(ctx context.Context, db *sql.DB, id string) error {
	if id == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `UPDATE channel_play_log SET ended_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func RecentPlayLog(ctx context.Context, db *sql.DB, channelID string, limit int) ([]PlayLogEntry, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, channel_id, source_id, item_ref, title, artist, kind, started_at, ended_at, duration_seconds
		FROM channel_play_log WHERE channel_id = ?
		ORDER BY started_at DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, fmt.Errorf("list play log: %w", err)
	}
	defer rows.Close()
	items := make([]PlayLogEntry, 0)
	for rows.Next() {
		var entry PlayLogEntry
		var startedAt, endedAt string
		if err := rows.Scan(&entry.ID, &entry.ChannelID, &entry.SourceID, &entry.ItemRef, &entry.Title, &entry.Artist, &entry.Kind, &startedAt, &endedAt, &entry.DurationSeconds); err != nil {
			return nil, fmt.Errorf("scan play log: %w", err)
		}
		entry.StartedAt = parseStoredTime(startedAt)
		entry.EndedAt = parseStoredTime(endedAt)
		items = append(items, entry)
	}
	return items, rows.Err()
}

// RecentItemRefs returns the item_ref values played on this channel in
// the lookback window. The scheduler reads this to avoid repeating the
// same episode/file back-to-back. Empty refs are skipped so that file
// pools (which often share the same path naming convention) still rotate
// fairly.
func RecentItemRefs(ctx context.Context, db *sql.DB, channelID string, lookback time.Duration) (map[string]time.Time, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if lookback <= 0 {
		lookback = 4 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-lookback).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT item_ref, started_at FROM channel_play_log
		WHERE channel_id = ? AND item_ref <> '' AND started_at > ?`,
		channelID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent refs: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var ref, startedAt string
		if err := rows.Scan(&ref, &startedAt); err != nil {
			return nil, fmt.Errorf("scan recent ref: %w", err)
		}
		when := parseStoredTime(startedAt)
		if existing, ok := out[ref]; !ok || when.After(existing) {
			out[ref] = when
		}
	}
	return out, rows.Err()
}

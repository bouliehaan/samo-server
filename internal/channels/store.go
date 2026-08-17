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

// clockOr resolves the caller's clock, falling back to the wall clock.
//
// Every read query the scheduler makes a decision from takes an explicit `now`,
// because the scheduler's own clock is injectable and the store's was not — so
// a test could set the time to 09:17, seed a night of talk radio, and have the
// store measure that history against the real wall clock and see nothing. Every
// rule about balance, freshness and repeats is a rule about time, and none of
// them could be tested while the two halves disagreed about what time it was.
func clockOr(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
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
	if input.TalkShare != nil {
		share := *input.TalkShare
		if share < 0 || share >= 1 {
			return Channel{}, fmt.Errorf("%w: talk share must be between 0 and 1", ErrInvalidID)
		}
		sets = append(sets, "talk_share = ?")
		args = append(args, share)
	}
	if input.DayStartMinute != nil {
		if *input.DayStartMinute < 0 || *input.DayStartMinute > 1439 {
			return Channel{}, fmt.Errorf("%w: day start must be a minute of day", ErrInvalidID)
		}
		sets = append(sets, "day_start_minute = ?")
		args = append(args, *input.DayStartMinute)
	}
	if input.DayEndMinute != nil {
		if *input.DayEndMinute < 0 || *input.DayEndMinute > 1439 {
			return Channel{}, fmt.Errorf("%w: day end must be a minute of day", ErrInvalidID)
		}
		sets = append(sets, "day_end_minute = ?")
		args = append(args, *input.DayEndMinute)
	}
	if input.Timezone != nil {
		zone := strings.TrimSpace(*input.Timezone)
		if zone != "" {
			if _, err := time.LoadLocation(zone); err != nil {
				return Channel{}, fmt.Errorf("%w: unknown timezone %q", ErrInvalidID, zone)
			}
		}
		sets = append(sets, "timezone = ?")
		args = append(args, zone)
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
		SELECT id, name, description, codec, bitrate_kbps, sample_rate_hz, enabled, timezone, talk_share, day_start_minute, day_end_minute, cover_id, created_at, updated_at
		FROM channels WHERE id = ?`, id).Scan(
		&ch.ID, &ch.Name, &ch.Description, &ch.Codec, &ch.BitrateKbps, &ch.SampleRateHz, &enabled, &ch.Timezone, &ch.TalkShare, &ch.DayStartMinute, &ch.DayEndMinute, &ch.CoverID, &createdAt, &updatedAt,
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

// SetChannelCover points a channel at an uploaded cover, or clears it with an
// empty id so the generated tile takes over again.
//
// Separate from UpdateChannel because it is reached by a different route with
// a different body (a multipart upload, not JSON), and because clearing it is
// a real operation rather than an omitted field.
func SetChannelCover(ctx context.Context, db *sql.DB, id, coverID string) (Channel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Channel{}, ErrInvalidID
	}
	result, err := db.ExecContext(ctx, `
		UPDATE channels SET cover_id = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(coverID), time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return Channel{}, fmt.Errorf("set channel cover: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return Channel{}, ErrNotFound
	}
	return LoadChannel(ctx, db, id)
}

func ListChannels(ctx context.Context, db *sql.DB) ([]Channel, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, description, codec, bitrate_kbps, sample_rate_hz, enabled, timezone, talk_share, day_start_minute, day_end_minute, cover_id, created_at, updated_at
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
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Codec, &ch.BitrateKbps, &ch.SampleRateHz, &enabled, &ch.Timezone, &ch.TalkShare, &ch.DayStartMinute, &ch.DayEndMinute, &ch.CoverID, &createdAt, &updatedAt); err != nil {
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
		INSERT INTO channel_sources (id, channel_id, kind, label, config_json, enabled, weight, default_rotation, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, channelID, kind, strings.TrimSpace(input.Label), string(cfgJSON), enabled, weight, defaultRotation,
		NormalizeRole(input.Role, kind, defaultRotation == 1), now, now,
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
	if input.Role != nil {
		// Kind and rotation only matter when the submitted role is blank, and
		// a blank role on an update means "leave it derived" rather than
		// "clear it", so the existing row's values are irrelevant here.
		sets = append(sets, "role = ?")
		args = append(args, NormalizeRole(*input.Role, "", true))
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
		SELECT id, channel_id, kind, label, config_json, enabled, weight, default_rotation, role, created_at, updated_at
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
		SELECT id, channel_id, kind, label, config_json, enabled, weight, default_rotation, role, created_at, updated_at
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
	var configJSON, role, createdAt, updatedAt string
	var enabled, defaultRotation int
	if err := row.Scan(&src.ID, &src.ChannelID, &src.Kind, &src.Label, &configJSON, &enabled, &src.Weight, &defaultRotation, &role, &createdAt, &updatedAt); err != nil {
		return Source{}, fmt.Errorf("scan source: %w", err)
	}
	src.Enabled = enabled == 1
	src.DefaultRotation = defaultRotation == 1
	src.Role = NormalizeRole(role, src.Kind, src.DefaultRotation)
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
	// Stored verbatim. The category is whatever the station's own plan calls
	// this kind of programming, and the row is a record of what actually aired
	// — re-labelling a source tomorrow must not rewrite what last night sounded
	// like.
	category := item.Category
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_play_log (id, channel_id, source_id, item_ref, title, artist, kind, category, started_at, duration_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, channelID, item.SourceID, item.ItemRef, item.Title, item.Artist, item.Kind, string(category), now, item.DurationSeconds,
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
func RecentItemRefs(ctx context.Context, db *sql.DB, channelID string, lookback time.Duration, now time.Time) (map[string]time.Time, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if lookback <= 0 {
		lookback = 4 * time.Hour
	}
	cutoff := clockOr(now).Add(-lookback).Format(time.RFC3339)
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

// LastAiredByRef reports when this channel last aired each item, over an
// arbitrarily long window.
//
// The twin of RecentItemRefs, which asks the same table a much shorter
// question ("did I already play this in the last few hours"). Reruns need the
// long view: an episode nobody has listened to never changes its listened
// state, so without a record of what the CHANNEL aired, "the oldest unheard
// episode" is the same episode forever and the rerun tier loops on one item.
func LastAiredByRef(ctx context.Context, db *sql.DB, channelID string, window time.Duration, now time.Time) (map[string]time.Time, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if window <= 0 {
		window = 90 * 24 * time.Hour
	}
	cutoff := clockOr(now).Add(-window).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT item_ref, MAX(started_at) FROM channel_play_log
		WHERE channel_id = ? AND item_ref <> '' AND started_at > ?
		GROUP BY item_ref`,
		channelID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query last aired by ref: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var ref, startedAt string
		if err := rows.Scan(&ref, &startedAt); err != nil {
			return nil, fmt.Errorf("scan last aired: %w", err)
		}
		out[ref] = parseStoredTime(startedAt)
	}
	return out, rows.Err()
}

// LastAiredBySource reports when this channel last aired anything from each
// source. It is what rotates the ladder across shows rather than draining one:
// pick the source heard least recently, then the item within it.
func LastAiredBySource(ctx context.Context, db *sql.DB, channelID string, window time.Duration, now time.Time) (map[string]time.Time, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if window <= 0 {
		window = 90 * 24 * time.Hour
	}
	cutoff := clockOr(now).Add(-window).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT source_id, MAX(started_at) FROM channel_play_log
		WHERE channel_id = ? AND source_id <> '' AND started_at > ?
		GROUP BY source_id`,
		channelID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query last aired by source: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var sourceID, startedAt string
		if err := rows.Scan(&sourceID, &startedAt); err != nil {
			return nil, fmt.Errorf("scan last aired source: %w", err)
		}
		out[sourceID] = parseStoredTime(startedAt)
	}
	return out, rows.Err()
}

// LastLongFormBySource is when each source last put an ENORMOUS item on air.
//
// Distinct from LastAiredBySource because the rest a giant earns is paid for by
// the giant, not by the show. A feed that publishes the occasional three-hour
// special and a great many ordinary episodes should step back after the special
// and not after the ordinary ones, so the question is about the airing rather
// than about the show's usual habits.
//
// Rows rather than a MAX(), because the end matters: a four-hour episode that
// started four hours ago finished a second ago, and the rest runs from when the
// listener got their time back. Giants are rare, so this is a handful of rows.
func LastLongFormBySource(
	ctx context.Context,
	db *sql.DB,
	channelID string,
	minDuration, window time.Duration,
	now time.Time,
) (map[string]LongFormAiring, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if minDuration <= 0 {
		return map[string]LongFormAiring{}, nil
	}
	if window <= 0 {
		window = 90 * 24 * time.Hour
	}
	cutoff := clockOr(now).Add(-window).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT source_id, started_at, duration_seconds FROM channel_play_log
		WHERE channel_id = ? AND source_id <> '' AND started_at > ?
		  AND duration_seconds >= ?`,
		channelID, cutoff, int64(minDuration/time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("query last long form by source: %w", err)
	}
	defer rows.Close()
	out := map[string]LongFormAiring{}
	for rows.Next() {
		var sourceID, startedAt string
		var durationSeconds int64
		if err := rows.Scan(&sourceID, &startedAt, &durationSeconds); err != nil {
			return nil, fmt.Errorf("scan last long form: %w", err)
		}
		began := parseStoredTime(startedAt)
		if began.IsZero() {
			continue
		}
		length := time.Duration(durationSeconds) * time.Second
		if ended := began.Add(length); ended.After(out[sourceID].EndedAt) {
			out[sourceID] = LongFormAiring{EndedAt: ended, Length: length}
		}
	}
	return out, rows.Err()
}

// LongFormAiring is one enormous item that went out: when it finished, and how
// much of the day it took. The length is carried because the rest a show owes
// afterwards is priced by it.
type LongFormAiring struct {
	EndedAt time.Time
	Length  time.Duration
}

// PlayTailEntry is one row of the channel's recent history, newest first.
type PlayTailEntry struct {
	SourceID string
	ItemRef  string
	// Artist is the item-level attribution as it aired. Separation asks about
	// the person, and for music the person is per track rather than per source
	// — a playlist is one source and four hundred artists.
	Artist    string
	Category  CategoryID
	StartedAt time.Time
	// Aired is how long this row actually occupied the air, so a run of talk
	// can be measured in hours rather than counted in items. Ten five-minute
	// news bulletins and one five-hour podcast are both "ten items" and are not
	// remotely the same amount of somebody talking.
	Aired time.Duration
}

// PlayLogTail returns the channel's most recent plays in reverse order.
//
// The ladder's other play-log queries ask aggregate questions ("when did this
// source last air"). This one needs the sequence, because a music set is a run
// of consecutive plays from one source and you cannot see a run in a MAX().
// Keeping it a plain ordered read means the run-detection itself is ordinary
// Go, and testable without a database.
func PlayLogTail(ctx context.Context, db *sql.DB, channelID string, window time.Duration, limit int, now time.Time) ([]PlayTailEntry, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	if window <= 0 {
		window = time.Hour
	}
	if limit <= 0 {
		limit = 50
	}
	now = clockOr(now)
	cutoff := now.Add(-window)
	// Overlapping rather than started-inside, for the same reason as the
	// airtime query: the talk run is measured in hours and the block that puts
	// it over the line is usually the one that started before the window did.
	since := cutoff.Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT source_id, item_ref, artist, category, started_at, ended_at, duration_seconds FROM channel_play_log
		WHERE channel_id = ? AND source_id <> ''
		  AND (started_at > ? OR ended_at = '' OR ended_at > ?)
		ORDER BY started_at DESC
		LIMIT ?`,
		channelID, since, since, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query play log tail: %w", err)
	}
	defer rows.Close()
	out := make([]PlayTailEntry, 0, limit)
	for rows.Next() {
		var sourceID, itemRef, artist, category, startedAt, endedAt string
		var durationSeconds int64
		if err := rows.Scan(&sourceID, &itemRef, &artist, &category, &startedAt, &endedAt, &durationSeconds); err != nil {
			return nil, fmt.Errorf("scan play log tail: %w", err)
		}
		began := parseStoredTime(startedAt)
		out = append(out, PlayTailEntry{
			SourceID:  sourceID,
			ItemRef:   itemRef,
			Artist:    artist,
			Category:  storedCategory(category),
			StartedAt: began,
			Aired:     airedDuration(began, parseStoredTime(endedAt), durationSeconds, cutoff, now),
		})
	}
	return out, rows.Err()
}

// airedDuration is how much of the window a play-log row actually filled.
//
// One definition, used by every query that asks the question, because they must
// agree: the talk-run governor and the balance reading the same row differently
// is a bug that only ever shows up as strange programming at 3am.
//
// A row still playing (no ended_at) counts up to now, so a long item in progress
// pushes the balance immediately rather than only once it finishes — which is
// the difference between noticing you are two hours into a podcast and noticing
// it afterwards.
func airedDuration(began, ended time.Time, durationSeconds int64, windowStart, now time.Time) time.Duration {
	if began.IsZero() {
		return 0
	}
	if began.Before(windowStart) {
		began = windowStart
	}
	if ended.IsZero() || ended.After(now) {
		ended = now
	}
	aired := ended.Sub(began)
	// A row whose clock says nothing useful falls back to the recorded
	// duration, then to a nominal slot, so it still counts for something.
	if aired <= 0 {
		aired = time.Duration(durationSeconds) * time.Second
	}
	if aired <= 0 {
		aired = time.Minute
	}
	return aired
}

// DiscardPlayLog removes a play-log row entirely.
//
// Used when an item was skipped almost immediately. The log is what the
// scheduler treats as "this channel has aired that" — freshness suppression
// reads it, and rerun ordering sorts by it — so leaving a row behind for
// something nobody actually heard burns the episode: it stops being fresh and
// goes to the back of a thirty-day queue, on the strength of three seconds of
// audio. Skipping should cost you the next few minutes, not the episode.
func DiscardPlayLog(ctx context.Context, db *sql.DB, playLogID string) error {
	playLogID = strings.TrimSpace(playLogID)
	if playLogID == "" {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM channel_play_log WHERE id = ?`, playLogID); err != nil {
		return fmt.Errorf("discard play log: %w", err)
	}
	return nil
}

// SourceAirtime is how long a source has been on air in a window.
type SourceAirtime struct {
	SourceID string
	Aired    time.Duration
	Plays    int
	// ByCategory is this source's airtime split by the category each airing was
	// RECORDED under, which is not always the one it would be recorded under
	// today. Re-labelling a source must not make last night's airtime
	// unsubtractable from the bucket it actually went into.
	ByCategory map[CategoryID]time.Duration
}

// AirtimeWindow is what the station has actually been doing lately.
type AirtimeWindow struct {
	BySource map[string]SourceAirtime
	// ByCategory is the aggregate the balance is actually about. Choosing
	// between individual sources cannot answer "have we had too much spoken
	// word", because every source's slice is small and every source can be
	// behind its own slice while its whole category is hours over.
	ByCategory map[CategoryID]time.Duration
	Total      time.Duration
}

// storedCategory reads a play-log row's category.
//
// Rows written before categories existed carry an empty string. They are
// spoken word — that is what migration 0017 backfilled everything else to, and
// a station that has since renamed its categories would rather see one stale
// bucket than have last week's history silently vanish from the balance.
func storedCategory(raw string) CategoryID {
	if raw == "" {
		return LegacyCategoryTalk
	}
	return CategoryID(raw)
}

// AirtimeBySource measures how much of the window each source and each category
// actually filled.
//
// Airtime, not play count, is the unit that matters: three minutes of music and
// three hours of Joe Rogan are one play each, and treating them as equal is how
// a station ends up 90% spoken word while believing it is balanced.
//
// Scheduled shows are counted like everything else, deliberately. An hour of
// booked public radio is an hour of somebody talking whether or not a rule
// picked it, so it has to push the programming either side of it toward music —
// otherwise the schedule and the rotation each behave sensibly on their own and
// the day adds up to nonsense.
func AirtimeBySource(ctx context.Context, db *sql.DB, channelID string, window time.Duration, now time.Time) (AirtimeWindow, error) {
	out := AirtimeWindow{
		BySource:   map[string]SourceAirtime{},
		ByCategory: map[CategoryID]time.Duration{},
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return out, ErrInvalidID
	}
	if window <= 0 {
		window = 24 * time.Hour
	}
	now = clockOr(now)
	start := now.Add(-window)
	// Rows that OVERLAP the window, not rows that started inside it.
	//
	// `started_at > cutoff` alone loses exactly the items that matter most: an
	// eight-hour talk block that began nine hours ago is still five hours of
	// this six-hour window, but its start has slid out and the row vanishes —
	// so the balance reads "no talk lately" at the precise moment the station
	// has been doing nothing else. An empty ended_at is something still on air,
	// which always overlaps.
	cutoff := start.Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT source_id, category, started_at, ended_at, duration_seconds
		FROM channel_play_log
		WHERE channel_id = ? AND source_id <> ''
		  AND (started_at > ? OR ended_at = '' OR ended_at > ?)`,
		channelID, cutoff, cutoff,
	)
	if err != nil {
		return out, fmt.Errorf("query airtime: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sourceID, category, startedAt, endedAt string
		var durationSeconds int64
		if err := rows.Scan(&sourceID, &category, &startedAt, &endedAt, &durationSeconds); err != nil {
			return out, fmt.Errorf("scan airtime: %w", err)
		}
		began := parseStoredTime(startedAt)
		if began.IsZero() {
			continue
		}
		aired := airedDuration(began, parseStoredTime(endedAt), durationSeconds, start, now)
		if aired <= 0 {
			continue
		}

		entry := out.BySource[sourceID]
		entry.SourceID = sourceID
		entry.Aired += aired
		entry.Plays++
		if entry.ByCategory == nil {
			entry.ByCategory = map[CategoryID]time.Duration{}
		}
		entry.ByCategory[storedCategory(category)] += aired
		out.BySource[sourceID] = entry
		out.ByCategory[storedCategory(category)] += aired
		out.Total += aired
	}
	return out, rows.Err()
}

// AiredInListeningDay reports which items have aired while somebody could
// plausibly have been listening, and when they last did.
//
// The twin of ItemAirings, asking the question that actually decides whether an
// episode is still new: not "has this been on air" but "has this reached
// anyone". A channel is on twenty-four hours; airing an overnight drop at 03:00
// is not the same event as airing it at 09:30, and counting them the same is
// precisely how a new episode gets spent on a dark room and greets the listener
// as back catalogue.
//
// Filtered in Go rather than SQL because the window is wall-clock in the
// channel's own timezone and started_at is stored UTC — the same mismatch that
// made every scheduled slot fire at the wrong hour.
func AiredInListeningDay(
	ctx context.Context,
	db *sql.DB,
	channelID string,
	window time.Duration,
	day ListeningDay,
	loc *time.Location,
	now time.Time,
) (map[string]int, map[string]time.Time, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil, ErrInvalidID
	}
	if window <= 0 {
		window = 24 * time.Hour
	}
	if loc == nil {
		loc = time.UTC
	}
	cutoff := clockOr(now).Add(-window).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT item_ref, started_at FROM channel_play_log
		WHERE channel_id = ? AND item_ref <> '' AND started_at > ?`,
		channelID, cutoff,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query daytime airings: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	last := map[string]time.Time{}
	for rows.Next() {
		var ref, startedAt string
		if err := rows.Scan(&ref, &startedAt); err != nil {
			return nil, nil, fmt.Errorf("scan daytime airing: %w", err)
		}
		began := parseStoredTime(startedAt)
		if began.IsZero() || !day.Contains(began.In(loc)) {
			continue
		}
		counts[ref]++
		if existing, ok := last[ref]; !ok || began.After(existing) {
			last[ref] = began
		}
	}
	return counts, last, rows.Err()
}

// ItemAirings counts how many times each item ref aired in a window, and when
// it last did. Backs the per-episode repeat caps.
func ItemAirings(ctx context.Context, db *sql.DB, channelID string, window time.Duration, now time.Time) (map[string]int, map[string]time.Time, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil, ErrInvalidID
	}
	if window <= 0 {
		window = 24 * time.Hour
	}
	cutoff := clockOr(now).Add(-window).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT item_ref, COUNT(*), MAX(started_at) FROM channel_play_log
		WHERE channel_id = ? AND item_ref <> '' AND started_at > ?
		GROUP BY item_ref`,
		channelID, cutoff,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query item airings: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	last := map[string]time.Time{}
	for rows.Next() {
		var ref, startedAt string
		var count int
		if err := rows.Scan(&ref, &count, &startedAt); err != nil {
			return nil, nil, fmt.Errorf("scan item airings: %w", err)
		}
		counts[ref] = count
		last[ref] = parseStoredTime(startedAt)
	}
	return counts, last, rows.Err()
}

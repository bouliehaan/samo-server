package lastfm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type sessionRecord struct {
	UserID      string
	Username    string
	SessionKey  string
	ConnectedAt time.Time
}

type appConfigRecord struct {
	Enabled      bool
	APIKey       string
	SharedSecret string
	UpdatedAt    time.Time
}

func loadAppConfig(ctx context.Context, db *sql.DB) (appConfigRecord, bool, error) {
	var enabled int
	var apiKey, sharedSecret, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT enabled, api_key, shared_secret, updated_at
		FROM lastfm_app_config
		WHERE id = 1`).Scan(&enabled, &apiKey, &sharedSecret, &updatedAt)
	if err == sql.ErrNoRows {
		return appConfigRecord{}, false, nil
	}
	if err != nil {
		return appConfigRecord{}, false, fmt.Errorf("load last.fm app config: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		parsed = time.Now().UTC()
	}
	return appConfigRecord{
		Enabled:      enabled != 0,
		APIKey:       apiKey,
		SharedSecret: sharedSecret,
		UpdatedAt:    parsed,
	}, true, nil
}

func saveAppConfig(ctx context.Context, db *sql.DB, enabled bool, apiKey, sharedSecret string) (appConfigRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO lastfm_app_config (id, enabled, api_key, shared_secret, updated_at)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			api_key = excluded.api_key,
			shared_secret = excluded.shared_secret,
			updated_at = excluded.updated_at`,
		boolInt(enabled),
		strings.TrimSpace(apiKey),
		strings.TrimSpace(sharedSecret),
		now,
	)
	if err != nil {
		return appConfigRecord{}, fmt.Errorf("save last.fm app config: %w", err)
	}
	record, _, err := loadAppConfig(ctx, db)
	return record, err
}

func loadSession(ctx context.Context, db *sql.DB, userID string) (sessionRecord, error) {
	var username, sessionKey, connectedAt string
	err := db.QueryRowContext(ctx, `
		SELECT lastfm_username, session_key, connected_at
		FROM lastfm_user_settings
		WHERE user_id = ?`, userID).Scan(&username, &sessionKey, &connectedAt)
	if err == sql.ErrNoRows {
		return sessionRecord{}, ErrNotConnected
	}
	if err != nil {
		return sessionRecord{}, fmt.Errorf("load last.fm session: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, connectedAt)
	if err != nil {
		parsed = time.Now().UTC()
	}
	return sessionRecord{
		UserID:      userID,
		Username:    username,
		SessionKey:  sessionKey,
		ConnectedAt: parsed,
	}, nil
}

func saveSession(ctx context.Context, db *sql.DB, userID, username, sessionKey string) (sessionRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO lastfm_user_settings (user_id, lastfm_username, session_key, connected_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			lastfm_username = excluded.lastfm_username,
			session_key = excluded.session_key,
			connected_at = excluded.connected_at`,
		userID,
		strings.TrimSpace(username),
		strings.TrimSpace(sessionKey),
		now,
	)
	if err != nil {
		return sessionRecord{}, fmt.Errorf("save last.fm session: %w", err)
	}
	return loadSession(ctx, db, userID)
}

func deleteSession(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM lastfm_user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete last.fm session: %w", err)
	}
	return nil
}

type trackSession struct {
	UserID         string
	TrackID        string
	PlayToken      string
	NowPlayingSent bool
	Scrobbled      bool
	PlayStartedAt  time.Time
}

func loadTrackSession(ctx context.Context, db *sql.DB, userID, trackID string) (trackSession, error) {
	var (
		playToken      string
		nowPlayingSent int
		scrobbled      int
		playStartedAt  int64
	)
	err := db.QueryRowContext(ctx, `
		SELECT play_token, now_playing_sent, scrobbled, play_started_at
		FROM lastfm_track_sessions
		WHERE user_id = ? AND track_id = ?`, userID, trackID).Scan(&playToken, &nowPlayingSent, &scrobbled, &playStartedAt)
	if err == sql.ErrNoRows {
		return trackSession{UserID: userID, TrackID: trackID}, nil
	}
	if err != nil {
		return trackSession{}, fmt.Errorf("load last.fm track session: %w", err)
	}
	return trackSession{
		UserID:         userID,
		TrackID:        trackID,
		PlayToken:      playToken,
		NowPlayingSent: nowPlayingSent != 0,
		Scrobbled:      scrobbled != 0,
		PlayStartedAt:  time.Unix(playStartedAt, 0).UTC(),
	}, nil
}

func saveTrackSession(ctx context.Context, db *sql.DB, session trackSession) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO lastfm_track_sessions (
			user_id, track_id, play_token, now_playing_sent, scrobbled, play_started_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, track_id) DO UPDATE SET
			play_token = excluded.play_token,
			now_playing_sent = excluded.now_playing_sent,
			scrobbled = excluded.scrobbled,
			play_started_at = excluded.play_started_at,
			updated_at = excluded.updated_at`,
		session.UserID,
		session.TrackID,
		session.PlayToken,
		boolInt(session.NowPlayingSent),
		boolInt(session.Scrobbled),
		session.PlayStartedAt.Unix(),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save last.fm track session: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type queuedSubmission struct {
	ID                   int64
	UserID               string
	Kind                 string
	TrackID              string
	Artist               string
	Track                string
	Album                string
	DurationSeconds      int
	Timestamp            time.Time
	Attempts             int
	MusicBrainzRecording string
}

func enqueueSubmission(ctx context.Context, db *sql.DB, userID, kind string, submission TrackSubmission, source string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO lastfm_scrobble_queue (
			user_id, kind, track_id, artist, track, album, duration_seconds, timestamp, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID,
		kind,
		strings.TrimSpace(submission.TrackID),
		submission.Artist,
		submission.Track,
		strings.TrimSpace(submission.Album),
		submission.DurationSeconds,
		submission.Timestamp.Unix(),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("enqueue last.fm submission: %w", err)
	}
	return nil
}

func countQueue(ctx context.Context, db *sql.DB, userID string) (int, error) {
	var count int
	var err error
	if userID == "" {
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_scrobble_queue`).Scan(&count)
	} else {
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_scrobble_queue WHERE user_id = ?`, userID).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("count last.fm queue: %w", err)
	}
	return count, nil
}

func listQueuePage(ctx context.Context, db *sql.DB, userID string, limit, offset int) ([]QueueItem, int, error) {
	limit, offset = normalizePage(limit, offset)
	var total int
	var rows *sql.Rows
	var err error
	if userID == "" {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_scrobble_queue`).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `
			SELECT id, kind, track_id, artist, track, album, duration_seconds, timestamp, attempts, last_error, created_at
			FROM lastfm_scrobble_queue ORDER BY id ASC LIMIT ? OFFSET ?`, limit, offset)
	} else {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_scrobble_queue WHERE user_id = ?`, userID).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `
			SELECT id, kind, track_id, artist, track, album, duration_seconds, timestamp, attempts, last_error, created_at
			FROM lastfm_scrobble_queue WHERE user_id = ? ORDER BY id ASC LIMIT ? OFFSET ?`, userID, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return scanQueueRows(rows, total)
}

func listQueuedSubmissions(ctx context.Context, db *sql.DB, userID string, limit int) ([]queuedSubmission, error) {
	if limit <= 0 {
		limit = 25
	}
	var rows *sql.Rows
	var err error
	if userID == "" {
		rows, err = db.QueryContext(ctx, `
			SELECT id, user_id, kind, track_id, artist, track, album, duration_seconds, timestamp, attempts
			FROM lastfm_scrobble_queue ORDER BY id ASC LIMIT ?`, limit)
	} else {
		rows, err = db.QueryContext(ctx, `
			SELECT id, user_id, kind, track_id, artist, track, album, duration_seconds, timestamp, attempts
			FROM lastfm_scrobble_queue WHERE user_id = ? ORDER BY id ASC LIMIT ?`, userID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list last.fm queue: %w", err)
	}
	defer rows.Close()

	items := make([]queuedSubmission, 0)
	for rows.Next() {
		var item queuedSubmission
		var album, trackID sql.NullString
		var duration sql.NullInt64
		var timestamp int64
		if err := rows.Scan(&item.ID, &item.UserID, &item.Kind, &trackID, &item.Artist, &item.Track, &album, &duration, &timestamp, &item.Attempts); err != nil {
			return nil, fmt.Errorf("scan last.fm queue row: %w", err)
		}
		item.TrackID = trackID.String
		item.Album = album.String
		item.DurationSeconds = int(duration.Int64)
		item.Timestamp = time.Unix(timestamp, 0).UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanQueueRows(rows *sql.Rows, total int) ([]QueueItem, int, error) {
	items := make([]QueueItem, 0)
	for rows.Next() {
		var item QueueItem
		var album, trackID, lastError sql.NullString
		var duration sql.NullInt64
		var timestamp int64
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Kind, &trackID, &item.Artist, &item.Track, &album, &duration, &timestamp, &item.Attempts, &lastError, &createdAt); err != nil {
			return nil, 0, err
		}
		item.TrackID = trackID.String
		item.Album = album.String
		item.DurationSeconds = int(duration.Int64)
		item.Timestamp = time.Unix(timestamp, 0).UTC()
		item.LastError = lastError.String
		if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
			item.CreatedAt = parsed
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func markQueueFailure(ctx context.Context, db *sql.DB, id int64, attempts int, message string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE lastfm_scrobble_queue
		SET attempts = ?, last_error = ?
		WHERE id = ?`, attempts, strings.TrimSpace(message), id)
	return err
}

func deleteQueueItem(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM lastfm_scrobble_queue WHERE id = ?`, id)
	return err
}

func recordSubmission(ctx context.Context, db *sql.DB, userID, kind string, submission TrackSubmission, status, source string, err error) error {
	message := ""
	if err != nil {
		message = strings.TrimSpace(err.Error())
	}
	_, execErr := db.ExecContext(ctx, `
		INSERT INTO lastfm_submissions (
			user_id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			timestamp, status, error, source, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID,
		kind,
		strings.TrimSpace(submission.TrackID),
		submission.Artist,
		submission.Track,
		strings.TrimSpace(submission.Album),
		submission.DurationSeconds,
		submission.PlayedSeconds,
		submission.Timestamp.Unix(),
		status,
		message,
		strings.TrimSpace(source),
		time.Now().UTC().Format(time.RFC3339),
	)
	if execErr != nil {
		return fmt.Errorf("record last.fm submission: %w", execErr)
	}
	return nil
}

func listSubmissionHistory(ctx context.Context, db *sql.DB, userID string, limit, offset int) ([]SubmissionRecord, int, error) {
	limit, offset = normalizePage(limit, offset)
	var total int
	var rows *sql.Rows
	var err error
	if userID == "" {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_submissions`).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `
			SELECT id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			       timestamp, status, error, source, created_at
			FROM lastfm_submissions ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	} else {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_submissions WHERE user_id = ?`, userID).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `
			SELECT id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			       timestamp, status, error, source, created_at
			FROM lastfm_submissions WHERE user_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]SubmissionRecord, 0)
	for rows.Next() {
		var item SubmissionRecord
		var album, trackID, errText, source sql.NullString
		var duration, played sql.NullInt64
		var timestamp int64
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Kind, &trackID, &item.Artist, &item.Track, &album, &duration, &played, &timestamp, &item.Status, &errText, &source, &createdAt); err != nil {
			return nil, 0, err
		}
		item.TrackID = trackID.String
		item.Album = album.String
		item.DurationSeconds = int(duration.Int64)
		item.PlayedSeconds = int(played.Int64)
		item.Timestamp = time.Unix(timestamp, 0).UTC()
		item.Error = errText.String
		item.Source = source.String
		if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
			item.CreatedAt = parsed
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

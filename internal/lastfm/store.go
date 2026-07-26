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
		APIKey:       strings.TrimSpace(apiKey),
		SharedSecret: strings.TrimSpace(sharedSecret),
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

// ---------------------------------------------------------------------------
// plays
// ---------------------------------------------------------------------------

func loadPlay(ctx context.Context, db *sql.DB, userID, trackID string) (play, error) {
	var (
		playID                                  string
		startedAt, lastPosition, lastObservedAt int64
		lastAdvanceAt, listened, duration       int64
		scrobbled, closed                       int64
	)
	err := db.QueryRowContext(ctx, `
		SELECT play_id, started_at, last_position, last_observed_at, last_advance_at,
		       listened_seconds, duration_seconds, scrobbled, closed
		FROM lastfm_plays
		WHERE user_id = ? AND track_id = ?`, userID, trackID).
		Scan(&playID, &startedAt, &lastPosition, &lastObservedAt, &lastAdvanceAt,
			&listened, &duration, &scrobbled, &closed)
	if err == sql.ErrNoRows {
		return play{UserID: userID, TrackID: trackID}, nil
	}
	if err != nil {
		return play{}, fmt.Errorf("load last.fm play: %w", err)
	}
	return play{
		UserID:          userID,
		TrackID:         trackID,
		PlayID:          playID,
		StartedAt:       unixTime(startedAt),
		LastPosition:    int(lastPosition),
		LastObservedAt:  unixTime(lastObservedAt),
		LastAdvanceAt:   unixTime(lastAdvanceAt),
		ListenedSeconds: int(listened),
		DurationSeconds: int(duration),
		Scrobbled:       scrobbled != 0,
		Closed:          closed != 0,
		Exists:          true,
	}, nil
}

func savePlay(ctx context.Context, db *sql.DB, p play) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO lastfm_plays (
			user_id, track_id, play_id, started_at, last_position, last_observed_at,
			last_advance_at, listened_seconds, duration_seconds, scrobbled, closed, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		ON CONFLICT(user_id, track_id) DO UPDATE SET
			play_id = excluded.play_id,
			started_at = excluded.started_at,
			last_position = excluded.last_position,
			last_observed_at = excluded.last_observed_at,
			last_advance_at = excluded.last_advance_at,
			listened_seconds = excluded.listened_seconds,
			duration_seconds = excluded.duration_seconds,
			scrobbled = excluded.scrobbled,
			closed = excluded.closed,
			updated_at = NOW()`,
		p.UserID, p.TrackID, p.PlayID,
		unixSeconds(p.StartedAt), p.LastPosition, unixSeconds(p.LastObservedAt),
		unixSeconds(p.LastAdvanceAt), p.ListenedSeconds, p.DurationSeconds,
		boolInt(p.Scrobbled), boolInt(p.Closed),
	)
	if err != nil {
		return fmt.Errorf("save last.fm play: %w", err)
	}
	return nil
}

// latestOtherAdvance returns when a track OTHER than trackID last credited real
// listening for this user. It is how a prefetched next track is told apart from
// the one actually playing.
func latestOtherAdvance(ctx context.Context, db *sql.DB, userID, trackID string) (time.Time, error) {
	var advancedAt sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT MAX(last_advance_at) FROM lastfm_plays
		WHERE user_id = ? AND track_id <> ? AND closed = 0`, userID, trackID).Scan(&advancedAt)
	if err != nil && err != sql.ErrNoRows {
		return time.Time{}, fmt.Errorf("load last.fm play advance: %w", err)
	}
	if !advancedAt.Valid {
		return time.Time{}, nil
	}
	return unixTime(advancedAt.Int64), nil
}

func prunePlays(ctx context.Context, db *sql.DB, before time.Time) error {
	_, err := db.ExecContext(ctx, `DELETE FROM lastfm_plays WHERE updated_at < ?`, before.UTC())
	if err != nil {
		return fmt.Errorf("prune last.fm plays: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// now playing pointer
// ---------------------------------------------------------------------------

func loadNowPlaying(ctx context.Context, db *sql.DB, userID string) (nowPlayingPointer, error) {
	var trackID, playID string
	var sentAt int64
	err := db.QueryRowContext(ctx, `
		SELECT track_id, play_id, sent_at FROM lastfm_now_playing WHERE user_id = ?`, userID).
		Scan(&trackID, &playID, &sentAt)
	if err == sql.ErrNoRows {
		return nowPlayingPointer{}, nil
	}
	if err != nil {
		return nowPlayingPointer{}, fmt.Errorf("load last.fm now playing: %w", err)
	}
	return nowPlayingPointer{TrackID: trackID, PlayID: playID, SentAt: unixTime(sentAt), Exists: true}, nil
}

func saveNowPlaying(ctx context.Context, db *sql.DB, userID string, pointer nowPlayingPointer) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO lastfm_now_playing (user_id, track_id, play_id, sent_at, updated_at)
		VALUES (?, ?, ?, ?, NOW())
		ON CONFLICT(user_id) DO UPDATE SET
			track_id = excluded.track_id,
			play_id = excluded.play_id,
			sent_at = excluded.sent_at,
			updated_at = NOW()`,
		userID, pointer.TrackID, pointer.PlayID, unixSeconds(pointer.SentAt))
	if err != nil {
		return fmt.Errorf("save last.fm now playing: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// queue: durable write-ahead log for everything sent upstream
// ---------------------------------------------------------------------------

type queuedSubmission struct {
	ID                   int64
	UserID               string
	Kind                 string
	TrackID              string
	Artist               string
	Track                string
	Album                string
	DurationSeconds      int
	PlayedSeconds        int
	Timestamp            time.Time
	Attempts             int
	MusicBrainzRecording string
	DedupeKey            string
	Source               string
}

func (q queuedSubmission) submission() TrackSubmission {
	return TrackSubmission{
		TrackID:              q.TrackID,
		Artist:               q.Artist,
		Track:                q.Track,
		Album:                q.Album,
		DurationSeconds:      q.DurationSeconds,
		PlayedSeconds:        q.PlayedSeconds,
		Timestamp:            q.Timestamp,
		MusicBrainzRecording: q.MusicBrainzRecording,
	}
}

// claimScrobble reserves a scrobble and writes it to the queue in one
// transaction. The ledger row is the exactly-once guarantee: whoever inserts it
// owns the scrobble, and every later attempt to claim the same play — a racing
// goroutine, a re-sent request, a replay after a crash — gets claimed=false.
//
// Writing the queue row in the same transaction is what makes the scrobble
// durable BEFORE Last.fm is contacted, so a crash mid-delivery loses nothing.
// leaseUntil hides the new row from every other flusher for the duration of
// the caller's own delivery attempt, so no one can send a copy of something
// already in flight. It is set in the same INSERT rather than a follow-up
// UPDATE, which would leave a window where the row is visible and unclaimed.
func claimScrobble(ctx context.Context, db *sql.DB, userID string, submission TrackSubmission, source string, leaseUntil time.Time) (queuedSubmission, bool, error) {
	if strings.TrimSpace(submission.DedupeKey) == "" {
		return queuedSubmission{}, false, fmt.Errorf("scrobble claim requires a dedupe key")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return queuedSubmission{}, false, fmt.Errorf("begin last.fm scrobble claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO lastfm_scrobble_ledger (user_id, dedupe_key, track_id, artist, track, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, dedupe_key) DO NOTHING`,
		userID, submission.DedupeKey, strings.TrimSpace(submission.TrackID),
		submission.Artist, submission.Track, submission.Timestamp.Unix())
	if err != nil {
		return queuedSubmission{}, false, fmt.Errorf("claim last.fm scrobble: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return queuedSubmission{}, false, nil
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO lastfm_scrobble_queue (
			user_id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			timestamp, mbid, dedupe_key, source, next_attempt_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		userID, queueKindScrobble, strings.TrimSpace(submission.TrackID),
		submission.Artist, submission.Track, strings.TrimSpace(submission.Album),
		submission.DurationSeconds, submission.PlayedSeconds, submission.Timestamp.Unix(),
		strings.TrimSpace(submission.MusicBrainzRecording), submission.DedupeKey,
		strings.TrimSpace(source), leaseUntil.Unix(), time.Now().UTC().Format(time.RFC3339)).Scan(&id)
	if err != nil {
		return queuedSubmission{}, false, fmt.Errorf("enqueue last.fm scrobble: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return queuedSubmission{}, false, fmt.Errorf("commit last.fm scrobble claim: %w", err)
	}

	item := queuedSubmission{
		ID:                   id,
		UserID:               userID,
		Kind:                 queueKindScrobble,
		TrackID:              submission.TrackID,
		Artist:               submission.Artist,
		Track:                submission.Track,
		Album:                submission.Album,
		DurationSeconds:      submission.DurationSeconds,
		PlayedSeconds:        submission.PlayedSeconds,
		Timestamp:            submission.Timestamp,
		MusicBrainzRecording: submission.MusicBrainzRecording,
		DedupeKey:            submission.DedupeKey,
		Source:               source,
	}
	return item, true, nil
}

// enqueueLove stores a love/unlove for retry. These carry no dedupe key: they
// are idempotent upstream, so a repeat is harmless.
func enqueueLove(ctx context.Context, db *sql.DB, userID, kind string, submission TrackSubmission, source string, leaseUntil time.Time) (queuedSubmission, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO lastfm_scrobble_queue (
			user_id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			timestamp, mbid, dedupe_key, source, next_attempt_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)
		RETURNING id`,
		userID, kind, strings.TrimSpace(submission.TrackID),
		submission.Artist, submission.Track, strings.TrimSpace(submission.Album),
		submission.DurationSeconds, submission.PlayedSeconds, submission.Timestamp.Unix(),
		strings.TrimSpace(submission.MusicBrainzRecording), strings.TrimSpace(source),
		leaseUntil.Unix(), time.Now().UTC().Format(time.RFC3339)).Scan(&id)
	if err != nil {
		return queuedSubmission{}, fmt.Errorf("enqueue last.fm %s: %w", kind, err)
	}
	return queuedSubmission{
		ID: id, UserID: userID, Kind: kind,
		TrackID: submission.TrackID, Artist: submission.Artist, Track: submission.Track,
		Album: submission.Album, DurationSeconds: submission.DurationSeconds,
		Timestamp: submission.Timestamp, MusicBrainzRecording: submission.MusicBrainzRecording,
		Source: source,
	}, nil
}

const queueColumns = `id, user_id, kind, track_id, artist, track, album,
	duration_seconds, played_seconds, timestamp, attempts, mbid, dedupe_key, source`

func listDueQueue(ctx context.Context, db *sql.DB, userID string, now time.Time, limit int) ([]queuedSubmission, error) {
	if limit <= 0 {
		limit = 50
	}
	// Oldest listen first: Last.fm renders a user's history in timestamp order,
	// and delivering a backlog chronologically keeps it coherent.
	query := `SELECT ` + queueColumns + ` FROM lastfm_scrobble_queue
		WHERE next_attempt_at <= ?`
	args := []any{now.Unix()}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY timestamp ASC, id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list last.fm queue: %w", err)
	}
	defer rows.Close()

	items := make([]queuedSubmission, 0, limit)
	for rows.Next() {
		var item queuedSubmission
		var album, trackID, mbid, dedupe, source sql.NullString
		var duration, played sql.NullInt64
		var timestamp int64
		if err := rows.Scan(&item.ID, &item.UserID, &item.Kind, &trackID, &item.Artist, &item.Track,
			&album, &duration, &played, &timestamp, &item.Attempts, &mbid, &dedupe, &source); err != nil {
			return nil, fmt.Errorf("scan last.fm queue row: %w", err)
		}
		item.TrackID = trackID.String
		item.Album = album.String
		item.DurationSeconds = int(duration.Int64)
		item.PlayedSeconds = int(played.Int64)
		item.Timestamp = unixTime(timestamp)
		item.MusicBrainzRecording = mbid.String
		item.DedupeKey = dedupe.String
		item.Source = source.String
		items = append(items, item)
	}
	return items, rows.Err()
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
	const columns = `id, kind, track_id, artist, track, album, duration_seconds,
		timestamp, attempts, last_error, created_at, next_attempt_at`
	if userID == "" {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_scrobble_queue`).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `SELECT `+columns+`
			FROM lastfm_scrobble_queue ORDER BY id ASC LIMIT ? OFFSET ?`, limit, offset)
	} else {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lastfm_scrobble_queue WHERE user_id = ?`, userID).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `SELECT `+columns+`
			FROM lastfm_scrobble_queue WHERE user_id = ? ORDER BY id ASC LIMIT ? OFFSET ?`, userID, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return scanQueueRows(rows, total)
}

func scanQueueRows(rows *sql.Rows, total int) ([]QueueItem, int, error) {
	items := make([]QueueItem, 0)
	for rows.Next() {
		var item QueueItem
		var album, trackID, lastError sql.NullString
		var duration sql.NullInt64
		var timestamp, nextAttempt int64
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Kind, &trackID, &item.Artist, &item.Track, &album,
			&duration, &timestamp, &item.Attempts, &lastError, &createdAt, &nextAttempt); err != nil {
			return nil, 0, err
		}
		item.TrackID = trackID.String
		item.Album = album.String
		item.DurationSeconds = int(duration.Int64)
		item.Timestamp = unixTime(timestamp)
		item.LastError = lastError.String
		if nextAttempt > 0 {
			next := unixTime(nextAttempt)
			item.NextAttemptAt = &next
		}
		if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
			item.CreatedAt = parsed
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func deferQueueItem(ctx context.Context, db *sql.DB, id int64, attempts int, retryAt time.Time, message string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE lastfm_scrobble_queue
		SET attempts = ?, last_error = ?, next_attempt_at = ?
		WHERE id = ?`, attempts, truncateError(message), retryAt.Unix(), id)
	if err != nil {
		return fmt.Errorf("defer last.fm queue item: %w", err)
	}
	return nil
}

func deleteQueueItem(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM lastfm_scrobble_queue WHERE id = ?`, id)
	return err
}

// releaseLedger un-claims a scrobble that will never be delivered, so a later
// play of the same track at the same second is not mistaken for a duplicate.
func releaseLedger(ctx context.Context, db *sql.DB, userID, dedupeKey string) error {
	if strings.TrimSpace(dedupeKey) == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM lastfm_scrobble_ledger WHERE user_id = ? AND dedupe_key = ?`, userID, dedupeKey)
	return err
}

func truncateError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

// ---------------------------------------------------------------------------
// submission audit log
// ---------------------------------------------------------------------------

func recordSubmission(ctx context.Context, db *sql.DB, userID, kind string, submission TrackSubmission, status, source string, err error) error {
	message := ""
	if err != nil {
		message = truncateError(err.Error())
	}
	_, execErr := db.ExecContext(ctx, `
		INSERT INTO lastfm_submissions (
			user_id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			timestamp, status, error, source, dedupe_key, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		strings.TrimSpace(submission.DedupeKey),
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
		item.Timestamp = unixTime(timestamp)
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

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func unixTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

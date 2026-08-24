package listenbrainz

// Persistence.
//
// Everything here mirrors the shape 0009 gave Last.fm, for the same reasons:
// measured plays survive a restart, a listen is claimed in a ledger before the
// network is touched, and the queue is a write-ahead log rather than a buffer.
// The differences are ListenBrainz's: a user row holds a token instead of a
// session key, and the queue carries the full MusicBrainz identifier set
// because this service can use all of it.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// user connection
// ---------------------------------------------------------------------------

type sessionRecord struct {
	UserID      string
	Username    string
	Token       string
	APIRoot     string
	ConnectedAt time.Time
}

func loadSession(ctx context.Context, db *sql.DB, userID string) (sessionRecord, error) {
	var record sessionRecord
	err := db.QueryRowContext(ctx, `
		SELECT username, user_token, api_root, connected_at
		FROM listenbrainz_user_settings
		WHERE user_id = ?`, userID).
		Scan(&record.Username, &record.Token, &record.APIRoot, &record.ConnectedAt)
	if err == sql.ErrNoRows {
		return sessionRecord{}, ErrNotConnected
	}
	if err != nil {
		return sessionRecord{}, fmt.Errorf("load listenbrainz session: %w", err)
	}
	if strings.TrimSpace(record.Token) == "" {
		return sessionRecord{}, ErrNotConnected
	}
	record.UserID = userID
	record.ConnectedAt = record.ConnectedAt.UTC()
	return record, nil
}

func saveSession(ctx context.Context, db *sql.DB, userID, username, token, apiRoot string) (sessionRecord, error) {
	connectedAt := time.Now().UTC()
	_, err := db.ExecContext(ctx, `
		INSERT INTO listenbrainz_user_settings (user_id, username, user_token, api_root, connected_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW())
		ON CONFLICT(user_id) DO UPDATE SET
			username = excluded.username,
			user_token = excluded.user_token,
			api_root = excluded.api_root,
			connected_at = excluded.connected_at,
			updated_at = NOW()`,
		userID, username, token, apiRoot, connectedAt)
	if err != nil {
		return sessionRecord{}, fmt.Errorf("save listenbrainz session: %w", err)
	}
	return sessionRecord{
		UserID:      userID,
		Username:    username,
		Token:       token,
		APIRoot:     apiRoot,
		ConnectedAt: connectedAt,
	}, nil
}

func deleteSession(ctx context.Context, db *sql.DB, userID string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM listenbrainz_user_settings WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete listenbrainz session: %w", err)
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
		FROM listenbrainz_plays
		WHERE user_id = ? AND track_id = ?`, userID, trackID).
		Scan(&playID, &startedAt, &lastPosition, &lastObservedAt, &lastAdvanceAt,
			&listened, &duration, &scrobbled, &closed)
	if err == sql.ErrNoRows {
		return play{UserID: userID, TrackID: trackID}, nil
	}
	if err != nil {
		return play{}, fmt.Errorf("load listenbrainz play: %w", err)
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
		INSERT INTO listenbrainz_plays (
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
		return fmt.Errorf("save listenbrainz play: %w", err)
	}
	return nil
}

// latestOtherAdvance returns when a track OTHER than trackID last credited real
// listening for this user, which is how a gapless client's prefetch of the next
// track is told apart from the one actually playing.
func latestOtherAdvance(ctx context.Context, db *sql.DB, userID, trackID string) (time.Time, error) {
	var advancedAt sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT MAX(last_advance_at) FROM listenbrainz_plays
		WHERE user_id = ? AND track_id <> ? AND closed = 0`, userID, trackID).Scan(&advancedAt)
	if err != nil && err != sql.ErrNoRows {
		return time.Time{}, fmt.Errorf("load listenbrainz play advance: %w", err)
	}
	if !advancedAt.Valid {
		return time.Time{}, nil
	}
	return unixTime(advancedAt.Int64), nil
}

func prunePlays(ctx context.Context, db *sql.DB, before time.Time) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM listenbrainz_plays WHERE updated_at < ?`, before.UTC()); err != nil {
		return fmt.Errorf("prune listenbrainz plays: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// playing-now pointer
// ---------------------------------------------------------------------------

func loadNowPlaying(ctx context.Context, db *sql.DB, userID string) (nowPlayingPointer, error) {
	var trackID, playID string
	var sentAt int64
	err := db.QueryRowContext(ctx, `
		SELECT track_id, play_id, sent_at FROM listenbrainz_now_playing WHERE user_id = ?`, userID).
		Scan(&trackID, &playID, &sentAt)
	if err == sql.ErrNoRows {
		return nowPlayingPointer{}, nil
	}
	if err != nil {
		return nowPlayingPointer{}, fmt.Errorf("load listenbrainz playing now: %w", err)
	}
	return nowPlayingPointer{TrackID: trackID, PlayID: playID, SentAt: unixTime(sentAt), Exists: true}, nil
}

func saveNowPlaying(ctx context.Context, db *sql.DB, userID string, pointer nowPlayingPointer) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO listenbrainz_now_playing (user_id, track_id, play_id, sent_at, updated_at)
		VALUES (?, ?, ?, ?, NOW())
		ON CONFLICT(user_id) DO UPDATE SET
			track_id = excluded.track_id,
			play_id = excluded.play_id,
			sent_at = excluded.sent_at,
			updated_at = NOW()`,
		userID, pointer.TrackID, pointer.PlayID, unixSeconds(pointer.SentAt))
	if err != nil {
		return fmt.Errorf("save listenbrainz playing now: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// queue
// ---------------------------------------------------------------------------

type queuedSubmission struct {
	ID              int64
	UserID          string
	Kind            string
	TrackID         string
	Artist          string
	Track           string
	Album           string
	AlbumArtist     string
	TrackNumber     int
	DurationSeconds int
	PlayedSeconds   int
	Timestamp       time.Time
	Attempts        int
	RecordingMBID   string
	ReleaseMBID     string
	ArtistMBID      string
	TrackMBID       string
	DedupeKey       string
	Source          string
}

func (q queuedSubmission) submission() TrackSubmission {
	return TrackSubmission{
		TrackID:              q.TrackID,
		Artist:               q.Artist,
		Track:                q.Track,
		Album:                q.Album,
		AlbumArtist:          q.AlbumArtist,
		TrackNumber:          q.TrackNumber,
		DurationSeconds:      q.DurationSeconds,
		PlayedSeconds:        q.PlayedSeconds,
		Timestamp:            q.Timestamp,
		MusicBrainzRecording: q.RecordingMBID,
		MusicBrainzRelease:   q.ReleaseMBID,
		MusicBrainzArtist:    q.ArtistMBID,
		MusicBrainzTrack:     q.TrackMBID,
		DedupeKey:            q.DedupeKey,
	}
}

// claimListen reserves a listen and writes it to the queue in one transaction.
//
// The ledger row is the exactly-once guarantee: whoever inserts it owns the
// listen, and every later attempt to claim the same play — a racing goroutine,
// a re-sent request, a replay after a crash — gets claimed=false. Writing the
// queue row in the same transaction is what makes the listen durable BEFORE
// ListenBrainz is contacted.
//
// leaseUntil hides the new row from every other flusher for the duration of the
// caller's own delivery attempt, so nobody sends a copy of something already in
// flight. It is set in the same INSERT rather than a follow-up UPDATE, which
// would leave a window where the row is visible and unclaimed.
func claimListen(ctx context.Context, db *sql.DB, userID string, submission TrackSubmission, source string, leaseUntil time.Time) (queuedSubmission, bool, error) {
	if strings.TrimSpace(submission.DedupeKey) == "" {
		return queuedSubmission{}, false, fmt.Errorf("listenbrainz claim requires a dedupe key")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return queuedSubmission{}, false, fmt.Errorf("begin listenbrainz claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO listenbrainz_listen_ledger (user_id, dedupe_key, track_id, artist, track, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, dedupe_key) DO NOTHING`,
		userID, submission.DedupeKey, strings.TrimSpace(submission.TrackID),
		submission.Artist, submission.Track, submission.Timestamp.Unix())
	if err != nil {
		return queuedSubmission{}, false, fmt.Errorf("claim listenbrainz listen: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return queuedSubmission{}, false, nil
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO listenbrainz_queue (
			user_id, kind, track_id, artist, track, album, album_artist, track_number,
			duration_seconds, played_seconds, timestamp,
			recording_mbid, release_mbid, artist_mbid, track_mbid,
			dedupe_key, source, next_attempt_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		userID, queueKindListen, strings.TrimSpace(submission.TrackID),
		submission.Artist, submission.Track, strings.TrimSpace(submission.Album),
		strings.TrimSpace(submission.AlbumArtist), submission.TrackNumber,
		submission.DurationSeconds, submission.PlayedSeconds, submission.Timestamp.Unix(),
		strings.TrimSpace(submission.MusicBrainzRecording), strings.TrimSpace(submission.MusicBrainzRelease),
		strings.TrimSpace(submission.MusicBrainzArtist), strings.TrimSpace(submission.MusicBrainzTrack),
		submission.DedupeKey, strings.TrimSpace(source), leaseUntil.Unix()).Scan(&id)
	if err != nil {
		return queuedSubmission{}, false, fmt.Errorf("enqueue listenbrainz listen: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return queuedSubmission{}, false, fmt.Errorf("commit listenbrainz claim: %w", err)
	}

	return queuedSubmission{
		ID:              id,
		UserID:          userID,
		Kind:            queueKindListen,
		TrackID:         submission.TrackID,
		Artist:          submission.Artist,
		Track:           submission.Track,
		Album:           submission.Album,
		AlbumArtist:     submission.AlbumArtist,
		TrackNumber:     submission.TrackNumber,
		DurationSeconds: submission.DurationSeconds,
		PlayedSeconds:   submission.PlayedSeconds,
		Timestamp:       submission.Timestamp,
		RecordingMBID:   submission.MusicBrainzRecording,
		ReleaseMBID:     submission.MusicBrainzRelease,
		ArtistMBID:      submission.MusicBrainzArtist,
		TrackMBID:       submission.MusicBrainzTrack,
		DedupeKey:       submission.DedupeKey,
		Source:          source,
	}, true, nil
}

// leasedColumns is the field order scanQueuedRows depends on. The lease is an
// UPDATE ... RETURNING over an aliased table, so every column is qualified.
const leasedColumns = `q.id, q.user_id, q.kind, q.track_id, q.artist, q.track, q.album,
	q.album_artist, q.track_number, q.duration_seconds, q.played_seconds, q.timestamp,
	q.attempts, q.recording_mbid, q.release_mbid, q.artist_mbid, q.track_mbid,
	q.dedupe_key, q.source`

// leaseDueQueue atomically hands the caller a batch of due items and hides them
// from every other flusher, so two pollers cannot deliver the same listen.
func leaseDueQueue(ctx context.Context, db *sql.DB, userID string, now, leaseUntil time.Time, limit int) ([]queuedSubmission, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		UPDATE listenbrainz_queue AS q
		SET next_attempt_at = ?
		FROM (
			SELECT id FROM listenbrainz_queue
			WHERE next_attempt_at <= ?`
	args := []any{leaseUntil.Unix(), now.Unix()}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	// Oldest listen first: a backlog delivered chronologically keeps a user's
	// ListenBrainz history coherent.
	query += `
			ORDER BY timestamp ASC, id ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		) AS due
		WHERE q.id = due.id
		RETURNING ` + leasedColumns
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lease listenbrainz queue: %w", err)
	}
	defer rows.Close()
	return scanQueuedRows(rows, limit)
}

func scanQueuedRows(rows *sql.Rows, capacity int) ([]queuedSubmission, error) {
	if capacity <= 0 {
		capacity = 16
	}
	items := make([]queuedSubmission, 0, capacity)
	for rows.Next() {
		var item queuedSubmission
		var timestamp int64
		var trackNumber, duration, played sql.NullInt64
		if err := rows.Scan(&item.ID, &item.UserID, &item.Kind, &item.TrackID, &item.Artist,
			&item.Track, &item.Album, &item.AlbumArtist, &trackNumber, &duration, &played,
			&timestamp, &item.Attempts, &item.RecordingMBID, &item.ReleaseMBID,
			&item.ArtistMBID, &item.TrackMBID, &item.DedupeKey, &item.Source); err != nil {
			return nil, fmt.Errorf("scan listenbrainz queue row: %w", err)
		}
		item.TrackNumber = int(trackNumber.Int64)
		item.DurationSeconds = int(duration.Int64)
		item.PlayedSeconds = int(played.Int64)
		item.Timestamp = unixTime(timestamp)
		items = append(items, item)
	}
	return items, rows.Err()
}

func deferQueueItem(ctx context.Context, db *sql.DB, id int64, attempts int, retryAt time.Time, message string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE listenbrainz_queue
		SET attempts = ?, next_attempt_at = ?, last_error = ?
		WHERE id = ?`, attempts, retryAt.Unix(), truncateError(message), id)
	if err != nil {
		return fmt.Errorf("defer listenbrainz queue item: %w", err)
	}
	return nil
}

func deleteQueueItem(ctx context.Context, db *sql.DB, id int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM listenbrainz_queue WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete listenbrainz queue item: %w", err)
	}
	return nil
}

// releaseLedger un-claims a listen that was dropped rather than delivered, so a
// later play of the same track is not mistaken for a duplicate of this one.
func releaseLedger(ctx context.Context, db *sql.DB, userID, dedupeKey string) error {
	if strings.TrimSpace(dedupeKey) == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM listenbrainz_listen_ledger WHERE user_id = ? AND dedupe_key = ?`, userID, dedupeKey)
	if err != nil {
		return fmt.Errorf("release listenbrainz ledger: %w", err)
	}
	return nil
}

// resetQueueBackoff makes every held submission due immediately, for when the
// reason they were failing has plainly been fixed — reconnecting, say.
//
// It touches only rows that have already failed at least once. A row with no
// attempts yet is inside the lease of a delivery happening right now, and
// making it due would let a concurrent flush send a second copy.
func resetQueueBackoff(ctx context.Context, db *sql.DB, userID string) error {
	query := `UPDATE listenbrainz_queue SET next_attempt_at = 0 WHERE attempts > 0`
	var args []any
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("reset listenbrainz queue backoff: %w", err)
	}
	return nil
}

func countQueue(ctx context.Context, db *sql.DB, userID string) (int, error) {
	var total int
	var err error
	if userID == "" {
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM listenbrainz_queue`).Scan(&total)
	} else {
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM listenbrainz_queue WHERE user_id = ?`, userID).Scan(&total)
	}
	if err != nil {
		return 0, fmt.Errorf("count listenbrainz queue: %w", err)
	}
	return total, nil
}

func listQueuePage(ctx context.Context, db *sql.DB, userID string, limit, offset int) ([]QueueItem, int, error) {
	limit, offset = normalizePage(limit, offset)
	total, err := countQueue(ctx, db, userID)
	if err != nil {
		return nil, 0, err
	}
	const columns = `id, kind, track_id, artist, track, album, duration_seconds,
		timestamp, attempts, last_error, next_attempt_at, created_at`
	var rows *sql.Rows
	if userID == "" {
		rows, err = db.QueryContext(ctx, `SELECT `+columns+`
			FROM listenbrainz_queue ORDER BY id ASC LIMIT ? OFFSET ?`, limit, offset)
	} else {
		rows, err = db.QueryContext(ctx, `SELECT `+columns+`
			FROM listenbrainz_queue WHERE user_id = ? ORDER BY id ASC LIMIT ? OFFSET ?`, userID, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list listenbrainz queue: %w", err)
	}
	defer rows.Close()

	items := make([]QueueItem, 0)
	for rows.Next() {
		var item QueueItem
		var duration sql.NullInt64
		var timestamp, nextAttemptAt int64
		if err := rows.Scan(&item.ID, &item.Kind, &item.TrackID, &item.Artist, &item.Track,
			&item.Album, &duration, &timestamp, &item.Attempts, &item.LastError,
			&nextAttemptAt, &item.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan listenbrainz queue page: %w", err)
		}
		item.DurationSeconds = int(duration.Int64)
		item.Timestamp = unixTime(timestamp)
		item.CreatedAt = item.CreatedAt.UTC()
		if next := unixTime(nextAttemptAt); !next.IsZero() {
			item.NextAttemptAt = &next
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// ---------------------------------------------------------------------------
// submission audit trail
// ---------------------------------------------------------------------------

func recordSubmission(ctx context.Context, db *sql.DB, userID, kind string, submission TrackSubmission, status, source string, cause error) error {
	message := ""
	if cause != nil {
		message = truncateError(cause.Error())
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO listenbrainz_submissions (
			user_id, kind, track_id, artist, track, album, duration_seconds, played_seconds,
			timestamp, status, error, source, dedupe_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, kind, strings.TrimSpace(submission.TrackID),
		submission.Artist, submission.Track, strings.TrimSpace(submission.Album),
		submission.DurationSeconds, submission.PlayedSeconds, submission.Timestamp.Unix(),
		status, message, strings.TrimSpace(source), strings.TrimSpace(submission.DedupeKey))
	if err != nil {
		return fmt.Errorf("record listenbrainz submission: %w", err)
	}
	return nil
}

func listSubmissionHistory(ctx context.Context, db *sql.DB, userID string, limit, offset int) ([]SubmissionRecord, int, error) {
	limit, offset = normalizePage(limit, offset)
	const columns = `id, kind, track_id, artist, track, album, duration_seconds,
		played_seconds, timestamp, status, error, source, created_at`
	var (
		total int
		rows  *sql.Rows
		err   error
	)
	if userID == "" {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM listenbrainz_submissions`).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `SELECT `+columns+`
			FROM listenbrainz_submissions ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	} else {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM listenbrainz_submissions WHERE user_id = ?`, userID).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = db.QueryContext(ctx, `SELECT `+columns+`
			FROM listenbrainz_submissions WHERE user_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list listenbrainz history: %w", err)
	}
	defer rows.Close()

	items := make([]SubmissionRecord, 0)
	for rows.Next() {
		var item SubmissionRecord
		var duration, played sql.NullInt64
		var timestamp int64
		if err := rows.Scan(&item.ID, &item.Kind, &item.TrackID, &item.Artist, &item.Track,
			&item.Album, &duration, &played, &timestamp, &item.Status, &item.Error,
			&item.Source, &item.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan listenbrainz history: %w", err)
		}
		item.DurationSeconds = int(duration.Int64)
		item.PlayedSeconds = int(played.Int64)
		item.Timestamp = unixTime(timestamp)
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func truncateError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		return message[:500]
	}
	return message
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

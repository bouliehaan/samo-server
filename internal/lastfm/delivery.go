package lastfm

// Delivery: getting a claimed submission to Last.fm, eventually, without ever
// sending it twice.
//
// A scrobble is written to the queue and the idempotency ledger in one
// transaction BEFORE Last.fm is contacted, so nothing is lost to a crash, a
// restart, or an outage. Delivery then drains that queue: an inline attempt
// first (so a healthy server scrobbles immediately), and a background poller
// for everything that did not go through.
//
// The retry schedule matters as much as the queue. The old policy allowed eight
// attempts one minute apart and then discarded the scrobble — a nine-minute
// network outage silently deleted every listen in it. The schedule below backs
// off geometrically and keeps trying for as long as Last.fm will still accept
// the timestamp.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

const (
	// Delay before a freshly claimed item becomes visible to the poller. The
	// inline attempt owns it until then, which is what stops the poller from
	// sending a copy of something already in flight.
	inlineLease = 45 * time.Second

	// How long a leased item stays hidden from other flushers.
	flushLease = 2 * time.Minute

	queueBaseDelay = 30 * time.Second
	queueMaxDelay  = 2 * time.Hour

	// A disconnected account or bad credentials are not fixed by hammering.
	authRetryDelay   = 15 * time.Minute
	configRetryDelay = 30 * time.Minute

	// Pacing between upstream calls while draining a backlog, well inside
	// Last.fm's request budget.
	flushPacing = 150 * time.Millisecond
)

// retryDelay is the geometric backoff for transient failures: 30s, 1m, 2m, 4m,
// ... capped at two hours, with jitter so a queue that failed together does not
// retry together.
func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := queueBaseDelay
	for i := 1; i < attempts && delay < queueMaxDelay; i++ {
		delay *= 2
	}
	if delay > queueMaxDelay {
		delay = queueMaxDelay
	}
	jitter := time.Duration(rand.Int64N(int64(delay / 4)))
	return delay + jitter
}

// scrobble claims a play and tries to deliver it at once.
//
// Returns queued=true when the listen is durably stored but not yet accepted
// upstream, owned=true when this caller won the claim (owned=false means the
// same play was already scrobbled and this call did nothing).
func (s *Service) scrobble(ctx context.Context, userID string, submission TrackSubmission, source string) (queued bool, owned bool, err error) {
	item, claimed, err := claimScrobble(ctx, s.db, userID, submission, source, s.clock().Add(inlineLease))
	if err != nil {
		return false, false, err
	}
	if !claimed {
		s.logger("last.fm scrobble already recorded for %q by %q; not sending again", submission.Track, submission.Artist)
		return false, false, nil
	}
	delivered := s.deliverItem(ctx, item)
	return !delivered, true, nil
}

func (s *Service) submitLove(ctx context.Context, userID, kind string, submission TrackSubmission) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	item, err := enqueueLove(ctx, s.db, userID, kind, submission, "playback", s.clock().Add(inlineLease))
	if err != nil {
		return err
	}
	s.deliverItem(ctx, item)
	return nil
}

// deliverItem sends one queued item and settles its row: deleted on success,
// rescheduled on a retryable failure, dropped only when retrying can never
// help. It reports whether the item reached Last.fm.
func (s *Service) deliverItem(ctx context.Context, item queuedSubmission) bool {
	now := s.clock()
	submission := item.submission()

	// Last.fm refuses scrobbles older than two weeks, so a listen that has been
	// undeliverable that long never will be. Record it as dropped rather than
	// retrying forever.
	if item.Kind == queueKindScrobble && !submission.Timestamp.IsZero() && now.Sub(submission.Timestamp) > maxScrobbleAge {
		s.dropItem(ctx, item, fmt.Errorf("scrobble timestamp is older than last.fm accepts (%s)", submission.Timestamp.Format(time.RFC3339)))
		return false
	}

	session, err := loadSession(ctx, s.db, item.UserID)
	if err != nil {
		// Not connected: hold the work until the account is linked again.
		s.rescheduleItem(ctx, item, err, authRetryDelay)
		return false
	}

	err = s.dispatch(ctx, session.SessionKey, item.Kind, submission)

	// A MusicBrainz id Last.fm cannot resolve makes it reject an otherwise fine
	// scrobble. The plain artist/track form always matches, so fall back to it
	// rather than losing the listen.
	var ignored *IgnoredError
	if errors.As(err, &ignored) && (ignored.Code == 1 || ignored.Code == 2) &&
		strings.TrimSpace(submission.MusicBrainzRecording) != "" {
		s.logger("last.fm ignored %q with mbid (%s); retrying without it", submission.Track, ignored.Message)
		retry := submission
		retry.MusicBrainzRecording = ""
		err = s.dispatch(ctx, session.SessionKey, item.Kind, retry)
		if err == nil {
			submission = retry
		}
	}

	if err == nil {
		if delErr := deleteQueueItem(ctx, s.db, item.ID); delErr != nil {
			s.logger("last.fm queue cleanup failed: %v", delErr)
		}
		_ = recordSubmission(ctx, s.db, item.UserID, item.Kind, submission, submissionStatusSubmitted, deliverySource(item), nil)
		return true
	}

	switch classify(err) {
	case classAuth:
		if isSessionRejection(err) {
			s.invalidateSession(ctx, item.UserID, err)
		}
		s.rescheduleItem(ctx, item, err, authRetryDelay)
	case classConfig:
		s.rescheduleItem(ctx, item, err, configRetryDelay)
	case classPermanent:
		s.dropItem(ctx, item, err)
	default:
		s.rescheduleItem(ctx, item, err, retryDelay(item.Attempts+1))
	}
	return false
}

func (s *Service) dispatch(ctx context.Context, sessionKey, kind string, submission TrackSubmission) error {
	client, ok := s.activeClient()
	if !ok {
		return ErrDisabled
	}
	switch kind {
	case queueKindScrobble:
		return client.Scrobble(ctx, sessionKey, submission)
	case queueKindLove:
		return client.LoveTrack(ctx, sessionKey, submission)
	case queueKindUnlove:
		return client.UnloveTrack(ctx, sessionKey, submission)
	case queueKindNowPlaying:
		// Never queued any more; tolerated so rows written by older builds drain.
		return client.UpdateNowPlaying(ctx, sessionKey, submission)
	default:
		return fmt.Errorf("unknown queue kind %q", kind)
	}
}

func (s *Service) rescheduleItem(ctx context.Context, item queuedSubmission, cause error, delay time.Duration) {
	attempts := item.Attempts + 1
	retryAt := s.clock().Add(delay)
	if err := deferQueueItem(ctx, s.db, item.ID, attempts, retryAt, cause.Error()); err != nil {
		s.logger("last.fm queue reschedule failed: %v", err)
		return
	}
	if attempts == 1 {
		// One audit row when something first fails, not one per retry.
		_ = recordSubmission(ctx, s.db, item.UserID, item.Kind, item.submission(), submissionStatusQueued, deliverySource(item), cause)
	}
	s.logger("last.fm %s for %q deferred to %s (attempt %d): %v",
		item.Kind, item.Track, retryAt.Format(time.RFC3339), attempts, cause)
}

func (s *Service) dropItem(ctx context.Context, item queuedSubmission, cause error) {
	s.logger("last.fm dropping %s for %q: %v", item.Kind, item.Track, cause)
	if err := deleteQueueItem(ctx, s.db, item.ID); err != nil {
		s.logger("last.fm queue cleanup failed: %v", err)
	}
	_ = recordSubmission(ctx, s.db, item.UserID, item.Kind, item.submission(), submissionStatusDropped, deliverySource(item), cause)
	// The ledger entry stays: a scrobble Last.fm will never accept must not be
	// re-claimed and re-attempted the next time the same play is evaluated.
}

func deliverySource(item queuedSubmission) string {
	if source := strings.TrimSpace(item.Source); source != "" {
		return source
	}
	return "queue"
}

// FlushQueue delivers due submissions. Pass userID "" for every user.
func (s *Service) FlushQueue(ctx context.Context, userID string, limit int) (int, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}
	if limit <= 0 {
		limit = 50
	}
	now := s.clock()
	items, err := leaseDueQueue(ctx, s.db, userID, now, now.Add(flushLease), limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for index, item := range items {
		if ctx.Err() != nil {
			return delivered, ctx.Err()
		}
		if index > 0 {
			s.pause(flushPacing)
		}
		if s.deliverItem(ctx, item) {
			delivered++
		}
	}
	return delivered, nil
}

// RetryQueue is the "try again now" the manual flush endpoint means: it clears
// the backoff on everything held for this user and then drains. Without the
// reset, pressing retry during a long backoff would appear to do nothing.
func (s *Service) RetryQueue(ctx context.Context, userID string, limit int) (int, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}
	if err := resetQueueBackoff(ctx, s.db, userID); err != nil {
		return 0, err
	}
	return s.DrainQueue(ctx, userID, limit, 20)
}

// DrainQueue repeatedly flushes until nothing is due, so a backlog left by an
// outage clears in one pass instead of one batch per poller tick.
func (s *Service) DrainQueue(ctx context.Context, userID string, batch, maxBatches int) (int, error) {
	if maxBatches <= 0 {
		maxBatches = 20
	}
	total := 0
	for i := 0; i < maxBatches; i++ {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		before := total
		flushed, err := s.FlushQueue(ctx, userID, batch)
		total += flushed
		if err != nil {
			return total, err
		}
		if flushed == 0 && total == before {
			// Either the queue is empty or everything left is deferred.
			return total, nil
		}
	}
	return total, nil
}

func (s *Service) pause(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	<-timer.C
}

// PrunePlays discards finished play bookkeeping that can no longer influence a
// decision.
func (s *Service) PrunePlays(ctx context.Context, olderThan time.Duration) error {
	if s == nil || s.db == nil {
		return nil
	}
	if olderThan <= 0 {
		olderThan = 30 * 24 * time.Hour
	}
	return prunePlays(ctx, s.db, s.clock().Add(-olderThan))
}

// leaseDueQueue atomically takes ownership of the next due items by pushing
// their retry time out, so two flushes — a poller tick and a manual flush, or
// two server processes — can never deliver the same row twice.
func leaseDueQueue(ctx context.Context, db *sql.DB, userID string, now, leaseUntil time.Time, limit int) ([]queuedSubmission, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		UPDATE lastfm_scrobble_queue AS q
		SET next_attempt_at = ?
		FROM (
			SELECT id FROM lastfm_scrobble_queue
			WHERE next_attempt_at <= ?`
	args := []any{leaseUntil.Unix(), now.Unix()}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += `
			ORDER BY timestamp ASC, id ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		) AS due
		WHERE q.id = due.id
		RETURNING q.id, q.user_id, q.kind, q.track_id, q.artist, q.track, q.album,
		          q.duration_seconds, q.played_seconds, q.timestamp, q.attempts,
		          q.mbid, q.dedupe_key, q.source`
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lease last.fm queue: %w", err)
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

// resetQueueBackoff makes every held submission due immediately. Used when the
// reason they were failing has plainly been fixed, such as reconnecting.
//
// It touches only rows that have already failed at least once. A row with no
// attempts yet is inside the lease of a delivery happening right now, and
// making it due would let a concurrent flush send a second copy.
func resetQueueBackoff(ctx context.Context, db *sql.DB, userID string) error {
	query := `UPDATE lastfm_scrobble_queue SET next_attempt_at = 0 WHERE attempts > 0`
	var args []any
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("reset last.fm queue backoff: %w", err)
	}
	return nil
}

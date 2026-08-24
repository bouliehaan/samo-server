package listenbrainz

// Delivery: getting a claimed listen to ListenBrainz, eventually, without ever
// sending it twice.
//
// A listen is written to the queue and the idempotency ledger in one
// transaction BEFORE the network is touched, so nothing is lost to a crash, a
// restart, or an outage. Delivery then drains that queue: an inline attempt
// first, so a healthy server submits immediately, and a background poller for
// everything that did not go through.
//
// ListenBrainz takes up to 1000 listens per request, so a backlog goes out in
// batches rather than one call per listen. That introduces the one failure mode
// a per-item queue does not have: a single unacceptable listen would fail the
// whole batch, and retrying the batch forever would wedge every listen behind
// it. deliverBatch handles that by falling back to individual submission the
// moment a batch is rejected as permanently bad, which isolates the poison pill
// to its own row.

import (
	"context"
	"errors"
	"time"

	"github.com/bouliehaan/samo-server/internal/scrobble"
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

	// A disconnected account or a revoked token is not fixed by hammering.
	authRetryDelay = 15 * time.Minute

	// Listens per submit-listens call. Far below ListenBrainz's limit of 1000:
	// a smaller batch keeps any single rejection cheap to isolate, and keeps
	// the request well inside the payload size cap.
	maxBatchListens = 50

	// Pacing between upstream calls while draining a backlog.
	flushPacing = 250 * time.Millisecond
)

// retryDelay applies ListenBrainz's backoff bounds to the shared schedule.
func retryDelay(attempts int) time.Duration {
	return scrobble.RetryDelay(attempts, queueBaseDelay, queueMaxDelay)
}

// submitListen claims a listen and tries to deliver it at once.
//
// Returns queued=true when the listen is durably stored but not yet accepted
// upstream, owned=true when this caller won the claim (owned=false means the
// same play was already submitted and this call did nothing).
func (s *Service) submitListen(ctx context.Context, userID string, submission TrackSubmission, source string) (queued bool, owned bool, err error) {
	item, claimed, err := claimListen(ctx, s.db, userID, submission, source, s.clock().Add(inlineLease))
	if err != nil {
		return false, false, err
	}
	if !claimed {
		s.logger("listenbrainz listen already recorded for %q by %q; not sending again", submission.Track, submission.Artist)
		return false, false, nil
	}
	delivered := s.deliverItem(ctx, item)
	return !delivered, true, nil
}

// deliverItem sends one queued listen and settles its row: deleted on success,
// rescheduled on a retryable failure, dropped only when retrying can never
// help. It reports whether the listen reached ListenBrainz.
func (s *Service) deliverItem(ctx context.Context, item queuedSubmission) bool {
	submission := item.submission()

	client, err := s.clientFor(ctx, item.UserID)
	if err != nil {
		s.rescheduleItem(ctx, item, err, authRetryDelay)
		return false
	}

	err = client.SubmitListens(ctx, []TrackSubmission{submission})
	if err == nil {
		if delErr := deleteQueueItem(ctx, s.db, item.ID); delErr != nil {
			s.logger("listenbrainz queue cleanup failed for %d: %v", item.ID, delErr)
		}
		s.record(ctx, item.UserID, item.Kind, submission, submissionStatusSubmitted, item.Source, nil)
		return true
	}

	switch classify(err) {
	case classPermanent:
		s.dropItem(ctx, item, err)
	case classAuth:
		s.noteAuthFailure(ctx, item.UserID, err)
		s.rescheduleItem(ctx, item, err, authRetryDelay)
	default:
		delay := retryAfter(err)
		if delay <= 0 {
			delay = retryDelay(item.Attempts + 1)
		}
		s.rescheduleItem(ctx, item, err, delay)
	}
	return false
}

// deliverBatch submits several listens in one request.
//
// On a permanent rejection it retries the items one at a time. That costs a
// round trip per listen exactly once, and in exchange a single malformed row
// can never hold the rest of the queue hostage.
func (s *Service) deliverBatch(ctx context.Context, userID string, items []queuedSubmission) int {
	if len(items) == 0 {
		return 0
	}
	if len(items) == 1 {
		if s.deliverItem(ctx, items[0]) {
			return 1
		}
		return 0
	}

	client, err := s.clientFor(ctx, userID)
	if err != nil {
		for _, item := range items {
			s.rescheduleItem(ctx, item, err, authRetryDelay)
		}
		return 0
	}

	submissions := make([]TrackSubmission, 0, len(items))
	for _, item := range items {
		submissions = append(submissions, item.submission())
	}

	err = client.SubmitListens(ctx, submissions)
	if err == nil {
		for _, item := range items {
			if delErr := deleteQueueItem(ctx, s.db, item.ID); delErr != nil {
				s.logger("listenbrainz queue cleanup failed for %d: %v", item.ID, delErr)
			}
			s.record(ctx, item.UserID, item.Kind, item.submission(), submissionStatusSubmitted, item.Source, nil)
		}
		return len(items)
	}

	switch classify(err) {
	case classPermanent:
		// One of these is bad and the batch cannot say which. Retry them
		// individually so only the offender is dropped.
		s.logger("listenbrainz batch of %d rejected (%v); retrying individually", len(items), err)
		delivered := 0
		for _, item := range items {
			if s.deliverItem(ctx, item) {
				delivered++
			}
			s.pause(flushPacing)
		}
		return delivered
	case classAuth:
		s.noteAuthFailure(ctx, userID, err)
		for _, item := range items {
			s.rescheduleItem(ctx, item, err, authRetryDelay)
		}
	default:
		delay := retryAfter(err)
		for _, item := range items {
			itemDelay := delay
			if itemDelay <= 0 {
				itemDelay = retryDelay(item.Attempts + 1)
			}
			s.rescheduleItem(ctx, item, err, itemDelay)
		}
	}
	return 0
}

func (s *Service) rescheduleItem(ctx context.Context, item queuedSubmission, cause error, delay time.Duration) {
	attempts := item.Attempts + 1
	retryAt := s.clock().Add(delay)
	if err := deferQueueItem(ctx, s.db, item.ID, attempts, retryAt, cause.Error()); err != nil {
		s.logger("listenbrainz queue reschedule failed for %d: %v", item.ID, err)
	}
	s.record(ctx, item.UserID, item.Kind, item.submission(), submissionStatusQueued, item.Source, cause)
}

// dropItem abandons a listen that can never be accepted. The ledger claim is
// released with it, so a later play of the same track is not mistaken for a
// duplicate of the one being discarded here.
func (s *Service) dropItem(ctx context.Context, item queuedSubmission, cause error) {
	if err := deleteQueueItem(ctx, s.db, item.ID); err != nil {
		s.logger("listenbrainz queue drop failed for %d: %v", item.ID, err)
	}
	if err := releaseLedger(ctx, s.db, item.UserID, item.DedupeKey); err != nil {
		s.logger("listenbrainz ledger release failed for %d: %v", item.ID, err)
	}
	s.logger("listenbrainz dropping %q by %q: %v", item.Track, item.Artist, cause)
	s.record(ctx, item.UserID, item.Kind, item.submission(), submissionStatusDropped, item.Source, cause)
}

func (s *Service) record(ctx context.Context, userID, kind string, submission TrackSubmission, status, source string, cause error) {
	if err := recordSubmission(ctx, s.db, userID, kind, submission, status, source, cause); err != nil {
		s.logger("listenbrainz submission audit failed: %v", err)
	}
}

// FlushQueue delivers one batch of due listens and reports how many landed.
func (s *Service) FlushQueue(ctx context.Context, userID string, limit int) (int, error) {
	if s == nil || s.db == nil {
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
	if len(items) == 0 {
		return 0, nil
	}

	// Batch per user: one token authenticates one account, so listens for
	// different users can never share a request.
	delivered := 0
	for _, group := range groupByUser(items) {
		for start := 0; start < len(group.items); start += maxBatchListens {
			end := min(start+maxBatchListens, len(group.items))
			delivered += s.deliverBatch(ctx, group.userID, group.items[start:end])
			if end < len(group.items) {
				s.pause(flushPacing)
			}
		}
	}
	return delivered, nil
}

// RetryQueue makes held listens due immediately and then flushes them. Used
// when the cause of the failures has plainly been fixed.
func (s *Service) RetryQueue(ctx context.Context, userID string, limit int) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrDisabled
	}
	if err := resetQueueBackoff(ctx, s.db, userID); err != nil {
		return 0, err
	}
	return s.DrainQueue(ctx, userID, limit, 20)
}

// DrainQueue keeps flushing until the queue stops yielding work.
func (s *Service) DrainQueue(ctx context.Context, userID string, batch, maxBatches int) (int, error) {
	if batch <= 0 {
		batch = 50
	}
	if maxBatches <= 0 {
		maxBatches = 10
	}
	total := 0
	for i := 0; i < maxBatches; i++ {
		flushed, err := s.FlushQueue(ctx, userID, batch)
		total += flushed
		if err != nil {
			return total, err
		}
		if flushed == 0 {
			return total, nil
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		s.pause(flushPacing)
	}
	return total, nil
}

type userGroup struct {
	userID string
	items  []queuedSubmission
}

// groupByUser preserves the order the queue handed items back in, so a
// backlog still goes out oldest-first within each account.
func groupByUser(items []queuedSubmission) []userGroup {
	groups := make([]userGroup, 0, 4)
	index := make(map[string]int, 4)
	for _, item := range items {
		at, ok := index[item.UserID]
		if !ok {
			index[item.UserID] = len(groups)
			groups = append(groups, userGroup{userID: item.UserID, items: []queuedSubmission{item}})
			continue
		}
		groups[at].items = append(groups[at].items, item)
	}
	return groups
}

func (s *Service) pause(d time.Duration) {
	if s.sleep != nil {
		s.sleep(d)
		return
	}
	time.Sleep(d)
}

// PrunePlays discards play state that can no longer earn a listen.
func (s *Service) PrunePlays(ctx context.Context, olderThan time.Duration) error {
	if s == nil || s.db == nil {
		return nil
	}
	if olderThan <= 0 {
		olderThan = 30 * 24 * time.Hour
	}
	return prunePlays(ctx, s.db, s.clock().Add(-olderThan))
}

// noteAuthFailure records that a user's token stopped working. The
// token is deliberately NOT deleted: the queue keeps the listens, and the user
// only has to paste a fresh token to have the backlog delivered.
func (s *Service) noteAuthFailure(ctx context.Context, userID string, cause error) {
	if errors.Is(cause, ErrNotConnected) {
		return
	}
	s.logger("listenbrainz token rejected for user %s: %v", userID, cause)
}

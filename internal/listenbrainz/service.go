package listenbrainz

// The service: everything durable that wraps the shared listen engine.
//
// The engine (internal/scrobble) decides whether a play has been listened to.
// This file does the rest — load the play, fold in the observation, claim and
// deliver anything the engine says was earned, and keep "playing now" honest.
//
// Unlike Last.fm there is no application credential to gate on, so a user with
// a token is fully operational on a server that was never configured for
// anything. That is the whole point of ListenBrainz being here.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/scrobble"
)

type Service struct {
	db             *sql.DB
	httpClient     *http.Client
	defaultAPIRoot string
	logger         func(format string, args ...any)

	// clock and playID are indirected so the listen engine can be driven
	// deterministically in tests.
	clock  func() time.Time
	playID func() string
	// sleep replaces time.Sleep in tests so draining a backlog is instant.
	sleep func(time.Duration)

	playbackLocks sync.Map // per-user *sync.Mutex serializing playback observations
}

type ServiceOptions struct {
	DB *sql.DB
	// APIRoot is the instance used when a user has not chosen one.
	APIRoot    string
	HTTPClient *http.Client
	Logger     func(format string, args ...any)
	Now        func() time.Time
	NewPlayID  func() string
	Sleep      func(time.Duration)
}

func NewService(options ServiceOptions) *Service {
	logger := options.Logger
	if logger == nil {
		logger = log.Printf
	}
	clock := options.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	playID := options.NewPlayID
	if playID == nil {
		playID = newPlayToken
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Service{
		db:             options.DB,
		httpClient:     httpClient,
		defaultAPIRoot: NormalizeAPIRoot(options.APIRoot),
		logger:         logger,
		clock:          clock,
		playID:         playID,
		sleep:          options.Sleep,
	}
}

// Enabled reports whether the integration can run at all.
//
// For Last.fm this asks whether an operator supplied an application key. There
// is no such thing here: ListenBrainz needs nothing but a per-user token, so
// the integration is available on any server with a database and "connected"
// is a per-user question answered by Status.
func (s *Service) Enabled() bool {
	return s != nil && s.db != nil
}

// DefaultAPIRoot is the instance new connections use unless they name another.
func (s *Service) DefaultAPIRoot() string {
	if s == nil || s.defaultAPIRoot == "" {
		return DefaultAPIRoot
	}
	return s.defaultAPIRoot
}

func (s *Service) playbackMutex(userID string) *sync.Mutex {
	value, _ := s.playbackLocks.LoadOrStore(userID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// clientFor builds the client for one user's stored connection.
func (s *Service) clientFor(ctx context.Context, userID string) (*Client, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	session, err := loadSession(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	root := session.APIRoot
	if strings.TrimSpace(root) == "" {
		root = s.DefaultAPIRoot()
	}
	return NewClient(root, session.Token, s.httpClient), nil
}

// ---------------------------------------------------------------------------
// connection
// ---------------------------------------------------------------------------

func (s *Service) Status(ctx context.Context, userID string) (Status, error) {
	if !s.Enabled() {
		return Status{}, nil
	}
	status := Status{Enabled: true, APIRoot: s.DefaultAPIRoot()}
	session, err := loadSession(ctx, s.db, userID)
	if err != nil {
		if errors.Is(err, ErrNotConnected) {
			return status, nil
		}
		return status, err
	}
	status.Connected = true
	status.Username = session.Username
	if strings.TrimSpace(session.APIRoot) != "" {
		status.APIRoot = session.APIRoot
	}
	if !session.ConnectedAt.IsZero() {
		connectedAt := session.ConnectedAt
		status.ConnectedAt = &connectedAt
	}
	if size, err := countQueue(ctx, s.db, userID); err == nil {
		status.QueueSize = size
	}
	return status, nil
}

// Connect validates a pasted token against the instance it belongs to and
// stores it. Validation is not optional: a token typo would otherwise be
// discovered only after a day of listens had silently failed to deliver.
func (s *Service) Connect(ctx context.Context, userID string, input ConnectInput) (ConnectResponse, error) {
	if !s.Enabled() {
		return ConnectResponse{}, ErrDisabled
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		return ConnectResponse{}, ErrMissingToken
	}
	root, err := ValidateAPIRoot(input.APIRoot)
	if err != nil {
		return ConnectResponse{}, err
	}
	effectiveRoot := root
	if effectiveRoot == "" {
		effectiveRoot = s.DefaultAPIRoot()
	}

	client := NewClient(effectiveRoot, token, s.httpClient)
	username, err := client.ValidateToken(ctx)
	if err != nil {
		return ConnectResponse{}, err
	}

	// Store the root exactly as chosen — blank means "follow the server
	// default", so an operator moving the default carries these users with it.
	session, err := saveSession(ctx, s.db, userID, username, token, root)
	if err != nil {
		return ConnectResponse{}, err
	}

	// A reconnection is the usual fix for a token that had been rejected, so
	// give the backlog an immediate chance rather than waiting out its backoff.
	if err := resetQueueBackoff(ctx, s.db, userID); err != nil {
		s.logger("listenbrainz queue backoff reset failed: %v", err)
	}

	return ConnectResponse{
		Username:    username,
		APIRoot:     effectiveRoot,
		Connected:   true,
		ConnectedAt: session.ConnectedAt,
	}, nil
}

// Disconnect forgets a user's token. Queued listens are deliberately kept: the
// usual reason to disconnect is to paste a fresh token, and discarding the
// backlog would lose listens the user already earned.
func (s *Service) Disconnect(ctx context.Context, userID string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	return deleteSession(ctx, s.db, userID)
}

func (s *Service) ListQueue(ctx context.Context, userID string, limit, offset int) (QueuePage, error) {
	if !s.Enabled() {
		return QueuePage{}, ErrDisabled
	}
	items, total, err := listQueuePage(ctx, s.db, userID, limit, offset)
	if err != nil {
		return QueuePage{}, err
	}
	return QueuePage{Items: items, Total: total}, nil
}

func (s *Service) ListHistory(ctx context.Context, userID string, limit, offset int) (HistoryPage, error) {
	if !s.Enabled() {
		return HistoryPage{}, ErrDisabled
	}
	items, total, err := listSubmissionHistory(ctx, s.db, userID, limit, offset)
	if err != nil {
		return HistoryPage{}, err
	}
	return HistoryPage{Items: items, Total: total}, nil
}

// ---------------------------------------------------------------------------
// playback
// ---------------------------------------------------------------------------

// HandlePlayback is the single entry point for every automatic trigger.
func (s *Service) HandlePlayback(ctx context.Context, input PlaybackInput) {
	s.processPlayback(ctx, input, input.DurationSeconds)
}

func (s *Service) HandleStreamStart(ctx context.Context, userID string, track catalog.MusicTrack, resumeSeconds int) {
	s.HandlePlayback(ctx, PlaybackInput{
		UserID:        userID,
		Track:         track,
		Source:        scrobble.SourceStream,
		ResumeSeconds: resumeSeconds,
		After:         catalog.PlaybackState{ProgressSeconds: resumeSeconds},
		ObservedAt:    s.clock(),
	})
}

func (s *Service) HandlePlaybackUpdate(
	ctx context.Context,
	userID string,
	track catalog.MusicTrack,
	before catalog.PlaybackState,
	after catalog.PlaybackState,
	patch playback.PatchInput,
) {
	s.HandlePlayback(ctx, PlaybackInput{
		UserID:     userID,
		Track:      track,
		Before:     before,
		After:      after,
		Patch:      &patch,
		Source:     "playback-patch",
		ObservedAt: s.clock(),
	})
}

func (s *Service) HandlePlaybackPut(
	ctx context.Context,
	userID string,
	track catalog.MusicTrack,
	before catalog.PlaybackState,
	after catalog.PlaybackState,
) {
	s.HandlePlayback(ctx, PlaybackInput{
		UserID:     userID,
		Track:      track,
		Before:     before,
		After:      after,
		Source:     "playback-put",
		ObservedAt: s.clock(),
	})
}

func (s *Service) HandleScrobbleEvent(ctx context.Context, userID string, track catalog.MusicTrack, input EventInput) (EventResponse, error) {
	event, err := scrobble.ParseEvent(input.Event)
	if err != nil {
		return EventResponse{}, err
	}
	result := s.processPlayback(ctx, PlaybackInput{
		UserID:     userID,
		Track:      track,
		After:      catalog.PlaybackState{ProgressSeconds: input.ProgressSeconds},
		Source:     "scrobble-event",
		Event:      event,
		ObservedAt: s.clock(),
		StartedAt:  input.StartedAt,
	}, input.DurationSeconds)
	return EventResponse{
		TrackID:         track.ID,
		Event:           string(event),
		NowPlaying:      result.NowPlaying,
		Scrobbled:       result.Scrobbled,
		Queued:          result.Queued,
		ProgressSeconds: input.ProgressSeconds,
	}, nil
}

func (s *Service) processPlayback(ctx context.Context, input PlaybackInput, durationOverride int) playbackResult {
	result := playbackResult{}
	if !s.Enabled() || strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.Track.ID) == "" {
		return result
	}
	// No token means nothing to deliver to, and measuring a play we could never
	// submit would only accumulate dead rows.
	if _, err := loadSession(ctx, s.db, input.UserID); err != nil {
		return result
	}
	submission, err := scrobble.TrackSubmissionFrom(input.Track, durationOverride)
	if err != nil {
		s.logger("listenbrainz skipping track %s: %v", input.Track.ID, err)
		return result
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = s.clock()
	}

	// One observation at a time per user. Combined with the ObservedAt ordering
	// inside the engine, concurrent notifications settle deterministically.
	mu := s.playbackMutex(input.UserID)
	mu.Lock()
	defer mu.Unlock()

	current, err := loadPlay(ctx, s.db, input.UserID, input.Track.ID)
	if err != nil {
		s.logger("listenbrainz play load failed for %s: %v", input.Track.ID, err)
		return result
	}

	update, earned := scrobble.Settle(current, scrobble.ObservationFrom(input, submission.DurationSeconds), s.playID())
	if update.Started && input.StartedAt != nil && !input.StartedAt.IsZero() {
		// An explicit client event may declare when the play really began.
		update.Play.StartedAt = input.StartedAt.UTC()
	}

	if earned {
		// ListenBrainz accepts historical listens, so unlike Last.fm there is
		// no clamp: a listen recovered days later keeps the time it happened.
		submission.Timestamp = scrobble.ScrobbleTimestamp(update.Play.StartedAt, input.ObservedAt, 0)
		submission.PlayedSeconds = update.Play.ListenedSeconds
		submission.DedupeKey = scrobble.DedupeKey(submission.TrackID, submission.Artist, submission.Track, submission.Timestamp)
		s.logger("listenbrainz submitting: track=%q artist=%q listened=%d/%d source=%s",
			submission.Track, submission.Artist, update.Play.ListenedSeconds, submission.DurationSeconds, playbackSource(input))
		queued, owned, err := s.submitListen(ctx, input.UserID, submission, playbackSource(input))
		switch {
		case err != nil:
			// Leave Scrobbled false so the next observation tries again.
			s.logger("listenbrainz listen claim failed for %s: %v", input.Track.ID, err)
		case owned:
			update.Play.Scrobbled = true
			result.Scrobbled = true
			result.Queued = queued
		default:
			// Already claimed elsewhere; stop re-evaluating this play.
			update.Play.Scrobbled = true
		}
	}

	if err := savePlay(ctx, s.db, update.Play); err != nil {
		s.logger("listenbrainz play save failed for %s: %v", input.Track.ID, err)
	}

	if s.announcePlayingNow(ctx, update, submission, input) {
		result.NowPlaying = true
	}
	return result
}

func playbackSource(input PlaybackInput) string {
	if source := strings.TrimSpace(input.Source); source != "" {
		return source
	}
	return "playback"
}

// announcePlayingNow updates ListenBrainz's "playing now" when this observation
// shows the track is what the user is actually hearing.
func (s *Service) announcePlayingNow(ctx context.Context, update scrobble.PlayUpdate, submission TrackSubmission, input PlaybackInput) bool {
	pointer, err := loadNowPlaying(ctx, s.db, input.UserID)
	if err != nil {
		s.logger("listenbrainz playing now state load failed: %v", err)
		return false
	}
	var otherAdvancedAt time.Time
	if !update.Advanced {
		// Only needed to tell a prefetch apart from real playback, which is
		// only in question when this observation credited nothing.
		if advancedAt, err := latestOtherAdvance(ctx, s.db, input.UserID, input.Track.ID); err == nil {
			otherAdvancedAt = advancedAt
		}
	}
	if !scrobble.ShouldAnnounceNowPlaying(update, pointer, otherAdvancedAt, input.ObservedAt) {
		return false
	}

	sendErr := s.sendPlayingNow(ctx, input.UserID, submission, playbackSource(input), false)

	// The pointer moves even on failure, with a zero SentAt: that audits the
	// failure once instead of on every position report, while leaving the
	// refresh throttle disengaged so the next report retries immediately.
	next := nowPlayingPointer{TrackID: update.Play.TrackID, PlayID: update.Play.PlayID, Exists: true}
	if sendErr == nil {
		next.SentAt = input.ObservedAt
	}
	if err := saveNowPlaying(ctx, s.db, input.UserID, next); err != nil {
		s.logger("listenbrainz playing now state save failed: %v", err)
	}
	return sendErr == nil
}

// sendPlayingNow announces the current track. It is never queued: by the time a
// retry succeeded the listener would be somewhere else entirely.
func (s *Service) sendPlayingNow(ctx context.Context, userID string, submission TrackSubmission, source string, record bool) error {
	client, err := s.clientFor(ctx, userID)
	if err != nil {
		return err
	}
	err = client.SubmitPlayingNow(ctx, submission)
	if err != nil {
		s.logger("listenbrainz playing now failed for %q: %v", submission.Track, err)
		if classify(err) == classAuth {
			s.noteAuthFailure(ctx, userID, err)
		}
		s.record(ctx, userID, queueKindPlayingNow, submission, submissionStatusFailed, source, err)
		return err
	}
	if record {
		s.record(ctx, userID, queueKindPlayingNow, submission, submissionStatusSubmitted, source, nil)
	}
	return nil
}

// ---------------------------------------------------------------------------
// explicit submission
// ---------------------------------------------------------------------------

// SubmitManualScrobble records a listen the server decided on its own — a
// radio play, say — rather than one measured from a client's reports.
func (s *Service) SubmitManualScrobble(ctx context.Context, userID string, track catalog.MusicTrack, playedAt time.Time, playedSeconds int) error {
	return s.SubmitScrobble(ctx, userID, track, playedAt, playedSeconds, "manual")
}

func (s *Service) SubmitScrobble(ctx context.Context, userID string, track catalog.MusicTrack, playedAt time.Time, playedSeconds int, source string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if _, err := loadSession(ctx, s.db, userID); err != nil {
		return err
	}
	submission, err := scrobble.TrackSubmissionFrom(track, 0)
	if err != nil {
		return err
	}
	submission.Timestamp = scrobble.ScrobbleTimestamp(playedAt.UTC(), s.clock(), 0)
	submission.PlayedSeconds = playedSeconds
	submission.DedupeKey = scrobble.DedupeKey(submission.TrackID, submission.Artist, submission.Track, submission.Timestamp)
	_, _, err = s.submitListen(ctx, userID, submission, normalizeSubmissionSource(source, "manual"))
	return err
}

func (s *Service) SubmitNowPlaying(ctx context.Context, userID string, track catalog.MusicTrack, source string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	submission, err := scrobble.TrackSubmissionFrom(track, 0)
	if err != nil {
		return err
	}
	return s.sendPlayingNow(ctx, userID, submission, normalizeSubmissionSource(source, "manual"), true)
}

func normalizeSubmissionSource(source, fallback string) string {
	if trimmed := strings.TrimSpace(source); trimmed != "" {
		return trimmed
	}
	return fallback
}

func newPlayToken() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

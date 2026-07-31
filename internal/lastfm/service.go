package lastfm

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/safego"
)

const (
	queueKindNowPlaying = "now_playing"
	queueKindScrobble   = "scrobble"
	queueKindLove       = "love"
	queueKindUnlove     = "unlove"

	sourceStream = "stream"
)

type Service struct {
	db         *sql.DB
	httpClient *http.Client
	mu         sync.RWMutex
	client     *Client
	logger     func(format string, args ...any)

	// clock and playID are indirected so the listen engine can be driven
	// deterministically in tests.
	clock  func() time.Time
	playID func() string

	playbackLocks sync.Map // per-user *sync.Mutex serializing playback observations
}

type ServiceOptions struct {
	DB           *sql.DB
	APIKey       string
	SharedSecret string
	HTTPClient   *http.Client
	Logger       func(format string, args ...any)
	Now          func() time.Time
	NewPlayID    func() string
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
	return &Service{
		db:         options.DB,
		httpClient: options.HTTPClient,
		client:     NewClient(options.APIKey, options.SharedSecret, options.HTTPClient),
		logger:     logger,
		clock:      clock,
		playID:     playID,
	}
}

func (s *Service) Enabled() bool {
	_, ok := s.activeClient()
	return ok
}

func (s *Service) Configure(apiKey, sharedSecret string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = NewClient(apiKey, sharedSecret, s.httpClient)
}

func (s *Service) LoadConfig(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	record, ok, err := loadAppConfig(ctx, s.db)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !record.Enabled {
		s.Configure("", "")
		return nil
	}
	s.Configure(record.APIKey, record.SharedSecret)
	return nil
}

func (s *Service) Config(ctx context.Context) (AppConfig, error) {
	if s == nil || s.db == nil {
		return AppConfig{}, ErrDisabled
	}
	if record, ok, err := loadAppConfig(ctx, s.db); err != nil {
		return AppConfig{}, err
	} else if ok {
		updatedAt := record.UpdatedAt
		return AppConfig{
			Enabled:         record.Enabled && strings.TrimSpace(record.APIKey) != "" && strings.TrimSpace(record.SharedSecret) != "",
			APIKey:          record.APIKey,
			HasSharedSecret: strings.TrimSpace(record.SharedSecret) != "",
			Source:          "ui",
			UpdatedAt:       &updatedAt,
		}, nil
	}
	apiKey, sharedSecret := s.currentCredentials()
	return AppConfig{
		Enabled:         strings.TrimSpace(apiKey) != "" && strings.TrimSpace(sharedSecret) != "",
		APIKey:          apiKey,
		HasSharedSecret: strings.TrimSpace(sharedSecret) != "",
		Source:          "environment",
	}, nil
}

func (s *Service) SaveConfig(ctx context.Context, input AppConfigInput) (AppConfig, error) {
	if s == nil || s.db == nil {
		return AppConfig{}, ErrDisabled
	}
	apiKey := strings.TrimSpace(input.APIKey)
	sharedSecret := strings.TrimSpace(input.SharedSecret)
	var previousKey string
	if record, ok, err := loadAppConfig(ctx, s.db); err != nil {
		return AppConfig{}, err
	} else if ok {
		previousKey = record.APIKey
		if apiKey == "" {
			apiKey = record.APIKey
		}
		if sharedSecret == "" {
			sharedSecret = record.SharedSecret
		}
	}
	currentAPIKey, currentSharedSecret := s.currentCredentials()
	if apiKey == "" {
		apiKey = currentAPIKey
	}
	if sharedSecret == "" {
		sharedSecret = currentSharedSecret
	}
	effectivePreviousKey := previousKey
	if effectivePreviousKey == "" {
		effectivePreviousKey = currentAPIKey
	}
	if strings.TrimSpace(input.APIKey) != "" &&
		effectivePreviousKey != "" &&
		apiKey != effectivePreviousKey &&
		strings.TrimSpace(input.SharedSecret) == "" {
		return AppConfig{}, fmt.Errorf("%w: shared secret is required when changing the API key", ErrInvalidConfig)
	}
	if apiKey == "" || sharedSecret == "" {
		return AppConfig{}, ErrInvalidConfig
	}
	httpClient := s.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	client := NewClient(apiKey, sharedSecret, httpClient)
	if _, err := client.GetToken(ctx); err != nil {
		if errors.Is(err, ErrInvalidSignature) {
			return AppConfig{}, fmt.Errorf("%w: verify the shared secret matches the API key in your Last.fm application settings", ErrInvalidConfig)
		}
		return AppConfig{}, fmt.Errorf("last.fm credentials rejected: %w", err)
	}
	if _, err := saveAppConfig(ctx, s.db, true, apiKey, sharedSecret); err != nil {
		return AppConfig{}, err
	}
	s.Configure(apiKey, sharedSecret)
	return s.Config(ctx)
}

func (s *Service) ClearConfig(ctx context.Context) (AppConfig, error) {
	if s == nil || s.db == nil {
		return AppConfig{}, ErrDisabled
	}
	if _, err := saveAppConfig(ctx, s.db, false, "", ""); err != nil {
		return AppConfig{}, err
	}
	s.Configure("", "")
	return s.Config(ctx)
}

func (s *Service) activeClient() (*Client, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	return client, client != nil && client.Enabled()
}

func (s *Service) reloadClient(ctx context.Context) error {
	return s.LoadConfig(ctx)
}

// ActiveClient exposes the configured Last.fm client for read-only API calls
// such as artist.getInfo. Scrobbling still requires Enabled().
func (s *Service) ActiveClient() (*Client, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	return client, client != nil && client.APIKeyConfigured()
}

func (s *Service) currentCredentials() (apiKey, sharedSecret string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil {
		return "", ""
	}
	return s.client.apiKey, s.client.sharedSecret
}

func (s *Service) playbackMutex(userID string) *sync.Mutex {
	v, _ := s.playbackLocks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Service) Status(ctx context.Context, userID string) (Status, error) {
	status := Status{Enabled: s.Enabled()}
	if !s.Enabled() {
		return status, nil
	}
	if session, err := loadSession(ctx, s.db, userID); err == nil {
		status.Connected = true
		status.Username = session.Username
		connectedAt := session.ConnectedAt
		status.ConnectedAt = &connectedAt
	}
	queueSize, err := countQueue(ctx, s.db, userID)
	if err != nil {
		return status, err
	}
	status.QueueSize = queueSize
	return status, nil
}

func (s *Service) BeginAuth(ctx context.Context) (AuthBeginResponse, error) {
	if err := s.reloadClient(ctx); err != nil {
		return AuthBeginResponse{}, err
	}
	client, ok := s.activeClient()
	if !ok {
		return AuthBeginResponse{}, ErrDisabled
	}
	token, err := client.GetToken(ctx)
	if err != nil {
		return AuthBeginResponse{}, err
	}
	return AuthBeginResponse{
		AuthURL: client.AuthURL(token),
		Token:   token,
	}, nil
}

func (s *Service) CompleteAuth(ctx context.Context, userID, token string) (AuthCompleteResponse, error) {
	if err := s.reloadClient(ctx); err != nil {
		return AuthCompleteResponse{}, err
	}
	client, ok := s.activeClient()
	if !ok {
		return AuthCompleteResponse{}, ErrDisabled
	}
	username, sessionKey, err := client.GetSession(ctx, token)
	if err != nil {
		return AuthCompleteResponse{}, err
	}
	record, err := saveSession(ctx, s.db, userID, username, sessionKey)
	if err != nil {
		return AuthCompleteResponse{}, err
	}
	// Anything that piled up while the account was disconnected is now
	// deliverable: send it immediately rather than waiting for a poller tick,
	// and reset the backoff so long-deferred items go out at once.
	if err := resetQueueBackoff(ctx, s.db, userID); err != nil {
		s.logger("last.fm queue backoff reset failed: %v", err)
	}
	safego.Go("last.fm post-auth flush", func() {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if flushed, err := s.FlushQueue(flushCtx, userID, 200); err != nil {
			s.logger("last.fm post-auth flush failed: %v", err)
		} else if flushed > 0 {
			s.logger("last.fm delivered %d submission(s) held while disconnected", flushed)
		}
	})
	return AuthCompleteResponse{
		Username:    record.Username,
		Connected:   true,
		ConnectedAt: record.ConnectedAt,
	}, nil
}

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
// playback entry points
// ---------------------------------------------------------------------------

// HandlePlayback folds one observation of a track into the listen engine. It is
// the single entry point for every automatic trigger; callers stamp ObservedAt
// when the request arrived so that observations overtaking each other in flight
// can be ordered.
func (s *Service) HandlePlayback(ctx context.Context, input PlaybackInput) {
	s.processPlayback(ctx, input, input.DurationSeconds)
}

func (s *Service) HandleStreamStart(ctx context.Context, userID string, track catalog.MusicTrack, resumeSeconds int) {
	s.HandlePlayback(ctx, PlaybackInput{
		UserID:        userID,
		Track:         track,
		Source:        sourceStream,
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

func (s *Service) HandleScrobbleEvent(ctx context.Context, userID string, track catalog.MusicTrack, input ScrobbleEventInput) (ScrobbleEventResponse, error) {
	event, err := parseScrobbleEvent(input.Event)
	if err != nil {
		return ScrobbleEventResponse{}, err
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
	return ScrobbleEventResponse{
		TrackID:         track.ID,
		Event:           string(event),
		NowPlaying:      result.NowPlaying,
		Scrobbled:       result.Scrobbled,
		Queued:          result.Queued,
		ProgressSeconds: input.ProgressSeconds,
	}, nil
}

// ProcessPlayback is retained for callers that do not need the result.
func (s *Service) ProcessPlayback(ctx context.Context, input PlaybackInput) {
	s.processPlayback(ctx, input, input.DurationSeconds)
}

func (s *Service) processPlayback(ctx context.Context, input PlaybackInput, durationOverride int) playbackResult {
	result := playbackResult{}
	if !s.Enabled() || strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.Track.ID) == "" {
		return result
	}
	if _, err := loadSession(ctx, s.db, input.UserID); err != nil {
		return result
	}
	submission, err := trackSubmission(input.Track, durationOverride)
	if err != nil {
		s.logger("last.fm skipping track %s: %v", input.Track.ID, err)
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
		s.logger("last.fm play load failed for %s: %v", input.Track.ID, err)
		return result
	}

	update, earned := settle(current, observationFrom(input, submission.DurationSeconds), s.playID())
	if update.Started && input.StartedAt != nil && !input.StartedAt.IsZero() {
		// An explicit client event may declare when the play really began.
		update.Play.StartedAt = input.StartedAt.UTC()
	}

	if earned {
		submission.Timestamp = scrobbleTimestamp(update.Play.StartedAt, input.ObservedAt)
		submission.PlayedSeconds = update.Play.ListenedSeconds
		submission.DedupeKey = scrobbleDedupeKey(submission.TrackID, submission.Artist, submission.Track, submission.Timestamp)
		s.logger("last.fm scrobbling: track=%q artist=%q listened=%d/%d source=%s",
			submission.Track, submission.Artist, update.Play.ListenedSeconds, submission.DurationSeconds, playbackSource(input))
		queued, owned, err := s.scrobble(ctx, input.UserID, submission, playbackSource(input))
		switch {
		case err != nil:
			// Leave Scrobbled false so the next observation tries again.
			s.logger("last.fm scrobble claim failed for %s: %v", input.Track.ID, err)
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
		s.logger("last.fm play save failed for %s: %v", input.Track.ID, err)
	}

	if s.announceNowPlaying(ctx, update, submission, input) {
		result.NowPlaying = true
	}

	if loved, unloved := loveStateChanged(input.Before, input.After, input.Patch); loved || unloved {
		s.handleLoveChange(ctx, input.UserID, submission, loved)
	}
	return result
}

func playbackSource(input PlaybackInput) string {
	if source := strings.TrimSpace(input.Source); source != "" {
		return source
	}
	return "playback"
}

// announceNowPlaying updates Last.fm's "now playing" when this observation
// shows the track is what the user is actually hearing.
func (s *Service) announceNowPlaying(ctx context.Context, update playUpdate, submission TrackSubmission, input PlaybackInput) bool {
	pointer, err := loadNowPlaying(ctx, s.db, input.UserID)
	if err != nil {
		s.logger("last.fm now playing state load failed: %v", err)
		return false
	}
	otherAdvancedAt := time.Time{}
	if !update.Advanced {
		if otherAdvancedAt, err = latestOtherAdvance(ctx, s.db, input.UserID, input.Track.ID); err != nil {
			s.logger("last.fm now playing prefetch check failed: %v", err)
			return false
		}
	}
	if !shouldAnnounceNowPlaying(update, pointer, otherAdvancedAt, input.ObservedAt) {
		return false
	}

	// The first attempt for a play is the one worth auditing; retries after it
	// say nothing new.
	first := !pointer.Exists || pointer.PlayID != update.Play.PlayID
	submission.Timestamp = input.ObservedAt
	sendErr := s.sendNowPlaying(ctx, input.UserID, submission, playbackSource(input), first)

	// The pointer moves either way. A zero SentAt marks the attempt as
	// unannounced, so the listener's next position report retries at once while
	// the audit log and the failure logging stay quiet.
	next := nowPlayingPointer{TrackID: update.Play.TrackID, PlayID: update.Play.PlayID}
	if sendErr == nil {
		next.SentAt = input.ObservedAt
	}
	if err := saveNowPlaying(ctx, s.db, input.UserID, next); err != nil {
		s.logger("last.fm now playing state save failed: %v", err)
	}
	return sendErr == nil
}

// sendNowPlaying delivers a "now playing" update. It is deliberately never
// queued: it describes this instant, and replaying a stale one later announces
// the wrong song.
func (s *Service) sendNowPlaying(ctx context.Context, userID string, submission TrackSubmission, source string, record bool) error {
	client, ok := s.activeClient()
	if !ok {
		return ErrDisabled
	}
	session, err := loadSession(ctx, s.db, userID)
	if err != nil {
		return err
	}
	if err := client.UpdateNowPlaying(ctx, session.SessionKey, submission); err != nil {
		if isSessionRejection(err) {
			s.invalidateSession(ctx, userID, err)
		}
		if record {
			s.logger("last.fm now playing failed for %q: %v", submission.Track, err)
			_ = recordSubmission(ctx, s.db, userID, queueKindNowPlaying, submission, submissionStatusFailed, source, err)
		}
		return err
	}
	s.logger("last.fm now playing: track=%q artist=%q source=%s", submission.Track, submission.Artist, source)
	if record {
		// One audit row per play, not per refresh.
		_ = recordSubmission(ctx, s.db, userID, queueKindNowPlaying, submission, submissionStatusSubmitted, source, nil)
	}
	return nil
}

func (s *Service) handleLoveChange(ctx context.Context, userID string, submission TrackSubmission, loved bool) {
	kind := queueKindUnlove
	if loved {
		kind = queueKindLove
	}
	if err := s.submitLove(ctx, userID, kind, submission); err != nil {
		s.logger("last.fm %s failed for %q: %v", kind, submission.Track, err)
	}
}

// ---------------------------------------------------------------------------
// explicit submissions
// ---------------------------------------------------------------------------

func (s *Service) SubmitManualScrobble(ctx context.Context, userID string, track catalog.MusicTrack, playedAt time.Time, playedSeconds int) error {
	return s.SubmitScrobble(ctx, userID, track, playedAt, playedSeconds, "manual")
}

// SubmitScrobble records one play directly, bypassing the listen engine. It
// still goes through the ledger, so submitting the same play twice is a no-op.
func (s *Service) SubmitScrobble(ctx context.Context, userID string, track catalog.MusicTrack, playedAt time.Time, playedSeconds int, source string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if _, err := loadSession(ctx, s.db, userID); err != nil {
		return err
	}
	submission, err := trackSubmission(track, 0)
	if err != nil {
		return err
	}
	submission.Timestamp = scrobbleTimestamp(playedAt.UTC(), s.clock())
	submission.PlayedSeconds = playedSeconds
	submission.DedupeKey = scrobbleDedupeKey(submission.TrackID, submission.Artist, submission.Track, submission.Timestamp)
	_, _, err = s.scrobble(ctx, userID, submission, normalizeSubmissionSource(source, "manual"))
	return err
}

func (s *Service) SubmitNowPlaying(ctx context.Context, userID string, track catalog.MusicTrack, source string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	submission, err := trackSubmission(track, 0)
	if err != nil {
		return err
	}
	submission.Timestamp = s.clock()
	return s.sendNowPlaying(ctx, userID, submission, normalizeSubmissionSource(source, "external"), true)
}

func normalizeSubmissionSource(source, fallback string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return fallback
	}
	return source
}

func (s *Service) invalidateSession(ctx context.Context, userID string, cause error) {
	if err := deleteSession(ctx, s.db, userID); err != nil {
		s.logger("last.fm session delete failed: %v", err)
		return
	}
	s.logger("last.fm session cleared after auth failure: %v", cause)
}

func newPlayToken() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

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
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

const (
	queueKindNowPlaying = "now_playing"
	queueKindScrobble   = "scrobble"
	queueKindLove       = "love"
	queueKindUnlove     = "unlove"
	maxQueueAttempts    = 8
)

type Service struct {
	db     *sql.DB
	client *Client
	logger func(format string, args ...any)
}

type ServiceOptions struct {
	DB           *sql.DB
	APIKey       string
	SharedSecret string
	HTTPClient   *http.Client
	Logger       func(format string, args ...any)
}

func NewService(options ServiceOptions) *Service {
	logger := options.Logger
	if logger == nil {
		logger = log.Printf
	}
	return &Service{
		db:     options.DB,
		client: NewClient(options.APIKey, options.SharedSecret, options.HTTPClient),
		logger: logger,
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.client != nil && s.client.Enabled() && s.db != nil
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
	if !s.Enabled() {
		return AuthBeginResponse{}, ErrDisabled
	}
	token, err := s.client.GetToken(ctx)
	if err != nil {
		return AuthBeginResponse{}, err
	}
	return AuthBeginResponse{
		AuthURL: s.client.AuthURL(token),
		Token:   token,
	}, nil
}

func (s *Service) CompleteAuth(ctx context.Context, userID, token string) (AuthCompleteResponse, error) {
	if !s.Enabled() {
		return AuthCompleteResponse{}, ErrDisabled
	}
	username, sessionKey, err := s.client.GetSession(ctx, token)
	if err != nil {
		return AuthCompleteResponse{}, err
	}
	record, err := saveSession(ctx, s.db, userID, username, sessionKey)
	if err != nil {
		return AuthCompleteResponse{}, err
	}
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

func (s *Service) HandleStreamStart(ctx context.Context, userID string, track catalog.MusicTrack, resumeSeconds int) {
	s.ProcessPlayback(ctx, PlaybackInput{
		UserID:        userID,
		Track:         track,
		Source:        "stream",
		ResumeSeconds: resumeSeconds,
		Event:         EventStart,
		After:         catalog.PlaybackState{ProgressSeconds: resumeSeconds},
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
	s.ProcessPlayback(ctx, PlaybackInput{
		UserID: userID,
		Track:  track,
		Before: before,
		After:  after,
		Patch:  &patch,
		Source: "playback-patch",
	})
}

func (s *Service) HandlePlaybackPut(
	ctx context.Context,
	userID string,
	track catalog.MusicTrack,
	before catalog.PlaybackState,
	after catalog.PlaybackState,
) {
	s.ProcessPlayback(ctx, PlaybackInput{
		UserID: userID,
		Track:  track,
		Before: before,
		After:  after,
		Source: "playback-put",
	})
}

func (s *Service) HandleScrobbleEvent(ctx context.Context, userID string, track catalog.MusicTrack, input ScrobbleEventInput) (ScrobbleEventResponse, error) {
	event, err := parseScrobbleEvent(input.Event)
	if err != nil {
		return ScrobbleEventResponse{}, err
	}
	after := catalog.PlaybackState{ProgressSeconds: input.ProgressSeconds}
	if input.StartedAt != nil {
		after.LastPlayedAt = input.StartedAt
	}
	durationOverride := input.DurationSeconds
	result := s.processPlayback(ctx, PlaybackInput{
		UserID: userID,
		Track:  track,
		After:  after,
		Source: "scrobble-event",
		Event:  event,
	}, durationOverride)
	return ScrobbleEventResponse{
		TrackID:         track.ID,
		Event:           string(event),
		NowPlaying:      result.NowPlaying,
		Scrobbled:       result.Scrobbled,
		Queued:          result.Queued,
		ProgressSeconds: input.ProgressSeconds,
	}, nil
}

func (s *Service) SubmitManualScrobble(ctx context.Context, userID string, track catalog.MusicTrack, playedAt time.Time, playedSeconds int) error {
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
	submission.Timestamp = playedAt.UTC()
	submission.PlayedSeconds = playedSeconds
	_, err = s.submitScrobble(ctx, userID, submission, "manual")
	return err
}

func (s *Service) SubmitSubsonicScrobble(ctx context.Context, userID string, track catalog.MusicTrack, playedAt time.Time) error {
	return s.SubmitManualScrobble(ctx, userID, track, playedAt, 0)
}

func (s *Service) SubmitSubsonicNowPlaying(ctx context.Context, userID string, track catalog.MusicTrack) error {
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
	submission.Timestamp = time.Now().UTC()
	_, err = s.submitNowPlaying(ctx, userID, submission, "subsonic")
	return err
}

func (s *Service) ProcessPlayback(ctx context.Context, input PlaybackInput) {
	s.processPlayback(ctx, input, 0)
}

func (s *Service) processPlayback(ctx context.Context, input PlaybackInput, durationOverride int) playbackResult {
	result := playbackResult{}
	if !s.Enabled() {
		return result
	}
	if _, err := loadSession(ctx, s.db, input.UserID); err != nil {
		return result
	}

	submission, err := trackSubmission(input.Track, durationOverride)
	if err != nil {
		return result
	}

	session, err := loadTrackSession(ctx, s.db, input.UserID, input.Track.ID)
	if err != nil {
		s.logger("last.fm track session load failed for %s: %v", input.Track.ID, err)
		return result
	}

	switch input.Event {
	case EventSkip:
		session = trackSession{UserID: input.UserID, TrackID: input.Track.ID}
		_ = saveTrackSession(ctx, s.db, session)
		return result
	case EventStart:
		session = s.resetTrackSession(input, session)
		result = s.tryNowPlaying(ctx, &session, submission, input, result)
	default:
		if shouldAbandonSession(input.Before, input.After, input.Patch) {
			session = trackSession{UserID: input.UserID, TrackID: input.Track.ID}
			_ = saveTrackSession(ctx, s.db, session)
			return result
		}
		if shouldStartNewPlaySession(input.Before, input.After, input.Patch) || input.Source == "stream" {
			session = s.resetTrackSession(input, session)
		}
		if input.Event == EventProgress || input.Event == EventComplete || input.Event == "" {
			result = s.tryNowPlaying(ctx, &session, submission, input, result)
		}
	}

	progress := progressFromInput(input)
	if input.Event == EventComplete {
		input.After.Completed = true
	}
	forceComplete := input.After.Completed || (input.Patch != nil && input.Patch.Completed != nil && *input.Patch.Completed)
	if !session.Scrobbled && shouldScrobble(progress, submission.DurationSeconds, forceComplete) {
		submission.Timestamp = playStartedAt(session, input)
		submission.PlayedSeconds = progress
		queued, err := s.submitScrobble(ctx, input.UserID, submission, input.Source)
		if err != nil {
			s.logger("last.fm scrobble failed for %s: %v", input.Track.ID, err)
		} else {
			session.Scrobbled = true
			result.Scrobbled = true
			result.Queued = queued
		}
	}

	if loved, unloved := loveStateChanged(input.Before, input.After, input.Patch); loved || unloved {
		s.handleLoveChange(ctx, input.UserID, submission, loved)
	}

	if err := saveTrackSession(ctx, s.db, session); err != nil {
		s.logger("last.fm track session save failed for %s: %v", input.Track.ID, err)
	}
	return result
}

func (s *Service) resetTrackSession(input PlaybackInput, current trackSession) trackSession {
	startedAt := time.Now().UTC()
	if input.ResumeSeconds > 0 {
		startedAt = startedAt.Add(-time.Duration(input.ResumeSeconds) * time.Second)
	}
	if input.After.LastPlayedAt != nil {
		startedAt = input.After.LastPlayedAt.UTC()
	}
	return trackSession{
		UserID:        input.UserID,
		TrackID:       input.Track.ID,
		PlayToken:     newPlayToken(),
		PlayStartedAt: startedAt,
	}
}

func (s *Service) tryNowPlaying(ctx context.Context, session *trackSession, submission TrackSubmission, input PlaybackInput, result playbackResult) playbackResult {
	if session.NowPlayingSent {
		return result
	}
	progress := progressFromInput(input)
	if progress <= 0 && input.Event != EventStart && input.Source != "stream" {
		return result
	}
	submission.Timestamp = playStartedAt(*session, input)
	queued, err := s.submitNowPlaying(ctx, input.UserID, submission, input.Source)
	if err != nil {
		s.logger("last.fm now playing failed for %s: %v", input.Track.ID, err)
		return result
	}
	session.NowPlayingSent = true
	result.NowPlaying = true
	result.Queued = queued
	return result
}

func (s *Service) handleLoveChange(ctx context.Context, userID string, submission TrackSubmission, loved bool) {
	if loved {
		if err := s.submitLove(ctx, userID, submission, true); err != nil {
			s.logger("last.fm love failed for %q: %v", submission.Track, err)
		}
		return
	}
	if err := s.submitLove(ctx, userID, submission, false); err != nil {
		s.logger("last.fm unlove failed for %q: %v", submission.Track, err)
	}
}

func (s *Service) FlushQueue(ctx context.Context, userID string, limit int) (int, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}

	items, err := listQueuedSubmissions(ctx, s.db, userID, limit)
	if err != nil {
		return 0, err
	}

	flushed := 0
	for _, item := range items {
		session, err := loadSession(ctx, s.db, item.UserID)
		if err != nil {
			continue
		}
		submission := TrackSubmission{
			TrackID:              item.TrackID,
			Artist:               item.Artist,
			Track:                item.Track,
			Album:                item.Album,
			DurationSeconds:      item.DurationSeconds,
			Timestamp:            item.Timestamp,
			MusicBrainzRecording: item.MusicBrainzRecording,
		}
		submitErr := s.dispatchQueued(ctx, session.SessionKey, item.Kind, submission)
		if submitErr != nil {
			if s.invalidateSessionOnAuthError(ctx, item.UserID, submitErr) {
				return flushed, ErrSessionExpired
			}
			attempts := item.Attempts + 1
			if attempts >= maxQueueAttempts {
				s.logger("last.fm dropping queued %s for %q after %d attempts: %v", item.Kind, item.Track, attempts, submitErr)
				_ = recordSubmission(ctx, s.db, item.UserID, item.Kind, submission, submissionStatusDropped, "queue-flush", submitErr)
				_ = deleteQueueItem(ctx, s.db, item.ID)
				continue
			}
			_ = markQueueFailure(ctx, s.db, item.ID, attempts, submitErr.Error())
			continue
		}
		_ = recordSubmission(ctx, s.db, item.UserID, item.Kind, submission, submissionStatusSubmitted, "queue-flush", nil)
		if err := deleteQueueItem(ctx, s.db, item.ID); err != nil {
			return flushed, err
		}
		flushed++
	}
	return flushed, nil
}

func (s *Service) dispatchQueued(ctx context.Context, sessionKey, kind string, submission TrackSubmission) error {
	switch kind {
	case queueKindNowPlaying:
		return s.client.UpdateNowPlaying(ctx, sessionKey, submission)
	case queueKindScrobble:
		return s.client.Scrobble(ctx, sessionKey, submission)
	case queueKindLove:
		return s.client.LoveTrack(ctx, sessionKey, submission)
	case queueKindUnlove:
		return s.client.UnloveTrack(ctx, sessionKey, submission)
	default:
		return fmt.Errorf("unknown queue kind %q", kind)
	}
}

func (s *Service) submitNowPlaying(ctx context.Context, userID string, submission TrackSubmission, source string) (bool, error) {
	session, err := loadSession(ctx, s.db, userID)
	if err != nil {
		return false, err
	}
	if err := s.client.UpdateNowPlaying(ctx, session.SessionKey, submission); err != nil {
		if s.invalidateSessionOnAuthError(ctx, userID, err) {
			return false, err
		}
		if enqueueSubmission(ctx, s.db, userID, queueKindNowPlaying, submission, source) == nil {
			_ = recordSubmission(ctx, s.db, userID, queueKindNowPlaying, submission, submissionStatusQueued, source, err)
			return true, nil
		}
		_ = recordSubmission(ctx, s.db, userID, queueKindNowPlaying, submission, submissionStatusFailed, source, err)
		return false, err
	}
	_ = recordSubmission(ctx, s.db, userID, queueKindNowPlaying, submission, submissionStatusSubmitted, source, nil)
	return false, nil
}

func (s *Service) submitScrobble(ctx context.Context, userID string, submission TrackSubmission, source string) (bool, error) {
	session, err := loadSession(ctx, s.db, userID)
	if err != nil {
		return false, err
	}
	if err := s.client.Scrobble(ctx, session.SessionKey, submission); err != nil {
		if s.invalidateSessionOnAuthError(ctx, userID, err) {
			return false, err
		}
		if enqueueSubmission(ctx, s.db, userID, queueKindScrobble, submission, source) == nil {
			_ = recordSubmission(ctx, s.db, userID, queueKindScrobble, submission, submissionStatusQueued, source, err)
			return true, nil
		}
		_ = recordSubmission(ctx, s.db, userID, queueKindScrobble, submission, submissionStatusFailed, source, err)
		return false, err
	}
	_ = recordSubmission(ctx, s.db, userID, queueKindScrobble, submission, submissionStatusSubmitted, source, nil)
	return false, nil
}

func (s *Service) submitLove(ctx context.Context, userID string, submission TrackSubmission, loved bool) error {
	session, err := loadSession(ctx, s.db, userID)
	if err != nil {
		return err
	}
	kind := queueKindLove
	var submitErr error
	if loved {
		submitErr = s.client.LoveTrack(ctx, session.SessionKey, submission)
	} else {
		kind = queueKindUnlove
		submitErr = s.client.UnloveTrack(ctx, session.SessionKey, submission)
	}
	if submitErr != nil {
		if s.invalidateSessionOnAuthError(ctx, userID, submitErr) {
			return submitErr
		}
		if enqueueSubmission(ctx, s.db, userID, kind, submission, "playback") == nil {
			_ = recordSubmission(ctx, s.db, userID, kind, submission, submissionStatusQueued, "playback", submitErr)
			return nil
		}
		_ = recordSubmission(ctx, s.db, userID, kind, submission, submissionStatusFailed, "playback", submitErr)
		return submitErr
	}
	_ = recordSubmission(ctx, s.db, userID, kind, submission, submissionStatusSubmitted, "playback", nil)
	return nil
}

func (s *Service) invalidateSessionOnAuthError(ctx context.Context, userID string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrSessionExpired) {
		if deleteErr := deleteSession(ctx, s.db, userID); deleteErr != nil {
			s.logger("last.fm session delete failed: %v", deleteErr)
		} else {
			s.logger("last.fm session cleared after auth failure: %v", err)
		}
		return true
	}
	if strings.Contains(strings.ToLower(err.Error()), "session") && strings.Contains(strings.ToLower(err.Error()), "invalid") {
		_ = deleteSession(ctx, s.db, userID)
		return true
	}
	return false
}

func newPlayToken() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

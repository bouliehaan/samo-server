package lastfm

import (
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func trackSubmission(track catalog.MusicTrack, durationOverride int) (TrackSubmission, error) {
	artist := strings.TrimSpace(track.DisplayArtist)
	if artist == "" && len(track.ArtistNames) > 0 {
		artist = strings.Join(track.ArtistNames, ", ")
	}
	if artist == "" && len(track.AlbumArtistNames) > 0 {
		artist = strings.Join(track.AlbumArtistNames, ", ")
	}
	title := strings.TrimSpace(track.Title)
	if artist == "" || title == "" {
		return TrackSubmission{}, ErrMissingMetadata
	}
	duration := durationOverride
	if duration <= 0 {
		duration = track.DurationSeconds
	}
	if duration <= 0 && len(track.AudioFiles) > 0 {
		duration = track.AudioFiles[0].DurationSeconds
	}
	return TrackSubmission{
		TrackID:              track.ID,
		Artist:               artist,
		Track:                title,
		Album:                strings.TrimSpace(track.AlbumTitle),
		DurationSeconds:      duration,
		MusicBrainzRecording: strings.TrimSpace(track.ExternalIDs.MusicBrainzRecordingID),
	}, nil
}

func shouldStartNewPlaySession(before, after catalog.PlaybackState, patch *playback.PatchInput) bool {
	if patch != nil {
		if patch.IncrementPlayCount {
			return true
		}
		if patch.IncrementSkipCount {
			return true
		}
		if patch.TouchLastPlayedAt && after.ProgressSeconds <= 15 {
			return true
		}
		if patch.PlayCount != nil && *patch.PlayCount > before.PlayCount {
			return true
		}
	}
	if before.ProgressSeconds >= 30 && after.ProgressSeconds < 15 {
		return true
	}
	if before.ProgressSeconds == 0 && after.ProgressSeconds > 0 && after.ProgressSeconds <= 5 {
		return true
	}
	if before.PlayCount < after.PlayCount {
		return true
	}
	return false
}

func shouldAbandonSession(before, after catalog.PlaybackState, patch *playback.PatchInput) bool {
	if patch != nil {
		if patch.IncrementSkipCount {
			return true
		}
		if patch.SkipCount != nil && *patch.SkipCount > before.SkipCount {
			return true
		}
	}
	return false
}

func shouldScrobble(progressSeconds, durationSeconds int, forceComplete bool) bool {
	if progressSeconds <= 0 {
		return false
	}
	minListen := minimumListenSeconds(durationSeconds)
	if progressSeconds < minListen {
		return false
	}
	if forceComplete {
		return true
	}
	return progressSeconds >= scrobbleThreshold(durationSeconds)
}

func minimumListenSeconds(durationSeconds int) int {
	if durationSeconds > 0 && durationSeconds < 30 {
		return durationSeconds
	}
	return 30
}

func scrobbleThreshold(durationSeconds int) int {
	if durationSeconds <= 0 {
		return 240
	}
	half := durationSeconds / 2
	if half < 240 {
		return half
	}
	return 240
}

func progressFromInput(input PlaybackInput) int {
	if input.Event != "" && input.After.ProgressSeconds > 0 {
		return input.After.ProgressSeconds
	}
	if input.Patch != nil && input.Patch.ProgressSeconds != nil {
		return *input.Patch.ProgressSeconds
	}
	return input.After.ProgressSeconds
}

func playStartedAt(session trackSession, input PlaybackInput) time.Time {
	if !session.PlayStartedAt.IsZero() {
		return session.PlayStartedAt
	}
	if input.ResumeSeconds > 0 {
		return time.Now().UTC().Add(-time.Duration(input.ResumeSeconds) * time.Second)
	}
	return time.Now().UTC()
}

func loveStateChanged(before, after catalog.PlaybackState, patch *playback.PatchInput) (loved bool, unloved bool) {
	beforeLoved := before.Favorite || before.Starred
	afterLoved := after.Favorite || after.Starred
	if patch != nil {
		if patch.Favorite != nil {
			afterLoved = *patch.Favorite || after.Starred
		}
		if patch.Starred != nil {
			afterLoved = after.Favorite || *patch.Starred
		}
	}
	if !beforeLoved && afterLoved {
		return true, false
	}
	if beforeLoved && !afterLoved {
		return false, true
	}
	return false, false
}

func parseScrobbleEvent(raw string) (ScrobbleEvent, error) {
	switch ScrobbleEvent(strings.ToLower(strings.TrimSpace(raw))) {
	case EventStart:
		return EventStart, nil
	case EventProgress:
		return EventProgress, nil
	case EventComplete:
		return EventComplete, nil
	case EventSkip:
		return EventSkip, nil
	default:
		return "", ErrInvalidEvent
	}
}

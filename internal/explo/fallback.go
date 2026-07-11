package explo

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/metadata"
)

// musicbrainzMinInterval keeps the fallback search under MusicBrainz's
// published "one request per second" etiquette for unauthenticated clients.
const musicbrainzMinInterval = 1100 * time.Millisecond

// trackNumberPrefix strips a leading track-number tag some exporters prepend
// to filenames, e.g. "02 ", "02. ", "02_", "02 - ".
var trackNumberPrefix = regexp.MustCompile(`^\d{1,3}[\s.\-_]+`)

// identifyByTextSearch is the fallback path when AcoustID can't identify a
// file (no match, or the lookup itself failed): parse a best-effort
// artist/title guess from the filename and search it against whatever music
// metadata providers are configured (MusicBrainz today, plus anything else
// SAMO_METADATA_PROVIDERS enables).
//
// The filename is ONLY ever used to seed a search query, never trusted
// directly - the actual identity comes from the search provider's own
// result, and that result is only accepted if its reported duration is
// close to the file's real (scanner-measured) duration. A wrong search hint
// producing an unrelated but plausible-sounding result is exactly the "wrong
// song" failure mode this guards against: an unrelated song essentially
// never happens to share a near-identical duration by chance, so the
// duration gate is the actual safety net, not the filename guess.
//
// Returns ok=false (no error) when nothing passed the duration gate -
// callers should record that as "unmatched", not retry.
func (s *Service) identifyByTextSearch(ctx context.Context, path string, knownDurationSeconds int) (identifiedTrack, bool, error) {
	if s.metadata == nil || knownDurationSeconds <= 0 {
		return identifiedTrack{}, false, nil
	}
	title, artist := parseFilenameSearchQuery(path)
	if title == "" {
		return identifiedTrack{}, false, nil
	}

	s.throttleMusicBrainz(ctx)
	response, err := s.metadata.Search(ctx, metadata.SearchRequest{
		Kind:      metadata.KindMusic,
		MusicType: metadata.MusicSearchTrack,
		Track:     title,
		Artist:    artist,
		Limit:     10,
	})
	if err != nil {
		return identifiedTrack{}, false, err
	}

	// Results already come back sorted by provider score descending, so the
	// first one to pass the duration gate is the best available candidate.
	for _, result := range response.Results {
		resultTitle := strings.TrimSpace(result.Title)
		if resultTitle == "" || result.DurationSeconds <= 0 {
			continue
		}
		if !withinDurationTolerance(result.DurationSeconds, knownDurationSeconds) {
			continue
		}
		names := make([]string, 0, len(result.Authors))
		for _, author := range result.Authors {
			if name := strings.TrimSpace(author.Name); name != "" {
				names = append(names, name)
			}
		}
		resultArtist := strings.Join(names, ", ")
		if resultArtist == "" {
			continue
		}

		match := identifiedTrack{
			Source:                 "musicbrainz-search",
			Score:                  float64(result.Score),
			MusicBrainzRecordingID: result.ExternalIDs.MusicBrainzRecordingID,
			Title:                  resultTitle,
			Artist:                 resultArtist,
		}
		if releaseTitle, ok := result.Raw["releaseTitle"].(string); ok {
			match.Album = strings.TrimSpace(releaseTitle)
		}
		return match, true, nil
	}
	return identifiedTrack{}, false, nil
}

// withinDurationTolerance is the actual identity check for the fallback
// path: 5% of the known duration or 5 seconds, whichever is larger, to
// absorb encoding/silence-trim differences without being loose enough to
// let a same-length-but-different song slip through.
func withinDurationTolerance(candidateSeconds, knownSeconds int) bool {
	tolerance := knownSeconds / 20
	if tolerance < 5 {
		tolerance = 5
	}
	diff := candidateSeconds - knownSeconds
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

// parseFilenameSearchQuery turns "02 - Some Artist - Track Title.mp3" (or
// underscore-delimited variants) into a (title, artist) search hint. Falls
// back to treating the whole cleaned filename as a title-only query when no
// "artist - title" delimiter is found. This is deliberately just a search
// seed, not a trusted identity - see identifyByTextSearch.
func parseFilenameSearchQuery(path string) (title, artist string) {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "_", " ")
	base = trackNumberPrefix.ReplaceAllString(base, "")
	base = strings.Join(strings.Fields(base), " ")
	if base == "" {
		return "", ""
	}

	if idx := strings.Index(base, " - "); idx > 0 {
		guessArtist := strings.TrimSpace(base[:idx])
		guessTitle := strings.TrimSpace(base[idx+len(" - "):])
		if guessArtist != "" && guessTitle != "" {
			return guessTitle, guessArtist
		}
	}
	return base, ""
}

type musicbrainzThrottle struct {
	mu   sync.Mutex
	last time.Time
}

// throttleMusicBrainz blocks until musicbrainzMinInterval has passed since
// the last fallback search call, so a large backfill batch can't hammer the
// (rate-limited, free) MusicBrainz search API.
func (s *Service) throttleMusicBrainz(ctx context.Context) {
	s.mbThrottle.mu.Lock()
	wait := musicbrainzMinInterval - time.Since(s.mbThrottle.last)
	if wait > 0 {
		s.mbThrottle.mu.Unlock()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		s.mbThrottle.mu.Lock()
	}
	s.mbThrottle.last = time.Now()
	s.mbThrottle.mu.Unlock()
}

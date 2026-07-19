package explo

import (
	"context"
	"fmt"
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
func (s *Service) identifyByTextSearch(ctx context.Context, path, tagTitle, tagArtist string, knownDurationSeconds int) (identifiedTrack, bool, error) {
	if s.metadata == nil || knownDurationSeconds <= 0 {
		return identifiedTrack{}, false, nil
	}
	title, artist := textSearchSeed(path, tagTitle, tagArtist)
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
	// Per-provider failures do NOT surface through err — the aggregator
	// collects them in ProviderErrors and returns an empty result set. Left
	// unchecked, an outage (or a rejected User-Agent) reads exactly like
	// "song not found": the track gets recorded as terminal 'unmatched'
	// instead of a retriable 'error', and the whole fallback path can be
	// broken for weeks without a single log line saying so.
	if len(response.Results) == 0 && len(response.ProviderErrors) > 0 {
		messages := make([]string, 0, len(response.ProviderErrors))
		for _, providerError := range response.ProviderErrors {
			messages = append(messages, providerError.Provider+": "+providerError.Error)
		}
		return identifiedTrack{}, false, fmt.Errorf("metadata search failed: %s", strings.Join(messages, "; "))
	}

	// Results come back sorted by provider score descending. Among the ones
	// that pass the duration gate, prefer a candidate whose release is
	// UNDERIVED (not a Compilation/Live/Remix group): a classic hit has many
	// duplicate MusicBrainz recordings, and the top-scored one is often a
	// duplicate that exists only on disco samplers — matching that one hands
	// the track a sampler album and sampler artwork. Fall back to the first
	// duration-passing candidate when no clean-release candidate exists.
	var fallbackMatch identifiedTrack
	fallbackFound := false
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
			// The recording search reports the release group too; carrying it
			// lets the cover engine build a Cover Art Archive URL for
			// fallback-matched tracks, which previously could never get a
			// cover at all on this path.
			MusicBrainzReleaseGroupID: result.ExternalIDs.MusicBrainzReleaseGroupID,
			Title:                     resultTitle,
			Artist:                    resultArtist,
		}
		derived, _ := result.Raw["releaseIsDerived"].(bool)
		if releaseTitle, ok := result.Raw["releaseTitle"].(string); ok && !derived {
			// A derived release's title is a sampler's name, not this
			// track's album — leave the album title alone in that case
			// (the scanner's tag, when present, is better than "Ultimate
			// Disco Vol. 7").
			match.Album = strings.TrimSpace(releaseTitle)
		}
		if !derived {
			return match, true, nil
		}
		if !fallbackFound {
			fallbackMatch = match
			fallbackFound = true
		}
	}
	return fallbackMatch, fallbackFound, nil
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

// featSuffix matches a trailing "feat./ft./featuring/with X" credit —
// MusicBrainz's canonical recording title omits guest artists, so searching
// the decorated form ("Song feat. Y") returns nothing.
var featSuffix = regexp.MustCompile(`(?i)\s*[(\[]?\s*(feat\.?|ft\.?|featuring|w/)\s+.*$`)

// trailingBracket matches ONE trailing "(...)" or "[...]" — "(single version)",
// "[Remix]", `(original 12")` — again, title noise the canonical record lacks.
var trailingBracket = regexp.MustCompile(`\s*[(\[][^()\[\]]*[)\]]\s*$`)

// normalizeSearchTitle strips guest credits and trailing bracketed qualifiers
// so a decorated title matches MusicBrainz's canonical form. It only shapes the
// SEARCH SEED — the duration gate and derived-release filter still decide
// whether any result is accepted, so an over-eager strip can only ever cost a
// match, never manufacture a wrong one.
func normalizeSearchTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	// Each strip is guarded so it can never reduce the title to nothing — a
	// title that is entirely a "feat." credit or a lone "(Reprise)" keeps its
	// original form rather than becoming an empty query.
	if stripped := strings.TrimSpace(featSuffix.ReplaceAllString(title, "")); stripped != "" {
		title = stripped
	}
	for {
		stripped := strings.TrimSpace(trailingBracket.ReplaceAllString(title, ""))
		if stripped == "" || stripped == title {
			break
		}
		title = stripped
	}
	return title
}

// textSearchSeed picks the cleanest (title, artist) to seed the fallback
// search. The scanner's parsed tags win when present — it already split the
// file into clean title/artist fields, whereas re-parsing the filename folds
// the album into the title. A genuinely tag-less drop falls back to the
// filename. The title is normalized either way (guest credits / bracketed
// qualifiers stripped).
func textSearchSeed(path, tagTitle, tagArtist string) (title, artist string) {
	if t := normalizeSearchTitle(tagTitle); t != "" {
		return t, strings.TrimSpace(tagArtist)
	}
	fnTitle, fnArtist := parseFilenameSearchQuery(path)
	return normalizeSearchTitle(fnTitle), fnArtist
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

// requestPacer enforces a minimum interval between calls to one external
// API. One instance per API (MusicBrainz, iTunes, Deezer). NOTE: like the
// AcoustID throttle, it drops its lock while sleeping, so it paces exactly
// one serial caller — which is what the explo pipeline is. If a worker pool
// is ever introduced, this must become a real token bucket first.
type requestPacer struct {
	mu   sync.Mutex
	last time.Time
}

func (p *requestPacer) wait(ctx context.Context, minInterval time.Duration) {
	p.mu.Lock()
	wait := minInterval - time.Since(p.last)
	if wait > 0 {
		p.mu.Unlock()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		p.mu.Lock()
	}
	p.last = time.Now()
	p.mu.Unlock()
}

// throttleMusicBrainz blocks until musicbrainzMinInterval has passed since
// the last MusicBrainz call (fallback search or cover release lookup), so a
// large batch can't hammer the rate-limited, free API.
func (s *Service) throttleMusicBrainz(ctx context.Context) {
	s.mbPacer.wait(ctx, musicbrainzMinInterval)
}

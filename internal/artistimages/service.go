package artistimages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/events"
	"github.com/bouliehaan/samo-server/internal/lastfm"
)

const (
	negativeCacheTTL   = 30 * 24 * time.Hour
	externalFetchLimit = 2

	// imageDownloadAttempts is small on purpose: a backfill multiplies it by
	// the size of the library, and a host that refuses one request is usually
	// refusing the next one too. It exists for the brief fault, not the ban.
	imageDownloadAttempts = 3
	imageRetryBaseDelay   = 400 * time.Millisecond
)

// fetchOutcome separates the two ways a lookup comes back empty-handed. They
// look alike from the outside and mean opposite things: one is an answer about
// the artist, the other is the absence of an answer.
type fetchOutcome int

const (
	fetchFound fetchOutcome = iota
	// fetchNoImage: every source was reached, and none holds a photo.
	fetchNoImage
	// fetchBlocked: a source never answered — refused, timed out, out of
	// quota. Says nothing about the artist.
	fetchBlocked
)

type CatalogPatcher interface {
	SetMusicArtistImages(artistID string, images []catalog.Image)
}

type Service struct {
	db      *sql.DB
	lastfm  *lastfm.Service
	covers  *covers.Service
	catalog CatalogPatcher
	logger  func(format string, args ...any)
	http    *http.Client
	bgCtx   context.Context

	mu       sync.Mutex
	inflight map[string]*resolveCall
	sem      chan struct{}

	events *events.Hub

	backfillMu     sync.Mutex
	activeBackfill *backfillRunner
	lastBackfill   *BackfillJob
}

type ServiceOptions struct {
	DB         *sql.DB
	LastFM     *lastfm.Service
	Covers     *covers.Service
	Catalog    CatalogPatcher
	Logger     func(format string, args ...any)
	HTTPClient *http.Client
}

func NewService(options ServiceOptions) *Service {
	logger := options.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Service{
		db:       options.DB,
		lastfm:   options.LastFM,
		covers:   options.Covers,
		catalog:  options.Catalog,
		logger:   logger,
		http:     httpClient,
		inflight: map[string]*resolveCall{},
		sem:      make(chan struct{}, externalFetchLimit),
	}
}

type resolveCall struct {
	done    chan struct{}
	images  []catalog.Image
	outcome fetchOutcome
}

// imageCandidate is one photo URL a source is offering, still unproven: the
// download that follows is where a blocked CDN actually surfaces.
type imageCandidate struct {
	URL    string
	Source string
}

// lookupResult is everything the sources offered for one artist, best source
// first, together with whether any of them failed to answer at all.
type lookupResult struct {
	candidates []imageCandidate
	blocked    bool
	source     string
}

func (r *lookupResult) add(candidate imageCandidate) {
	for _, existing := range r.candidates {
		if strings.EqualFold(existing.URL, candidate.URL) {
			return
		}
	}
	r.candidates = append(r.candidates, candidate)
	r.source = candidate.Source
}

func (r *lookupResult) note(source string, err error) {
	r.source = source
	if isTransientLookupError(err) {
		r.blocked = true
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.db != nil && s.covers != nil
}

func (s *Service) ResolveMusicArtistCover(ctx context.Context, artist catalog.MusicArtist) ([]catalog.Image, bool) {
	if images := catalog.NonEmptyImages(artist.Images); len(images) > 0 {
		if hasLocalArtistImage(images) {
			return images, true
		}
	}

	if s == nil || s.db == nil {
		return nil, false
	}

	if cached, ok, err := s.loadCachedCover(ctx, artist.ID); err == nil && ok {
		s.patchCatalog(artist.ID, cached)
		return cached, true
	} else if err == nil && !ok {
		return nil, false
	}

	if !s.Enabled() {
		return nil, false
	}

	return s.resolveExternal(ctx, artist)
}

func hasLocalArtistImage(images []catalog.Image) bool {
	for _, image := range images {
		if strings.TrimSpace(image.Path) != "" {
			return true
		}
		if strings.TrimSpace(image.ID) != "" && strings.HasPrefix(strings.TrimSpace(image.ID), "cover_") {
			return true
		}
	}
	return false
}

func (s *Service) loadCachedCover(ctx context.Context, artistID string) ([]catalog.Image, bool, error) {
	row, err := loadCacheRow(ctx, s.db, artistID)
	if errors.Is(err, errCacheMiss) {
		return nil, false, errCacheMiss
	}
	if err != nil {
		return nil, false, err
	}
	if row.CoverID == "" {
		if row.FetchedAt.IsZero() || time.Since(row.FetchedAt) < negativeCacheTTL {
			return nil, false, nil
		}
		return nil, false, errCacheMiss
	}
	if s.covers == nil {
		return nil, false, fmt.Errorf("covers service unavailable")
	}
	image, err := s.covers.Get(ctx, row.CoverID)
	if err != nil {
		if time.Since(row.FetchedAt) < negativeCacheTTL {
			return nil, false, errCacheMiss
		}
		return nil, false, errCacheMiss
	}
	return []catalog.Image{image}, true, nil
}

func (s *Service) resolveExternal(ctx context.Context, artist catalog.MusicArtist) ([]catalog.Image, bool) {
	s.mu.Lock()
	if call, ok := s.inflight[artist.ID]; ok {
		s.mu.Unlock()
		<-call.done
		return call.images, call.outcome == fetchFound
	}
	call := &resolveCall{done: make(chan struct{})}
	s.inflight[artist.ID] = call
	s.mu.Unlock()

	defer func() {
		close(call.done)
		s.mu.Lock()
		delete(s.inflight, artist.ID)
		s.mu.Unlock()
	}()

	images, outcome := s.fetchAndPersist(ctx, artist)
	call.images = images
	call.outcome = outcome
	return images, outcome == fetchFound
}

func (s *Service) fetchAndPersist(ctx context.Context, artist catalog.MusicArtist) ([]catalog.Image, fetchOutcome) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, fetchBlocked
	}

	lookup := s.lookupArtistImageCandidates(ctx, artist)
	blocked := lookup.blocked

	for _, candidate := range lookup.candidates {
		downloaded, err := s.downloadArtistImage(ctx, candidate.URL)
		if err == nil && downloaded != nil {
			images := []catalog.Image{*downloaded}
			if err := s.persistArtistImages(ctx, artist.ID, images, candidate.Source); err != nil {
				s.logger("artist image persist failed for %q: %v", artist.Name, err)
			} else {
				s.patchCatalog(artist.ID, images)
			}
			return images, fetchFound
		}
		s.logger("artist image download failed for %q via %s: %v", artist.Name, candidate.Source, err)
		if isTransientLookupError(err) {
			blocked = true
		}
	}

	if blocked {
		// No negative cache row. Nothing that happened here is a statement
		// about this artist, and recording it as one would hide them for a
		// month over a refusal that may clear in minutes.
		return nil, fetchBlocked
	}

	source := lookup.source
	if source == "" {
		source = "external"
	}
	_ = saveCacheRow(ctx, s.db, artist.ID, "", source)
	return nil, fetchNoImage
}

// downloadArtistImage fetches one candidate, retrying only faults that might
// clear. A 404 or a non-image body is an answer, and repeating the request
// cannot change it.
func (s *Service) downloadArtistImage(ctx context.Context, imageURL string) (*catalog.Image, error) {
	var lastErr error
	for attempt := range imageDownloadAttempts {
		if attempt > 0 {
			select {
			case <-time.After(imageRetryBaseDelay << (attempt - 1)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		downloaded, err := s.covers.DownloadFromURL(ctx, imageURL)
		if err == nil && downloaded != nil {
			return downloaded, nil
		}
		lastErr = err
		if lastErr == nil {
			lastErr = errors.New("cover download returned no image")
		}
		if !shouldRetryDownload(lastErr) {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// lookupArtistImageCandidates asks every source and keeps all the URLs they
// offer, Last.fm first and Deezer behind it. Every source is asked even after
// one has answered, because a URL is only a promise: the download is where a
// blocked CDN shows up, and by then it is too late to go back and consult the
// source that was skipped. Holding the alternatives is what makes the fallback
// real rather than a fallback only for the case where a lookup came back empty.
func (s *Service) lookupArtistImageCandidates(ctx context.Context, artist catalog.MusicArtist) lookupResult {
	var result lookupResult

	picture, err := s.lastfmArtistPictureURL(ctx, artist)
	switch {
	case err != nil:
		s.logger("artist image lookup failed for %q via lastfm: %v", artist.Name, err)
		result.note("lastfm", err)
	case picture != "":
		result.add(imageCandidate{URL: picture, Source: "lastfm"})
	}

	picture, err = deezerArtistPictureURL(ctx, s.http, lookupArtistNames(artist)...)
	switch {
	case err != nil:
		s.logger("artist image lookup failed for %q via deezer: %v", artist.Name, err)
		result.note("deezer", err)
	case picture != "":
		result.add(imageCandidate{URL: picture, Source: "deezer"})
	}

	return result
}

// lastfmArtistPictureURL asks Last.fm for a usable photo, by MBID first and
// then by name. The second attempt is not conditional on the first erroring:
// artist.getInfo answers 200 with its grey placeholder for artists it holds no
// picture of, and that empty-handed success is exactly the case where the other
// spelling is worth trying.
func (s *Service) lastfmArtistPictureURL(ctx context.Context, artist catalog.MusicArtist) (string, error) {
	if s.lastfm == nil {
		return "", nil
	}
	client, ok := s.lastfm.ActiveClient()
	if !ok || !client.APIKeyConfigured() {
		return "", nil
	}

	var firstErr error
	for _, key := range lastfmLookupKeys(strings.TrimSpace(artist.ExternalIDs.MusicBrainzArtistID)) {
		info, err := client.GetArtistInfo(ctx, artist.Name, key)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if image := strings.TrimSpace(info.Image); image != "" {
			return image, nil
		}
	}
	return "", firstErr
}

// lastfmLookupKeys is the MBID and then the empty string, which GetArtistInfo
// reads as "look this up by name instead".
func lastfmLookupKeys(mbid string) []string {
	if mbid == "" {
		return []string{""}
	}
	return []string{mbid, ""}
}

// artistCreditJoiners are the words a tagger puts between a lead artist and
// their guests. A track credited "Aesop Rock with El-P" is filed under an
// artist of that whole name, and no music service has an entry for it — the
// photo that belongs on it is Aesop Rock's.
//
// Every entry is spelled with the surrounding space that makes it a joiner
// rather than a substring, so "Ft. Lauderdale" and a band with "with" inside
// one of its words are both safe. Plain " and " is deliberately absent: "Bob
// Marley and the Wailers", "Simon and Garfunkel" and "Johnny Cash and The
// Tennessee Three" are single acts, and cutting them would trade a miss for a
// *wrong* photo, which is worse. " + " is included despite "Florence + the
// Machine" for the same reason the rest are safe — the full name is tried
// first, so that band matches itself and never reaches the shortened form.
//
// Matched case-insensitively against a lowered copy, and cut at the earliest
// joiner found, so "Toby Keith Duet with Willie Nelson" cuts at " duet with "
// rather than leaving a stray "Duet" behind on the shorter " with ".
var artistCreditJoiners = []string{
	" featuring ", " feat. ", " feat ", " ft. ", " ft ",
	" f/", " w/", " duet with ", " with ", " accomp. by ",
	" et ", " vs. ", " vs ", " / ", " + ", " read by ",
}

// leadArtistName returns the name up to the first credit joiner, or "" when
// there is none.
func leadArtistName(name string) string {
	lowered := strings.ToLower(name)
	cut := -1
	for _, joiner := range artistCreditJoiners {
		if idx := strings.Index(lowered, joiner); idx > 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut < 0 {
		return ""
	}
	return strings.TrimSpace(name[:cut])
}

// lookupArtistNames is the ordered list of spellings to try against an image
// source, most specific first. The full name leads, which is what keeps a band
// whose real name contains a joiner — "Earth, Wind & Fire", "Florence + the
// Machine" — matching itself: a shortened candidate is only ever reached after
// the full name has already come back empty.
func lookupArtistNames(artist catalog.MusicArtist) []string {
	names := []string{
		strings.TrimSpace(artist.Name),
		strings.TrimSpace(artist.SortName),
	}
	if idx := strings.Index(artist.Name, ","); idx > 0 {
		names = append(names, strings.TrimSpace(artist.Name[:idx]))
	}
	if idx := strings.Index(artist.Name, " & "); idx > 0 {
		names = append(names, strings.TrimSpace(artist.Name[:idx]))
	}
	// The lead artist of a collaboration credit, from both the display name and
	// the sort name — "Watsky w/Elliott, Damon" only yields "Watsky" via the
	// latter once the comma split has already run over it.
	for _, candidate := range []string{artist.Name, artist.SortName} {
		if lead := leadArtistName(candidate); lead != "" {
			names = append(names, lead)
			if idx := strings.Index(lead, ","); idx > 0 {
				names = append(names, strings.TrimSpace(lead[:idx]))
			}
		}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (s *Service) persistArtistImages(ctx context.Context, artistID string, images []catalog.Image, source string) error {
	coverID := ""
	if len(images) > 0 {
		coverID = strings.TrimSpace(images[0].ID)
	}
	if err := saveCacheRow(ctx, s.db, artistID, coverID, source); err != nil {
		return err
	}
	payload, err := json.Marshal(images)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE music_artists
		SET images_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, string(payload), artistID)
	return err
}

func (s *Service) patchCatalog(artistID string, images []catalog.Image) {
	if s.catalog == nil || len(images) == 0 {
		return
	}
	s.catalog.SetMusicArtistImages(artistID, images)
}

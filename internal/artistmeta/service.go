// Package artistmeta enriches catalog artists with a biography and a list of
// similar artists fetched from external providers, mirroring the resolve →
// cache → patch-catalog shape of internal/artistimages. Similar-artist names
// are resolved back to LOCAL catalog artists so the client can navigate to
// them; biographies come from Last.fm (when keyed) or Wikipedia (no key).
package artistmeta

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/lastfm"
)

const (
	// How long a "nothing found" result is trusted before we try again — keeps
	// us from hammering providers for artists they simply don't cover.
	negativeCacheTTL = 30 * 24 * time.Hour
	// Concurrent external fetches across the whole service (backfill + reads).
	externalFetchLimit = 2
	// Cap on similar artists surfaced per artist.
	maxSimilar = 12
)

// Catalog is the slice of the catalog service artistmeta needs: read an artist,
// resolve names to local artist IDs, and patch enrichment back in.
type Catalog interface {
	MusicArtist(id string) (catalog.MusicArtist, error)
	MusicArtistIDByName(name string) (string, bool)
	SetMusicArtistMeta(artistID, biography string, similar []catalog.SimilarArtistRef)
}

type Service struct {
	db      *sql.DB
	lastfm  *lastfm.Service
	catalog Catalog
	logger  func(format string, args ...any)
	http    *http.Client
	bgCtx   context.Context

	mu       sync.Mutex
	inflight map[string]*resolveCall
	sem      chan struct{}
}

type ServiceOptions struct {
	DB         *sql.DB
	LastFM     *lastfm.Service
	Catalog    Catalog
	Logger     func(format string, args ...any)
	HTTPClient *http.Client
}

type resolveCall struct {
	done chan struct{}
}

// similarCandidate is one related artist from an external provider before it's
// resolved against the local catalog. ImageURL is the provider's artist picture
// (Deezer); it's empty for providers that don't carry usable images (Last.fm).
type similarCandidate struct {
	Name     string
	ImageURL string
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
		catalog:  options.Catalog,
		logger:   logger,
		http:     httpClient,
		bgCtx:    context.Background(),
		inflight: map[string]*resolveCall{},
		sem:      make(chan struct{}, externalFetchLimit),
	}
}

// SetBackgroundContext supplies the long-lived context used by fire-and-forget
// resolves kicked from read paths, so they're cancelled at shutdown.
func (s *Service) SetBackgroundContext(ctx context.Context) {
	if ctx != nil {
		s.bgCtx = ctx
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.db != nil && s.catalog != nil
}

// Hydrate merges any cached biography/similar artists into the artist for THIS
// response, and — on a cache miss (or an expired negative cache) — kicks a
// non-blocking background resolve so the data self-heals for next time. Read
// paths call this; it never blocks on the network.
func (s *Service) Hydrate(ctx context.Context, artist *catalog.MusicArtist) {
	if !s.Enabled() || artist == nil || strings.TrimSpace(artist.ID) == "" {
		return
	}
	cached, err := loadCacheRow(ctx, s.db, artist.ID)
	if err == nil {
		if !cached.Empty {
			if cached.Biography != "" {
				artist.Biography = cached.Biography
			}
			if len(cached.Similar) > 0 {
				artist.SimilarArtists = cached.Similar
			}
			return
		}
		if time.Since(cached.FetchedAt) < negativeCacheTTL {
			return // recently confirmed empty — don't re-fetch
		}
	}
	target := *artist
	go func() {
		if _, _, _, err := s.resolve(s.bgCtx, target); err != nil {
			s.logger("artist meta resolve failed for %q: %v", target.Name, err)
		}
	}()
}

// FetchArtistsByIDs resolves a specific set of artists (e.g. ones newly added by
// a scan), blocking until done. Safe to call from a background goroutine.
func (s *Service) FetchArtistsByIDs(ctx context.Context, artistIDs []string) {
	if !s.Enabled() {
		return
	}
	for _, id := range artistIDs {
		if ctx.Err() != nil {
			return
		}
		artist, err := s.catalog.MusicArtist(id)
		if err != nil {
			continue
		}
		if _, _, _, err := s.resolve(ctx, artist); err != nil {
			s.logger("artist meta resolve failed for %q: %v", artist.Name, err)
		}
	}
}

// BackfillMissing resolves every artist that has no cached meta row yet. Bounded
// by externalFetchLimit; intended for a background run after a full scan.
func (s *Service) BackfillMissing(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id
		FROM music_artists a
		LEFT JOIN music_artist_external_meta m ON m.artist_id = a.id
		WHERE m.artist_id IS NULL`)
	if err != nil {
		return err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		artist, err := s.catalog.MusicArtist(id)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(artist catalog.MusicArtist) {
			defer wg.Done()
			if _, _, _, err := s.resolve(ctx, artist); err != nil {
				s.logger("artist meta backfill failed for %q: %v", artist.Name, err)
			}
		}(artist)
	}
	wg.Wait()
	return nil
}

// resolve performs (or coalesces) one artist's external lookup, persists the
// result to the cache (positive or negative), and patches the catalog. Returns
// the resolved biography, similar refs, and whether anything was found.
func (s *Service) resolve(
	ctx context.Context,
	artist catalog.MusicArtist,
) (string, []catalog.SimilarArtistRef, bool, error) {
	artistID := strings.TrimSpace(artist.ID)
	if artistID == "" {
		return "", nil, false, nil
	}

	// Single-flight per artist so concurrent reads/backfill don't double-fetch.
	s.mu.Lock()
	if call, ok := s.inflight[artistID]; ok {
		s.mu.Unlock()
		<-call.done
		cached, err := loadCacheRow(ctx, s.db, artistID)
		if err != nil {
			return "", nil, false, nil
		}
		return cached.Biography, cached.Similar, !cached.Empty, nil
	}
	call := &resolveCall{done: make(chan struct{})}
	s.inflight[artistID] = call
	s.mu.Unlock()
	defer func() {
		close(call.done)
		s.mu.Lock()
		delete(s.inflight, artistID)
		s.mu.Unlock()
	}()

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return "", nil, false, ctx.Err()
	}

	biography, lastfmSimilar := s.fetchFromLastFM(ctx, artist)
	// Deezer is the primary similar-artist source: no API key, and it carries
	// real artist pictures that power the external tiles. Last.fm's similar
	// names (its artist images are dead placeholders) are only a fallback for
	// when Deezer returns nothing.
	candidates := deezerSimilarArtists(ctx, s.http, artist.Name)
	if len(candidates) == 0 {
		for _, name := range lastfmSimilar {
			candidates = append(candidates, similarCandidate{Name: name})
		}
	}
	if strings.TrimSpace(biography) == "" {
		biography = wikipediaArtistBio(ctx, s.http, artist.Name)
	}
	similar := s.resolveSimilarRefs(artistID, candidates)

	if err := saveCacheRow(ctx, s.db, artistID, biography, similar, "external"); err != nil {
		s.logger("artist meta cache save failed for %q: %v", artist.Name, err)
	}
	if strings.TrimSpace(biography) != "" || len(similar) > 0 {
		s.catalog.SetMusicArtistMeta(artistID, biography, similar)
		return biography, similar, true, nil
	}
	return "", nil, false, nil
}

func (s *Service) fetchFromLastFM(ctx context.Context, artist catalog.MusicArtist) (string, []string) {
	if s.lastfm == nil {
		return "", nil
	}
	client, ok := s.lastfm.ActiveClient()
	if !ok || !client.APIKeyConfigured() {
		return "", nil
	}
	mbid := strings.TrimSpace(artist.ExternalIDs.MusicBrainzArtistID)
	meta, err := client.GetArtistMeta(ctx, artist.Name, mbid)
	if err != nil {
		// Retry without the MBID — a stale/foreign id can 404 where the name hits.
		if mbid != "" {
			if retry, retryErr := client.GetArtistMeta(ctx, artist.Name, ""); retryErr == nil {
				return retry.Biography, retry.SimilarNames
			}
		}
		return "", nil
	}
	return meta.Biography, meta.SimilarNames
}

// resolveSimilarRefs turns provider candidates into rail refs. An artist that
// exists in THIS library becomes a navigable ref (local id + images); one that
// doesn't becomes an External ref (name + provider image) so the rail stays full
// instead of collapsing to the handful the user happens to own. De-duped by id
// and by case-folded name, excluding the artist itself, capped at maxSimilar.
func (s *Service) resolveSimilarRefs(selfID string, candidates []similarCandidate) []catalog.SimilarArtistRef {
	refs := make([]catalog.SimilarArtistRef, 0, len(candidates))
	seenIDs := map[string]struct{}{selfID: {}}
	seenNames := map[string]struct{}{}
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			continue
		}
		nameKey := strings.ToLower(name)
		if _, dup := seenNames[nameKey]; dup {
			continue
		}

		if id, ok := s.catalog.MusicArtistIDByName(name); ok && id != "" {
			if _, dup := seenIDs[id]; dup {
				continue
			}
			seenIDs[id] = struct{}{}
			seenNames[nameKey] = struct{}{}
			ref := catalog.SimilarArtistRef{ID: id, Name: name}
			if local, err := s.catalog.MusicArtist(id); err == nil {
				ref.Name = local.Name
				ref.Images = local.Images
			}
			refs = append(refs, ref)
		} else {
			seenNames[nameKey] = struct{}{}
			refs = append(refs, catalog.SimilarArtistRef{
				Name:     name,
				ImageURL: strings.TrimSpace(candidate.ImageURL),
				External: true,
			})
		}

		if len(refs) >= maxSimilar {
			break
		}
	}
	return refs
}

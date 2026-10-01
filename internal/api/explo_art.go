package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/explo"
)

// Covers beside Search for new results. A result is an album nobody has
// downloaded yet, so its cover is its catalog's — the Cover Art Archive's for
// a MusicBrainz release group, Deezer's for a deezer-<n> album — fetched by
// samo rather than the browser: from the server it leaves through the same
// route as all of samo's art (the VPN on the samo box, or the egress proxy for
// the hosts that refuse the VPN, like Deezer's image CDN), and a page of
// results that shares an album asks once. Nothing is stored: the small
// thumbnails live in memory for a day and are gone on restart.

const (
	exploArtMaxEntries = 400
	exploArtMaxBytes   = 1 << 20
	exploArtTTL        = 24 * time.Hour
	// A release group the archive has no front cover for is asked about
	// again after this long, not on every search that lists it.
	exploArtMissingTTL = time.Hour
	// exploArtFetches bounds the archive requests a page of results makes
	// at once; the rest wait their turn rather than open forty connections.
	exploArtFetches = 6
)

// exploArtURL is where an album's cover is: the archive's 250px front cover
// of a release group, or Deezer's 400px one. Both redirect to the image.
var exploArtURL = func(albumID string) string {
	if id := explo.DeezerID(albumID); id != "" {
		return "https://api.deezer.com/album/" + id + "/image?size=big"
	}
	return "https://coverartarchive.org/release-group/" + albumID + "/front-250"
}

type exploArtEntry struct {
	data        []byte
	contentType string
	fetchedAt   time.Time
	// failed is a fetch that says nothing about the cover: a timeout, the
	// archive down. Never cached, by samo or the browser.
	failed bool
	ready  chan struct{}
}

type exploArtCache struct {
	client  *http.Client
	mu      sync.Mutex
	entries map[string]*exploArtEntry
	order   []string
	slots   chan struct{}
}

// newExploArtCache fetches with client, the egress-aware one samo uses for
// all artwork; nil is a plain client.
func newExploArtCache(client *http.Client) *exploArtCache {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &exploArtCache{
		client:  client,
		entries: map[string]*exploArtEntry{},
		slots:   make(chan struct{}, exploArtFetches),
	}
}

// get returns the cover of a release group, nil when the archive has none.
// Concurrent asks for the same cover share one fetch.
func (c *exploArtCache) get(ctx context.Context, releaseGroupID string) (*exploArtEntry, error) {
	c.mu.Lock()
	entry, ok := c.entries[releaseGroupID]
	if ok {
		select {
		case <-entry.ready:
			ttl := exploArtTTL
			if entry.data == nil {
				ttl = exploArtMissingTTL
			}
			if time.Since(entry.fetchedAt) < ttl {
				c.mu.Unlock()
				return entry, nil
			}
			ok = false
		default:
		}
	}
	if !ok {
		entry = &exploArtEntry{ready: make(chan struct{})}
		c.entries[releaseGroupID] = entry
		c.order = append(c.order, releaseGroupID)
		for len(c.order) > exploArtMaxEntries {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
		c.mu.Unlock()
		go c.fetch(releaseGroupID, entry)
	} else {
		c.mu.Unlock()
	}
	select {
	case <-entry.ready:
		return entry, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fetch fills entry. It outlives the request that started it, so a browser
// that gives up on one image does not leave the cover half-fetched for the
// next one to ask.
func (c *exploArtCache) fetch(releaseGroupID string, entry *exploArtEntry) {
	defer close(entry.ready)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		c.forget(releaseGroupID, entry)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, exploArtURL(releaseGroupID), nil)
	if err != nil {
		c.forget(releaseGroupID, entry)
		return
	}
	req.Header.Set("User-Agent", "samo-server (https://github.com/bouliehaan/samo-server)")
	resp, err := c.client.Do(req)
	if err != nil {
		c.forget(releaseGroupID, entry)
		return
	}
	defer resp.Body.Close()
	entry.fetchedAt = time.Now()
	if resp.StatusCode == http.StatusNotFound {
		return // no cover: remembered as missing for a while
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(mediaType, "image/") {
		c.forget(releaseGroupID, entry)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, exploArtMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > exploArtMaxBytes {
		c.forget(releaseGroupID, entry)
		return
	}
	entry.data, entry.contentType = data, mediaType
}

// forget drops an entry whose fetch failed for a reason that says nothing
// about the cover (a timeout, the archive down), so the next ask tries again.
func (c *exploArtCache) forget(releaseGroupID string, entry *exploArtEntry) {
	entry.failed = true
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[releaseGroupID] == entry {
		delete(c.entries, releaseGroupID)
		for i, id := range c.order {
			if id == releaseGroupID {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
}

// serveExploArt answers an <img> beside a Search for new result. 404 when the
// archive has no cover: the page shows its own placeholder.
func (s *Server) serveExploArt(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	if !explo.ValidCatalogID(id) {
		writeError(w, 400, "invalid album ID")
		return
	}
	entry, err := s.exploArt.get(r.Context(), id)
	if err != nil {
		return // the browser went away
	}
	switch {
	case entry.failed:
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, 502, "the Cover Art Archive did not answer")
		return
	case entry.data == nil:
		w.Header().Set("Cache-Control", "private, max-age=3600")
		writeError(w, 404, "no cover for this album")
		return
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(entry.data)
}

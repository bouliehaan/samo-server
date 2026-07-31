package api

import (
	"context"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/subsonic"
	"github.com/bouliehaan/samo-server/internal/users"
)

// Adapters that let the Subsonic layer reuse the native handlers rather than
// growing its own path to disk or its own scrobble pipeline. Both delegate into
// the exact code the native API runs, so range handling, the library-root
// sandbox, cover fallback and playback reporting behave identically no matter
// which API a client speaks.

type subsonicStreamAdapter struct{ server *Server }

// StreamTrack reuses streamMusicTrack. That handler reads the track id from a
// path value, so the request is rewritten to carry it — cheaper and far safer
// than duplicating the file selection, offset and range logic.
func (a subsonicStreamAdapter) StreamTrack(w http.ResponseWriter, r *http.Request, principal users.Principal, trackID string) {
	// The native handler resolves the user from this package's context key, so
	// the Subsonic-authenticated principal is planted there before delegating.
	// Without this the request reaches streamMusicTrack with no user and 401s.
	r = r.WithContext(a.server.withPrincipal(r.Context(), principal))
	a.server.streamMusicTrack(w, withPathValue(r, "id", trackID))
}

// ServeCover resolves a Subsonic coverArt id, which may be a track, album,
// playlist or image id depending on which element it came from. Each is tried
// against the catalog before falling through to the generic image route.
func (a subsonicStreamAdapter) ServeCover(w http.ResponseWriter, r *http.Request, coverArtID string) {
	if album, err := a.server.catalog.MusicAlbum(coverArtID); err == nil {
		a.server.serveCatalogImage(w, r, a.server.catalog.MusicAlbumCoverImages(album.ID))
		return
	}
	if track, err := a.server.catalog.MusicTrack(coverArtID); err == nil && len(track.Images) > 0 {
		a.server.serveCatalogImage(w, r, track.Images)
		return
	}
	if images := a.server.catalog.MusicPlaylistCoverImages(coverArtID); len(images) > 0 {
		a.server.serveCatalogImage(w, r, images)
		return
	}
	a.server.serveMetadataImage(w, withPathValue(r, "id", coverArtID))
}

type subsonicScrobbleAdapter struct{ server *Server }

// Scrobble routes a Subsonic play report into the same last.fm pipeline the
// native API uses. submission=false is a now-playing ping; true is a completed
// play. Both are reported as a stream event, which is what the native path
// sends for the equivalent client action.
func (a subsonicScrobbleAdapter) Scrobble(ctx context.Context, userID, trackID string, submission bool) {
	if a.server.lastfm == nil || userID == "" {
		return
	}
	track, err := a.server.catalog.MusicTrack(trackID)
	if err != nil {
		return
	}
	source := "now-playing"
	if submission {
		source = "stream"
	}
	a.server.notifyMusicTrackLastFM(
		userID, track.ID,
		catalog.PlaybackState{}, catalog.PlaybackState{},
		nil, source, 0,
	)
}

// withPathValue returns r carrying a ServeMux path value, so a handler written
// against r.PathValue can be called from a route that did not define it.
func withPathValue(r *http.Request, key, value string) *http.Request {
	clone := r.Clone(r.Context())
	clone.SetPathValue(key, value)
	return clone
}

// registerSubsonic mounts the Subsonic surface if the server has the services
// it needs. It is additive: nothing about the native API changes.
func (s *Server) registerSubsonic() {
	if s.users == nil || s.catalog == nil {
		return
	}
	subsonic.New(subsonic.Options{
		Catalog:  s.catalog,
		Users:    s.users,
		Stream:   subsonicStreamAdapter{server: s},
		Scrobble: subsonicScrobbleAdapter{server: s},
	}).Register(s.mux)
}

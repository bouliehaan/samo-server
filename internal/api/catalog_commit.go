package api

import (
	"context"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The catalog write contract lives in this file, in one function, because it
// used to live in two or three separate calls that every handler had to
// remember to make in the right order.
//
// The steps are: write to the database, install the change into the in-memory
// projection, then tell every connected client. Miss the second and this
// server answers from a stale projection. Miss the third and every OTHER
// device stays stale until it is restarted — and nothing fails, nothing logs,
// and the next read looks perfectly normal.
//
// That is exactly what happened. The contract was worked out properly for
// playlists and then never generalised: publishCatalogChange had six call
// sites, all of them playlist-related, while deleting an album, applying
// metadata, adding a podcast feed, creating a collection and half a dozen
// others updated the projection and told nobody. Delete an album on the
// desktop and the phone still listed it until you killed the app.
//
// The lesson is not "remember the third call". A rule that has to be
// remembered is a rule that decays as the codebase grows, and this one decayed
// within one feature of being written. So the two steps a handler cannot see
// the effect of are now one call it cannot make halfway, and
// catalog_commit_test.go fails the build if a mutating handler skips it.

// catalogScope names the part of the catalog a change touched.
//
// The desktop treats "playlist" specially and invalidates broadly for anything
// else; Android ignores the scope and re-syncs on any change. So a new scope
// is safe to add — it degrades to "something changed", which is correct, just
// less precise than it could be.
type catalogScope string

const (
	scopeAlbum      catalogScope = "album"
	scopeAudiobook  catalogScope = "audiobook"
	scopeCollection catalogScope = "collection"
	scopeLibrary    catalogScope = "library"
	scopeMetadata   catalogScope = "metadata"
	scopePlaylist   catalogScope = "playlist"
	scopePodcast    catalogScope = "podcast"
	scopeStation    catalogScope = "station"
)

// catalogAction says what happened to it.
type catalogAction string

const (
	actionDeleted catalogAction = "deleted"
	actionUpdated catalogAction = "updated"
)

// projector installs a change into the live projection. Returning an error
// aborts the commit, so the notification is never sent for a change the
// projection rejected.
type projector func(context.Context) error

// commitCatalog performs the projection update and the client notification
// that every catalog write owes, in the one order that is correct.
//
// Pass a projector for a scope that has an incremental path; pass nil to fall
// back to the full reload. The full reload re-reads the entire library and
// rebuilds eleven maps and ten slice clones — about 6.1s on a 100k-track
// library, measured in catalog/service.go — so an incremental path is worth
// writing for anything a user does more than occasionally. Most scopes do not
// have one yet, and nil is the honest way to say so.
//
// Returns false when it has already answered the request with an error, so the
// caller returns without writing a body:
//
//	if !s.commitCatalog(w, r, scopeAlbum, actionDeleted, id, nil) {
//	    return
//	}
//	writeJSON(w, http.StatusOK, result)
func (s *Server) commitCatalog(
	w http.ResponseWriter,
	r *http.Request,
	scope catalogScope,
	action catalogAction,
	id string,
	project projector,
) bool {
	if project == nil {
		project = func(context.Context) error { return s.reloadCatalogProjection(r) }
	}
	if err := project(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	// Publish AFTER the projection, never before: a client acts on this by
	// refetching immediately, and a notification that outruns the projection
	// hands it the pre-change state and leaves it confidently stale.
	s.publishCatalogChange(r, string(scope), string(action), id)
	return true
}

// commitPlaylist is the incremental commit for a single changed playlist.
//
// Playlists are the one scope with a real incremental path — see
// catalog.Service.UpsertMusicPlaylist for why it was worth writing — so they
// get a named helper rather than an inline closure at every call site.
func (s *Server) commitPlaylist(w http.ResponseWriter, r *http.Request, playlist catalog.MusicPlaylist) bool {
	return s.commitCatalog(w, r, scopePlaylist, actionUpdated, playlist.ID, func(context.Context) error {
		return s.applyPlaylistProjection(r, playlist)
	})
}

// commitPlaylistRemoval is the deletion counterpart to commitPlaylist.
func (s *Server) commitPlaylistRemoval(w http.ResponseWriter, r *http.Request, id string) bool {
	return s.commitCatalog(w, r, scopePlaylist, actionDeleted, id, func(context.Context) error {
		return s.removePlaylistProjection(r, id)
	})
}

// noProjectionChange is the projector for a commit whose projection has already
// been refreshed by something else — a scan that ran inside the same handler,
// most often. It exists so those handlers still go through commitCatalog rather
// than reaching past it to publishCatalogChange, which is how the notification
// step got skipped everywhere else.
func noProjectionChange(context.Context) error { return nil }

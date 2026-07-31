package subsonic

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/users"
)

// CatalogReader is the slice of the catalog projection this API needs. Narrow
// on purpose: the Subsonic layer reads, it never mutates.
type CatalogReader interface {
	ListMusicArtists(page catalog.PageRequest) catalog.Page[catalog.MusicArtist]
	ListMusicAlbums(page catalog.PageRequest) catalog.Page[catalog.MusicAlbum]
	ListMusicTracks(page catalog.PageRequest) catalog.Page[catalog.MusicTrack]
	ListGenres(page catalog.PageRequest) catalog.Page[catalog.GenreSummary]
	MusicArtist(id string) (catalog.MusicArtist, error)
	MusicAlbum(id string) (catalog.MusicAlbum, error)
	MusicTrack(id string) (catalog.MusicTrack, error)
	MusicAlbumsForArtist(artistID string) []catalog.MusicAlbum
	MusicTracksForAlbum(albumID string) []catalog.MusicTrack
	MusicTracksForPlaylist(playlistID string) []catalog.MusicTrack
	ListMusicPlaylistsForUser(userID string, page catalog.PageRequest) catalog.Page[catalog.MusicPlaylist]
	MusicPlaylistForUser(userID, id string) (catalog.MusicPlaylist, error)
}

// StreamHandler serves audio and cover bytes. Both are delegated to the native
// handlers so Subsonic streaming goes through exactly the same file sandbox,
// range handling and cover resolution as everything else — there is no second
// path to disk.
type StreamHandler interface {
	// StreamTrack receives the resolved principal because the native handler it
	// delegates to reads the user from the API layer's own request context. The
	// Subsonic layer authenticates under a different scheme and cannot set that
	// key itself without an import cycle, so it hands the principal across
	// explicitly rather than leaving the native handler to find nobody there.
	StreamTrack(w http.ResponseWriter, r *http.Request, principal users.Principal, trackID string)
	ServeCover(w http.ResponseWriter, r *http.Request, coverArtID string)
}

// Scrobbler records playback reported by a Subsonic client.
type Scrobbler interface {
	Scrobble(ctx context.Context, userID, trackID string, submission bool)
}

type Options struct {
	Catalog  CatalogReader
	Users    *users.Service
	Stream   StreamHandler
	Scrobble Scrobbler
}

type Server struct {
	catalog  CatalogReader
	users    *users.Service
	stream   StreamHandler
	scrobble Scrobbler
}

func New(options Options) *Server {
	return &Server{
		catalog:  options.Catalog,
		users:    options.Users,
		stream:   options.Stream,
		scrobble: options.Scrobble,
	}
}

// Register mounts the Subsonic surface. Every action is available both bare and
// with the `.view` suffix, because clients are split on which they send.
func (s *Server) Register(mux *http.ServeMux) {
	for action, handler := range map[string]http.HandlerFunc{
		// ping and getLicense authenticate too. Clients use ping as their
		// "test connection" step, so leaving it open makes a wrong password
		// look like a successful setup and fail confusingly later.
		"ping":              s.authed(s.handlePing),
		"getLicense":        s.authed(s.handleGetLicense),
		"getMusicFolders":   s.authed(s.handleGetMusicFolders),
		"getIndexes":        s.authed(s.handleGetIndexes),
		"getArtists":        s.authed(s.handleGetArtists),
		"getArtist":         s.authed(s.handleGetArtist),
		"getAlbum":          s.authed(s.handleGetAlbum),
		"getSong":           s.authed(s.handleGetSong),
		"getMusicDirectory": s.authed(s.handleGetMusicDirectory),
		"getAlbumList":      s.authed(s.handleGetAlbumList),
		"getAlbumList2":     s.authed(s.handleGetAlbumList2),
		"getRandomSongs":    s.authed(s.handleGetRandomSongs),
		"getGenres":         s.authed(s.handleGetGenres),
		"search2":           s.authed(s.handleSearch2),
		"search3":           s.authed(s.handleSearch3),
		"getPlaylists":      s.authed(s.handleGetPlaylists),
		"getPlaylist":       s.authed(s.handleGetPlaylist),
		"stream":            s.authed(s.handleStream),
		"download":          s.authed(s.handleStream),
		"getCoverArt":       s.authed(s.handleGetCoverArt),
		"scrobble":          s.authed(s.handleScrobble),
		"getUser":           s.authed(s.handleGetUser),
		"getScanStatus":     s.authed(s.handleGetScanStatus),
		"getNowPlaying":     s.authed(s.handleGetNowPlaying),
		"getStarred":        s.authed(s.handleGetStarred),
		"getStarred2":       s.authed(s.handleGetStarred2),
	} {
		// Methods are explicit because Go's ServeMux rejects a method-less
		// pattern that overlaps the catch-all "GET /" route. Subsonic clients
		// use both verbs — most send GET, some POST the same parameters as a
		// form — so both are registered.
		for _, path := range []string{"/rest/" + action, "/rest/" + action + ".view"} {
			mux.HandleFunc("GET "+path, handler)
			mux.HandleFunc("POST "+path, handler)
		}
	}
}

type principalKey struct{}

// authed resolves Subsonic credentials before running the action. Auth failure
// is reported inside the envelope with code 40, not as an HTTP status — clients
// surface a non-200 as "server unreachable" rather than "wrong password".
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.users == nil {
			writeError(w, errCodeNotAuthorized, "user service unavailable")
			return
		}
		// A POSTed request carries its parameters as a form body; ParseForm
		// merges those with the query string so both verbs read the same way.
		_ = r.ParseForm()
		query := r.Form
		principal, err := s.users.AuthenticateSubsonic(
			r.Context(),
			query.Get("u"),
			query.Get("p"),
			query.Get("t"),
			query.Get("s"),
		)
		if err != nil {
			writeError(w, errCodeBadCredentials, "wrong username or password")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	}
}

func principalFrom(r *http.Request) users.Principal {
	principal, _ := r.Context().Value(principalKey{}).(users.Principal)
	return principal
}

// param reads a request parameter from either the query string or a POSTed
// form body, so every action works under both verbs.
func param(r *http.Request, key string) string {
	_ = r.ParseForm()
	return r.Form.Get(key)
}

// paramInt reads an integer query parameter, falling back when absent or
// malformed — Subsonic clients send a wide range of shapes for these.
func paramInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(param(r, key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

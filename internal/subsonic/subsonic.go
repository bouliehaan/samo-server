// Package subsonic implements the Subsonic / OpenSubsonic API over Samo's own
// catalog, so existing third-party clients (DSub, Substreamer, Symfonium,
// play:Sub, Sonixd, …) work against a Samo server without a Samo-specific
// client.
//
// This is a translation layer, not a second data model: every handler reads the
// same in-memory catalog projection the native API reads, and streams through
// the same files service with the same library-root sandbox. Nothing here owns
// state, and nothing here writes SQL.
//
// Scope is deliberate. Samo's native API is the one to build new clients
// against; this exists so a user's existing app keeps working. JSON only
// (`f=json`) — the XML envelope is not implemented, and no known modern client
// needs it.
package subsonic

import (
	"encoding/json"
	"net/http"
)

// apiVersion is the Subsonic protocol version we claim. 1.16.1 is the last
// published Subsonic version and what OpenSubsonic servers advertise.
const apiVersion = "1.16.1"

// serverType identifies this implementation to clients that branch on it (some
// enable OpenSubsonic extensions when they recognise the server).
const serverType = "samo-server"

// Subsonic error codes, from the published protocol. Clients switch on these,
// so the numbers matter more than the messages.
const (
	errCodeGeneric          = 0
	errCodeMissingParameter = 10
	errCodeClientTooOld     = 20
	errCodeServerTooOld     = 30
	errCodeBadCredentials   = 40
	errCodeNotAuthorized    = 50
	errCodeNotFound         = 70
)

// envelope is the `{"subsonic-response": {...}}` wrapper every response carries.
type envelope struct {
	Response body `json:"subsonic-response"`
}

// body holds the status fields plus at most one payload. Every payload field is
// omitempty so a response carries only the element the action defines — clients
// are strict about unexpected keys in some cases, and an empty object is not
// the same as an absent one.
type body struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Type          string `json:"type"`
	ServerVersion string `json:"serverVersion"`
	OpenSubsonic  bool   `json:"openSubsonic"`

	Error *apiError `json:"error,omitempty"`

	MusicFolders  *musicFolders   `json:"musicFolders,omitempty"`
	Indexes       *indexes        `json:"indexes,omitempty"`
	Artists       *artistsRoot    `json:"artists,omitempty"`
	Artist        *artistDetail   `json:"artist,omitempty"`
	Album         *albumDetail    `json:"album,omitempty"`
	Song          *child          `json:"song,omitempty"`
	Directory     *directory      `json:"directory,omitempty"`
	AlbumList     *albumList      `json:"albumList,omitempty"`
	AlbumList2    *albumList2     `json:"albumList2,omitempty"`
	RandomSongs   *songs          `json:"randomSongs,omitempty"`
	SongsByGenre  *songs          `json:"songsByGenre,omitempty"`
	Starred       *starred        `json:"starred,omitempty"`
	Starred2      *starred2       `json:"starred2,omitempty"`
	SearchResult2 *searchResult2  `json:"searchResult2,omitempty"`
	SearchResult3 *searchResult3  `json:"searchResult3,omitempty"`
	Playlists     *playlists      `json:"playlists,omitempty"`
	Playlist      *playlistDetail `json:"playlist,omitempty"`
	Genres        *genres         `json:"genres,omitempty"`
	License       *license        `json:"license,omitempty"`
	ScanStatus    *scanStatus     `json:"scanStatus,omitempty"`
	NowPlaying    *nowPlaying     `json:"nowPlaying,omitempty"`
	User          *userResponse   `json:"user,omitempty"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newBody() body {
	return body{
		Status:        "ok",
		Version:       apiVersion,
		Type:          serverType,
		ServerVersion: "0.1",
		OpenSubsonic:  true,
	}
}

// write emits a successful response with whatever payload the caller filled in.
func write(w http.ResponseWriter, payload body) {
	payload.Status = "ok"
	payload.Version = apiVersion
	payload.Type = serverType
	payload.OpenSubsonic = true
	writeEnvelope(w, http.StatusOK, payload)
}

// writeError emits a Subsonic error. The HTTP status stays 200: the protocol
// carries failure in the body, and clients that see a non-200 tend to report
// "server unreachable" rather than the actual reason.
func writeError(w http.ResponseWriter, code int, message string) {
	payload := newBody()
	payload.Status = "failed"
	payload.Error = &apiError{Code: code, Message: message}
	writeEnvelope(w, http.StatusOK, payload)
}

func writeEnvelope(w http.ResponseWriter, status int, payload body) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Response: payload})
}

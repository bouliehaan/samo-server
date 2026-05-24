package subsonic

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/search"
	"github.com/bouliehaan/samo-server/internal/users"
)

const (
	apiVersion    = "1.16.1"
	serverType    = "samo-server"
	defaultFolder = "0"
)

type Options struct {
	Catalog       *catalog.Service
	Search        *search.Service
	Libraries     *libraries.Service
	Files         *files.Service
	Playback      *playback.Service
	LastFM        *lastfm.Service
	Users         *users.Service
	APIToken      string
	ServerVersion string
}

type Server struct {
	catalog       *catalog.Service
	search        *search.Service
	libraries     *libraries.Service
	files         *files.Service
	playback      *playback.Service
	lastfm        *lastfm.Service
	users         *users.Service
	apiToken      string
	serverVersion string
}

func New(options Options) *Server {
	version := strings.TrimSpace(options.ServerVersion)
	if version == "" {
		version = "dev"
	}
	return &Server{
		catalog:       options.Catalog,
		search:        options.Search,
		libraries:     options.Libraries,
		files:         options.Files,
		playback:      options.Playback,
		lastfm:        options.LastFM,
		users:         options.Users,
		apiToken:      strings.TrimSpace(options.APIToken),
		serverVersion: version,
	}
}

func (s *Server) searchService() *search.Service {
	if s.search == nil {
		panic("search service is not configured")
	}
	return s.search
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := normalizeAction(r.PathValue("action"))
	if action == "" {
		action = normalizeAction(strings.TrimPrefix(r.URL.Path, "/rest/"))
	}
	if action == "" {
		s.writeFailed(w, 0, "missing action")
		return
	}

	switch action {
	case "stream", "getcoverart":
		var ok bool
		r, ok = s.authorize(r)
		if !ok {
			s.writeFailed(w, 40, "Wrong username or password")
			return
		}
		if s.files == nil {
			s.writeFailed(w, 0, "media streaming is not configured")
			return
		}
		if action == "stream" {
			s.stream(w, r)
			return
		}
		s.coverArt(w, r)
		return
	}

	var ok bool
	r, ok = s.authorize(r)
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}

	switch action {
	case "ping":
		s.writeOK(w, responseBody{})
	case "getlicense":
		s.writeOK(w, responseBody{License: &license{Valid: true, Email: "samo@localhost", Expires: "2099-12-31"}})
	case "getmusicfolders":
		s.getMusicFolders(w, r)
	case "getindexes":
		s.getIndexes(w, r)
	case "getartists":
		s.getArtists(w, r)
	case "getartist":
		s.getArtist(w, r)
	case "getalbum":
		s.getAlbum(w, r)
	case "getalbumlist2":
		s.getAlbumList2(w, r)
	case "getmusicdirectory":
		s.getMusicDirectory(w, r)
	case "getsong":
		s.getSong(w, r)
	case "search2":
		s.search2(w, r)
	case "search3":
		s.search3(w, r)
	case "getplaylists":
		s.getPlaylists(w, r)
	case "getplaylist":
		s.getPlaylist(w, r)
	case "getstarred":
		s.getStarred(w, r)
	case "getstarred2":
		s.getStarred(w, r)
	case "star":
		s.star(w, r)
	case "unstar":
		s.unstar(w, r)
	case "setrating":
		s.setRating(w, r)
	case "getrandomsongs":
		s.getRandomSongs(w, r)
	case "getopensubsonicextensions":
		s.writeOK(w, responseBody{OpenSubsonicExtensions: &openSubsonicExtensions{OpenSubsonicExtension: nil}})
	case "scrobble":
		s.scrobble(w, r)
	case "updatenowplaying":
		s.updateNowPlaying(w, r)
	default:
		s.writeFailed(w, 0, "unknown endpoint: "+action)
	}
}

func normalizeAction(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimSuffix(raw, ".view")
	return raw
}

func (s *Server) getMusicFolders(w http.ResponseWriter, r *http.Request) {
	folders := []musicFolder{{ID: defaultFolder, Name: "Music"}}
	if s.libraries != nil {
		page, err := s.libraries.List(r.Context(), 500, 0)
		if err == nil {
			folders = folders[:0]
			for _, item := range page.Items {
				if item.Kind != libraries.KindMusic {
					continue
				}
				folders = append(folders, musicFolder{ID: item.ID, Name: item.Name})
			}
			if len(folders) == 0 {
				folders = []musicFolder{{ID: defaultFolder, Name: "Music"}}
			}
		}
	}
	s.writeOK(w, responseBody{MusicFolders: &musicFolders{MusicFolder: folders}})
}

func (s *Server) getArtists(w http.ResponseWriter, r *http.Request) {
	page := s.catalog.ListMusicArtists(catalog.PageRequest{Limit: 500})
	index := buildArtistIndex(page.Items)
	s.writeOK(w, responseBody{Artists: &index})
}

func (s *Server) getIndexes(w http.ResponseWriter, r *http.Request) {
	page := s.catalog.ListMusicArtists(catalog.PageRequest{Limit: 500})
	index := buildArtistIndex(page.Items)
	s.writeOK(w, responseBody{Indexes: &index})
}

func (s *Server) getArtist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	artistItem, err := s.catalog.MusicArtist(id)
	if err != nil {
		s.writeFailed(w, 70, "artist not found")
		return
	}

	albums := s.catalog.MusicAlbumsForArtist(id)
	children := make([]child, 0, len(albums))
	for _, album := range albums {
		children = append(children, toArtistAlbumChild(album))
	}

	s.writeOK(w, responseBody{Artist: &artistDetail{
		ID:         artistItem.ID,
		Name:       artistItem.Name,
		AlbumCount: len(children),
		CoverArt:   artistItem.ID,
		Album:      children,
	}})
}

func (s *Server) getAlbum(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	album, err := s.catalog.MusicAlbum(id)
	if err != nil {
		s.writeFailed(w, 70, "album not found")
		return
	}

	tracks := s.catalog.MusicTracksForAlbum(id)
	songs := make([]child, 0, len(tracks))
	for _, track := range tracks {
		songs = append(songs, toSongChild(track))
	}

	s.writeOK(w, responseBody{Album: &albumDetail{
		ID:        album.ID,
		Name:      album.Title,
		Artist:    displayArtist(album.DisplayArtist, album.ArtistNames, album.AlbumArtistNames),
		ArtistID:  firstID(album.ArtistIDs, album.AlbumArtistIDs),
		CoverArt:  album.ID,
		SongCount: len(songs),
		Duration:  album.DurationSeconds,
		Year:      album.ReleaseYear,
		Genre:     firstGenre(album.Genres),
		Song:      songs,
	}})
}

func (s *Server) getMusicDirectory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}

	if album, err := s.catalog.MusicAlbum(id); err == nil {
		tracks := s.catalog.MusicTracksForAlbum(id)
		children := make([]child, 0, len(tracks))
		for _, track := range tracks {
			children = append(children, toSongChild(track))
		}
		s.writeOK(w, responseBody{Directory: &directory{
			ID:    album.ID,
			Name:  album.Title,
			Child: children,
		}})
		return
	}

	if artist, err := s.catalog.MusicArtist(id); err == nil {
		albums := s.catalog.MusicAlbumsForArtist(id)
		children := make([]child, 0, len(albums))
		for _, album := range albums {
			children = append(children, toArtistAlbumChild(album))
		}
		s.writeOK(w, responseBody{Directory: &directory{
			ID:    artist.ID,
			Name:  artist.Name,
			Child: children,
		}})
		return
	}

	s.writeFailed(w, 70, "directory not found")
}

func (s *Server) getAlbumList2(w http.ResponseWriter, r *http.Request) {
	listType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	switch listType {
	case "starred":
		s.albumListFromBrowse(w, r, catalog.MusicBrowseStarred)
		return
	case "frequent":
		s.albumListFromBrowse(w, r, catalog.MusicBrowseRecentlyPlayed)
		return
	}

	page := s.catalog.ListMusicAlbums(catalog.PageRequest{Limit: 500})
	albums := append([]catalog.MusicAlbum(nil), page.Items...)

	switch listType {
	case "newest", "recent":
		sort.SliceStable(albums, func(i, j int) bool {
			return albums[i].ReleaseYear > albums[j].ReleaseYear
		})
	case "random":
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		rng.Shuffle(len(albums), func(i, j int) {
			albums[i], albums[j] = albums[j], albums[i]
		})
	default:
		sort.SliceStable(albums, func(i, j int) bool {
			return strings.ToLower(albums[i].Title) < strings.ToLower(albums[j].Title)
		})
	}

	size := intQuery(r, "size", 10)
	offset := intQuery(r, "offset", 0)
	if offset > len(albums) {
		albums = nil
	} else {
		end := offset + size
		if end > len(albums) {
			end = len(albums)
		}
		albums = albums[offset:end]
	}

	children := make([]child, 0, len(albums))
	for _, album := range albums {
		children = append(children, toAlbumChild(album))
	}
	s.writeOK(w, responseBody{AlbumList2: &albumList2{Album: children}})
}

func (s *Server) getSong(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	track, err := s.catalog.MusicTrack(id)
	if err != nil {
		s.writeFailed(w, 70, "song not found")
		return
	}
	item := toSong(track)
	s.writeOK(w, responseBody{Song: &item})
}

func (s *Server) search2(w http.ResponseWriter, r *http.Request) {
	s.writeSearch(w, r, 2)
}

func (s *Server) search3(w http.ResponseWriter, r *http.Request) {
	s.writeSearch(w, r, 3)
}

func (s *Server) writeSearch(w http.ResponseWriter, r *http.Request, version int) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		query = strings.TrimSpace(r.URL.Query().Get("q"))
	}
	results := s.searchService().SearchMusicText(query, catalog.PageRequest{Limit: intQuery(r, "count", 20), Offset: intQuery(r, "offset", 0)})

	artists := make([]artist, 0, len(results.Artists))
	for _, item := range results.Artists {
		artists = append(artists, toArtist(item))
	}
	albums := make([]child, 0, len(results.Albums))
	for _, item := range results.Albums {
		albums = append(albums, toAlbumChild(item))
	}
	songs := make([]child, 0, len(results.Tracks))
	for _, item := range results.Tracks {
		songs = append(songs, toSongChild(item))
	}

	body := responseBody{}
	if version == 3 {
		body.SearchResult3 = &searchResult3{Artist: artists, Album: albums, Song: songs}
	} else {
		body.SearchResult2 = &searchResult2{Artist: artists, Album: albums, Song: songs}
	}
	s.writeOK(w, body)
}

func (s *Server) getPlaylists(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	page := s.catalog.ListMusicPlaylistsForUser(principal.User.ID, catalog.PageRequest{Limit: 500})
	items := make([]playlistSummary, 0, len(page.Items))
	for _, playlist := range page.Items {
		items = append(items, playlistSummary{
			ID:        playlist.ID,
			Name:      playlist.Name,
			Owner:     playlist.OwnerID,
			Public:    playlist.Public,
			SongCount: playlist.TrackCount,
			Duration:  playlist.DurationSeconds,
		})
	}
	s.writeOK(w, responseBody{Playlists: &playlists{Playlist: items}})
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	playlistItem, err := s.catalog.MusicPlaylistForUser(principal.User.ID, id)
	if err != nil {
		s.writeFailed(w, 70, "playlist not found")
		return
	}

	tracks := s.catalog.MusicTracksForPlaylist(id)
	entries := make([]child, 0, len(tracks))
	for _, track := range tracks {
		entries = append(entries, toSongChild(track))
	}

	s.writeOK(w, responseBody{Playlist: &playlist{
		ID:        playlistItem.ID,
		Name:      playlistItem.Name,
		Owner:     playlistItem.OwnerID,
		Public:    playlistItem.Public,
		SongCount: len(entries),
		Duration:  playlistItem.DurationSeconds,
		Entry:     entries,
	}})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	track, err := s.catalog.MusicTrack(id)
	if err != nil {
		s.writeFailed(w, 70, "song not found")
		return
	}
	if len(track.AudioFiles) == 0 {
		s.writeFailed(w, 70, "song not found")
		return
	}

	target, err := catalog.SelectStreamTarget(track.AudioFiles, track.Playback, catalog.StreamSelectQueryFromRequest(r), track.DiscNumber)
	if err != nil {
		s.writeFailed(w, 0, err.Error())
		return
	}
	if s.lastfm != nil && s.lastfm.Enabled() {
		resume := target.GlobalSeconds
		if resume <= 0 {
			resume = target.OffsetSeconds
		}
		if principal, ok := principalFromContext(r.Context()); ok {
			s.lastfm.HandleStreamStart(r.Context(), principal.User.ID, track, resume)
		}
	}
	if err := s.files.ServeMediaFileAt(r.Context(), target.FileID, target.OffsetSeconds, w, r); err != nil {
		s.writeFilesFailed(w, err)
	}
}

func (s *Server) coverArt(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	_, images := s.catalog.ResolveMusicCoverArtID(id)
	path := firstImagePath(images)
	if path == "" {
		s.writeFailed(w, 70, "cover art not found")
		return
	}
	if err := s.files.ServeLocalPath(r.Context(), path, w, r); err != nil {
		s.writeFilesFailed(w, err)
	}
}

func (s *Server) scrobble(w http.ResponseWriter, r *http.Request) {
	if s.lastfm == nil || !s.lastfm.Enabled() {
		s.writeFailed(w, 0, "last.fm scrobbling is not configured")
		return
	}
	track, playedAt, err := s.resolveSubsonicScrobbleTrack(r)
	if err != nil {
		s.writeFailed(w, 70, err.Error())
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	if err := s.lastfm.SubmitScrobble(r.Context(), principal.User.ID, track, playedAt, 0, "subsonic-compat"); err != nil {
		s.writeFailed(w, 0, err.Error())
		return
	}
	s.writeOK(w, responseBody{})
}

func (s *Server) updateNowPlaying(w http.ResponseWriter, r *http.Request) {
	if s.lastfm == nil || !s.lastfm.Enabled() {
		s.writeFailed(w, 0, "last.fm scrobbling is not configured")
		return
	}
	track, _, err := s.resolveSubsonicScrobbleTrack(r)
	if err != nil {
		s.writeFailed(w, 70, err.Error())
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	if err := s.lastfm.SubmitNowPlaying(r.Context(), principal.User.ID, track, "subsonic-compat"); err != nil {
		s.writeFailed(w, 0, err.Error())
		return
	}
	s.writeOK(w, responseBody{})
}

func (s *Server) resolveSubsonicScrobbleTrack(r *http.Request) (catalog.MusicTrack, time.Time, error) {
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		track, err := s.catalog.MusicTrack(id)
		if err != nil {
			return catalog.MusicTrack{}, time.Time{}, err
		}
		return track, subsonicScrobbleTime(r), nil
	}
	artist := strings.TrimSpace(r.URL.Query().Get("artist"))
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if artist == "" || title == "" {
		return catalog.MusicTrack{}, time.Time{}, fmt.Errorf("id or artist+title required")
	}
	results := s.searchService().SearchMusicText(title, catalog.PageRequest{Limit: 20})
	for _, track := range results.Tracks {
		if strings.EqualFold(strings.Join(track.ArtistNames, ", "), artist) || strings.EqualFold(track.DisplayArtist, artist) {
			if strings.EqualFold(track.Title, title) {
				return track, subsonicScrobbleTime(r), nil
			}
		}
	}
	return catalog.MusicTrack{
		ID:          stableSubsonicScrobbleID(artist, title),
		Title:       title,
		ArtistNames: []string{artist},
		AlbumTitle:  strings.TrimSpace(r.URL.Query().Get("album")),
	}, subsonicScrobbleTime(r), nil
}

func subsonicScrobbleTime(r *http.Request) time.Time {
	if raw := strings.TrimSpace(r.URL.Query().Get("time")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			return time.Unix(seconds, 0).UTC()
		}
	}
	return time.Now().UTC()
}

func stableSubsonicScrobbleID(artist, title string) string {
	return fmt.Sprintf("subsonic:%s:%s", strings.ToLower(artist), strings.ToLower(title))
}

func (s *Server) writeOK(w http.ResponseWriter, body responseBody) {
	body.Status = "ok"
	body.Version = apiVersion
	body.Type = serverType
	if body.ServerVersion == "" {
		body.ServerVersion = s.serverVersion
	}
	writeEnvelope(w, http.StatusOK, body)
}

func (s *Server) writeFailed(w http.ResponseWriter, code int, message string) {
	body := responseBody{
		Status:        "failed",
		Version:       apiVersion,
		Type:          serverType,
		ServerVersion: s.serverVersion,
		Error:         &errorPayload{Code: code, Message: message},
	}
	writeEnvelope(w, http.StatusOK, body)
}

func writeEnvelope(w http.ResponseWriter, status int, body responseBody) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(envelope{Response: body}); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func intQuery(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func (s *Server) writeFilesFailed(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, files.ErrNotFound), errors.Is(err, files.ErrMissing):
		s.writeFailed(w, 70, "media not found")
	default:
		s.writeFailed(w, 0, err.Error())
	}
}

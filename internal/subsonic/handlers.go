package subsonic

import (
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// allItems is the page size used when reading the whole catalog. The projection
// is already in memory, so this is a slice walk rather than a query, and
// Subsonic's browse actions are defined over complete collections.
const allItems = 100000

func fullPage() catalog.PageRequest { return catalog.PageRequest{Limit: allItems} }

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	write(w, newBody())
}

func (s *Server) handleGetLicense(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	// Samo is GPL-3.0 and has no licensing gate; reporting invalid here makes
	// several clients refuse to proceed at all.
	payload.License = &license{Valid: true}
	write(w, payload)
}

// Samo models libraries, not Subsonic "music folders". One synthetic folder
// keeps clients that insist on picking one working without inventing a mapping
// that would then have to stay stable forever.
func (s *Server) handleGetMusicFolders(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	payload.MusicFolders = &musicFolders{
		MusicFolder: []musicFolder{{ID: 0, Name: "Music"}},
	}
	write(w, payload)
}

func (s *Server) handleGetIndexes(w http.ResponseWriter, _ *http.Request) {
	grouped := s.artistIndex()
	entries := make([]indexEntry, 0, len(grouped))
	for _, g := range grouped {
		entries = append(entries, indexEntry{Name: g.Name, Artist: g.Artist})
	}
	payload := newBody()
	payload.Indexes = &indexes{IgnoredArticles: "The El La Los Las Le Les", Index: entries}
	write(w, payload)
}

func (s *Server) handleGetArtists(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	payload.Artists = &artistsRoot{
		IgnoredArticles: "The El La Los Las Le Les",
		Index:           s.artistIndex(),
	}
	write(w, payload)
}

// artistIndex groups artists under their first letter, which is the shape both
// getIndexes and getArtists want.
func (s *Server) artistIndex() []artistIndex {
	artists := s.catalog.ListMusicArtists(fullPage()).Items
	buckets := map[string][]artistItem{}
	for _, artist := range artists {
		buckets[indexLetter(artist.Name)] = append(buckets[indexLetter(artist.Name)], artistItem{
			ID:         artist.ID,
			Name:       artist.Name,
			CoverArt:   artist.ID,
			AlbumCount: len(s.catalog.MusicAlbumsForArtist(artist.ID)),
		})
	}
	letters := make([]string, 0, len(buckets))
	for letter := range buckets {
		letters = append(letters, letter)
	}
	sort.Strings(letters)

	out := make([]artistIndex, 0, len(letters))
	for _, letter := range letters {
		items := buckets[letter]
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		out = append(out, artistIndex{Name: letter, Artist: items})
	}
	return out
}

// indexLetter picks the bucket an artist sorts under, ignoring a leading
// article so "The Beatles" files under B the way every music app does.
func indexLetter(name string) string {
	name = strings.TrimSpace(name)
	for _, article := range []string{"the ", "a ", "an ", "el ", "la ", "los ", "las ", "le ", "les "} {
		if len(name) > len(article) && strings.EqualFold(name[:len(article)], article) {
			name = name[len(article):]
			break
		}
	}
	if name == "" {
		return "#"
	}
	first := []rune(strings.ToUpper(name))[0]
	if !unicode.IsLetter(first) {
		return "#"
	}
	return string(first)
}

func (s *Server) handleGetArtist(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")
	artist, err := s.catalog.MusicArtist(id)
	if err != nil {
		writeError(w, errCodeNotFound, "artist not found")
		return
	}
	albums := s.catalog.MusicAlbumsForArtist(artist.ID)
	items := make([]albumItem, 0, len(albums))
	for _, album := range albums {
		items = append(items, s.albumItem(album))
	}
	payload := newBody()
	payload.Artist = &artistDetail{
		artistItem: artistItem{ID: artist.ID, Name: artist.Name, CoverArt: artist.ID, AlbumCount: len(albums)},
		Album:      items,
	}
	write(w, payload)
}

func (s *Server) handleGetAlbum(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")
	album, err := s.catalog.MusicAlbum(id)
	if err != nil {
		writeError(w, errCodeNotFound, "album not found")
		return
	}
	tracks := s.catalog.MusicTracksForAlbum(album.ID)
	songs := make([]child, 0, len(tracks))
	for _, track := range tracks {
		songs = append(songs, s.child(track))
	}
	payload := newBody()
	payload.Album = &albumDetail{albumItem: s.albumItem(album), Song: songs}
	write(w, payload)
}

func (s *Server) handleGetSong(w http.ResponseWriter, r *http.Request) {
	track, err := s.catalog.MusicTrack(param(r, "id"))
	if err != nil {
		writeError(w, errCodeNotFound, "song not found")
		return
	}
	item := s.child(track)
	payload := newBody()
	payload.Song = &item
	write(w, payload)
}

// getMusicDirectory is the older browse model: an id is either an artist (whose
// children are albums) or an album (whose children are songs).
func (s *Server) handleGetMusicDirectory(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")

	if artist, err := s.catalog.MusicArtist(id); err == nil {
		albums := s.catalog.MusicAlbumsForArtist(artist.ID)
		children := make([]child, 0, len(albums))
		for _, album := range albums {
			children = append(children, child{
				ID: album.ID, Parent: artist.ID, IsDir: true, Title: album.Title,
				Album: album.Title, Artist: artist.Name, CoverArt: album.ID,
				Year: album.ReleaseYear, AlbumID: album.ID, ArtistID: artist.ID,
			})
		}
		payload := newBody()
		payload.Directory = &directory{ID: artist.ID, Name: artist.Name, Child: children}
		write(w, payload)
		return
	}

	if album, err := s.catalog.MusicAlbum(id); err == nil {
		tracks := s.catalog.MusicTracksForAlbum(album.ID)
		children := make([]child, 0, len(tracks))
		for _, track := range tracks {
			children = append(children, s.child(track))
		}
		payload := newBody()
		payload.Directory = &directory{ID: album.ID, Name: album.Title, Child: children}
		write(w, payload)
		return
	}

	writeError(w, errCodeNotFound, "directory not found")
}

func (s *Server) handleGetAlbumList(w http.ResponseWriter, r *http.Request) {
	albums := s.sortedAlbums(r)
	items := make([]child, 0, len(albums))
	for _, album := range albums {
		items = append(items, child{
			ID: album.ID, IsDir: true, Title: album.Title, Album: album.Title,
			Artist: album.DisplayArtist, CoverArt: album.ID, Year: album.ReleaseYear,
			AlbumID: album.ID,
		})
	}
	payload := newBody()
	payload.AlbumList = &albumList{Album: items}
	write(w, payload)
}

func (s *Server) handleGetAlbumList2(w http.ResponseWriter, r *http.Request) {
	albums := s.sortedAlbums(r)
	items := make([]albumItem, 0, len(albums))
	for _, album := range albums {
		items = append(items, s.albumItem(album))
	}
	payload := newBody()
	payload.AlbumList2 = &albumList2{Album: items}
	write(w, payload)
}

// sortedAlbums applies the `type` ordering plus size/offset paging that both
// album-list actions share.
func (s *Server) sortedAlbums(r *http.Request) []catalog.MusicAlbum {
	albums := s.catalog.ListMusicAlbums(fullPage()).Items

	switch strings.ToLower(param(r, "type")) {
	case "newest":
		sort.SliceStable(albums, func(i, j int) bool {
			return afterTime(albums[i].AddedAt, albums[j].AddedAt)
		})
	case "alphabeticalbyartist":
		sort.SliceStable(albums, func(i, j int) bool {
			return strings.ToLower(albums[i].DisplayArtist) < strings.ToLower(albums[j].DisplayArtist)
		})
	case "random":
		// Deliberately not shuffled: without a seeded source this would differ
		// per request and make paging incoherent. Ordered is a better answer
		// than incoherent.
		fallthrough
	default: // alphabeticalByName and anything unrecognised
		sort.SliceStable(albums, func(i, j int) bool {
			return strings.ToLower(albums[i].Title) < strings.ToLower(albums[j].Title)
		})
	}

	offset := paramInt(r, "offset", 0)
	size := paramInt(r, "size", 10)
	return pageSlice(albums, offset, size)
}

func (s *Server) handleGetRandomSongs(w http.ResponseWriter, r *http.Request) {
	tracks := s.catalog.ListMusicTracks(fullPage()).Items
	size := paramInt(r, "size", 10)
	items := make([]child, 0, size)
	for _, track := range pageSlice(tracks, 0, size) {
		items = append(items, s.child(track))
	}
	payload := newBody()
	payload.RandomSongs = &songs{Song: items}
	write(w, payload)
}

func (s *Server) handleGetGenres(w http.ResponseWriter, _ *http.Request) {
	list := s.catalog.ListGenres(fullPage()).Items
	items := make([]genreItem, 0, len(list))
	for _, g := range list {
		items = append(items, genreItem{Value: g.Name, SongCount: g.TrackCount, AlbumCount: g.AlbumCount})
	}
	payload := newBody()
	payload.Genres = &genres{Genre: items}
	write(w, payload)
}

func (s *Server) handleSearch2(w http.ResponseWriter, r *http.Request) {
	artists, albums, tracks := s.search(r)
	albumChildren := make([]child, 0, len(albums))
	for _, album := range albums {
		albumChildren = append(albumChildren, child{
			ID: album.ID, IsDir: true, Title: album.Title, Album: album.Title,
			Artist: album.DisplayArtist, CoverArt: album.ID, AlbumID: album.ID,
		})
	}
	payload := newBody()
	payload.SearchResult2 = &searchResult2{Artist: artists, Album: albumChildren, Song: tracks}
	write(w, payload)
}

func (s *Server) handleSearch3(w http.ResponseWriter, r *http.Request) {
	artists, albums, tracks := s.search(r)
	items := make([]albumItem, 0, len(albums))
	for _, album := range albums {
		items = append(items, s.albumItem(album))
	}
	payload := newBody()
	payload.SearchResult3 = &searchResult3{Artist: artists, Album: items, Song: tracks}
	write(w, payload)
}

// search is a substring match across the three entity types, honouring the
// per-type counts and offsets the protocol defines.
func (s *Server) search(r *http.Request) ([]artistItem, []catalog.MusicAlbum, []child) {
	query := strings.ToLower(strings.TrimSpace(param(r, "query")))
	// Subsonic clients send `""` to mean "everything" when populating a browse
	// view, so an empty query matches rather than returning nothing.
	matches := func(s string) bool {
		return query == "" || strings.Contains(strings.ToLower(s), query)
	}

	var artists []artistItem
	for _, artist := range s.catalog.ListMusicArtists(fullPage()).Items {
		if matches(artist.Name) {
			artists = append(artists, artistItem{ID: artist.ID, Name: artist.Name, CoverArt: artist.ID})
		}
	}
	var albums []catalog.MusicAlbum
	for _, album := range s.catalog.ListMusicAlbums(fullPage()).Items {
		if matches(album.Title) || matches(album.DisplayArtist) {
			albums = append(albums, album)
		}
	}
	var tracks []child
	for _, track := range s.catalog.ListMusicTracks(fullPage()).Items {
		if matches(track.Title) || matches(track.DisplayArtist) || matches(track.AlbumTitle) {
			tracks = append(tracks, s.child(track))
		}
	}

	artists = pageSlice(artists, paramInt(r, "artistOffset", 0), paramInt(r, "artistCount", 20))
	albums = pageSlice(albums, paramInt(r, "albumOffset", 0), paramInt(r, "albumCount", 20))
	tracks = pageSlice(tracks, paramInt(r, "songOffset", 0), paramInt(r, "songCount", 20))
	return artists, albums, tracks
}

func (s *Server) handleGetPlaylists(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r)
	list := s.catalog.ListMusicPlaylistsForUser(principal.User.ID, fullPage()).Items
	items := make([]playlistItem, 0, len(list))
	for _, pl := range list {
		items = append(items, s.playlistItem(pl, principal.User.Username))
	}
	payload := newBody()
	payload.Playlists = &playlists{Playlist: items}
	write(w, payload)
}

func (s *Server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r)
	pl, err := s.catalog.MusicPlaylistForUser(principal.User.ID, param(r, "id"))
	if err != nil {
		writeError(w, errCodeNotFound, "playlist not found")
		return
	}
	tracks := s.catalog.MusicTracksForPlaylist(pl.ID)
	entries := make([]child, 0, len(tracks))
	for _, track := range tracks {
		entries = append(entries, s.child(track))
	}
	payload := newBody()
	payload.Playlist = &playlistDetail{
		playlistItem: s.playlistItem(pl, principal.User.Username),
		Entry:        entries,
	}
	write(w, payload)
}

// handleStream delegates to the native streaming handler so range requests, the
// library-root sandbox and content types behave identically to the native API.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.stream == nil {
		writeError(w, errCodeGeneric, "streaming unavailable")
		return
	}
	id := strings.TrimSpace(param(r, "id"))
	if id == "" {
		writeError(w, errCodeMissingParameter, "id is required")
		return
	}
	s.stream.StreamTrack(w, r, principalFrom(r), id)
}

func (s *Server) handleGetCoverArt(w http.ResponseWriter, r *http.Request) {
	if s.stream == nil {
		writeError(w, errCodeGeneric, "cover art unavailable")
		return
	}
	id := strings.TrimSpace(param(r, "id"))
	if id == "" {
		writeError(w, errCodeMissingParameter, "id is required")
		return
	}
	s.stream.ServeCover(w, r, id)
}

// handleScrobble accepts both the now-playing ping (submission=false) and the
// real play submission, and hands them to the same pipeline the native API uses.
func (s *Server) handleScrobble(w http.ResponseWriter, r *http.Request) {
	if s.scrobble == nil {
		write(w, newBody())
		return
	}
	id := strings.TrimSpace(param(r, "id"))
	if id == "" {
		writeError(w, errCodeMissingParameter, "id is required")
		return
	}
	submission := strings.ToLower(strings.TrimSpace(param(r, "submission")))
	s.scrobble.Scrobble(r.Context(), principalFrom(r).User.ID, id, submission != "false")
	write(w, newBody())
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r)
	payload := newBody()
	payload.User = &userResponse{
		Username:          principal.User.Username,
		ScrobblingEnabled: true,
		AdminRole:         principal.User.Role == "admin",
		SettingsRole:      principal.User.Role == "admin",
		DownloadRole:      true,
		PlaylistRole:      true,
		CoverArtRole:      true,
		StreamRole:        true,
		ScanRole:          principal.User.Role == "admin",
	}
	write(w, payload)
}

func (s *Server) handleGetScanStatus(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	payload.ScanStatus = &scanStatus{Scanning: false}
	write(w, payload)
}

func (s *Server) handleGetNowPlaying(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	payload.NowPlaying = &nowPlaying{Entry: []child{}}
	write(w, payload)
}

// Starred content is a native Samo concept the Subsonic layer does not yet
// project. Returning empty collections is correct and keeps clients working;
// returning an error makes several of them abort their whole sync.
func (s *Server) handleGetStarred(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	payload.Starred = &starred{Artist: []artistItem{}, Album: []child{}, Song: []child{}}
	write(w, payload)
}

func (s *Server) handleGetStarred2(w http.ResponseWriter, _ *http.Request) {
	payload := newBody()
	payload.Starred2 = &starred2{Artist: []artistItem{}, Album: []albumItem{}, Song: []child{}}
	write(w, payload)
}

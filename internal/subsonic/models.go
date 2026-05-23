package subsonic

type envelope struct {
	Response responseBody `json:"subsonic-response"`
}

type responseBody struct {
	Status                 string                  `json:"status"`
	Version                string                  `json:"version"`
	Type                   string                  `json:"type,omitempty"`
	ServerVersion          string                  `json:"serverVersion,omitempty"`
	Error                  *errorPayload           `json:"error,omitempty"`
	License                *license                `json:"license,omitempty"`
	MusicFolders           *musicFolders           `json:"musicFolders,omitempty"`
	Artists                *artistsIndex           `json:"artists,omitempty"`
	Indexes                *artistsIndex           `json:"indexes,omitempty"`
	Artist                 *artistDetail           `json:"artist,omitempty"`
	Album                  *albumDetail            `json:"album,omitempty"`
	Directory              *directory              `json:"directory,omitempty"`
	AlbumList2             *albumList2             `json:"albumList2,omitempty"`
	Song                   *song                   `json:"song,omitempty"`
	SearchResult2          *searchResult2          `json:"searchResult2,omitempty"`
	SearchResult3          *searchResult3          `json:"searchResult3,omitempty"`
	Playlists              *playlists              `json:"playlists,omitempty"`
	Playlist               *playlist               `json:"playlist,omitempty"`
	OpenSubsonicExtensions *openSubsonicExtensions `json:"openSubsonicExtensions,omitempty"`
}

type errorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type license struct {
	Valid   bool   `json:"valid"`
	Email   string `json:"email,omitempty"`
	Expires string `json:"expires,omitempty"`
	License string `json:"license,omitempty"`
}

type musicFolders struct {
	MusicFolder []musicFolder `json:"musicFolder"`
}

type musicFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type artistsIndex struct {
	LastModified int64        `json:"lastModified,omitempty"`
	Index        []indexGroup `json:"index"`
}

type indexGroup struct {
	Name   string   `json:"name"`
	Artist []artist `json:"artist"`
}

type artist struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AlbumCount int    `json:"albumCount,omitempty"`
	CoverArt   string `json:"coverArt,omitempty"`
}

type artistDetail struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	AlbumCount int     `json:"albumCount,omitempty"`
	CoverArt   string  `json:"coverArt,omitempty"`
	Album      []child `json:"album,omitempty"`
}

type albumDetail struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Artist    string  `json:"artist,omitempty"`
	ArtistID  string  `json:"artistId,omitempty"`
	CoverArt  string  `json:"coverArt,omitempty"`
	SongCount int     `json:"songCount,omitempty"`
	Duration  int     `json:"duration,omitempty"`
	Year      int     `json:"year,omitempty"`
	Genre     string  `json:"genre,omitempty"`
	Song      []child `json:"song,omitempty"`
}

type directory struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Parent string  `json:"parent,omitempty"`
	Child  []child `json:"child,omitempty"`
}

type albumList2 struct {
	Album []child `json:"album"`
}

type child struct {
	ID          string `json:"id"`
	Parent      string `json:"parent,omitempty"`
	Title       string `json:"title"`
	IsDir       bool   `json:"isDir"`
	Album       string `json:"album,omitempty"`
	Artist      string `json:"artist,omitempty"`
	ArtistID    string `json:"artistId,omitempty"`
	Track       int    `json:"track,omitempty"`
	Year        int    `json:"year,omitempty"`
	Genre       string `json:"genre,omitempty"`
	CoverArt    string `json:"coverArt,omitempty"`
	Duration    int    `json:"duration,omitempty"`
	BitRate     int    `json:"bitRate,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Path        string `json:"path,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Suffix      string `json:"suffix,omitempty"`
}

type song struct {
	ID          string `json:"id"`
	Parent      string `json:"parent,omitempty"`
	Title       string `json:"title"`
	Album       string `json:"album,omitempty"`
	Artist      string `json:"artist,omitempty"`
	ArtistID    string `json:"artistId,omitempty"`
	Track       int    `json:"track,omitempty"`
	Year        int    `json:"year,omitempty"`
	Genre       string `json:"genre,omitempty"`
	CoverArt    string `json:"coverArt,omitempty"`
	Duration    int    `json:"duration,omitempty"`
	BitRate     int    `json:"bitRate,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Path        string `json:"path,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Suffix      string `json:"suffix,omitempty"`
}

type searchResult2 struct {
	Artist []artist `json:"artist,omitempty"`
	Album  []child  `json:"album,omitempty"`
	Song   []child  `json:"song,omitempty"`
}

type searchResult3 struct {
	Artist []artist `json:"artist,omitempty"`
	Album  []child  `json:"album,omitempty"`
	Song   []child  `json:"song,omitempty"`
}

type playlists struct {
	Playlist []playlistSummary `json:"playlist"`
}

type playlistSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
	Public    bool   `json:"public"`
	SongCount int    `json:"songCount,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	Created   string `json:"created,omitempty"`
	Changed   string `json:"changed,omitempty"`
}

type playlist struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Owner     string  `json:"owner,omitempty"`
	Public    bool    `json:"public"`
	SongCount int     `json:"songCount,omitempty"`
	Duration  int     `json:"duration,omitempty"`
	Entry     []child `json:"entry,omitempty"`
}

type openSubsonicExtensions struct {
	OpenSubsonicExtension []openSubsonicExtension `json:"openSubsonicExtension"`
}

type openSubsonicExtension struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
}

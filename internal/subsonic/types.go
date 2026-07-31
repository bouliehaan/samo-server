package subsonic

import "time"

// Subsonic DTOs. These mirror the published protocol's element names exactly —
// clients parse by key, so a rename here breaks them silently. Everything is a
// projection of catalog types; none of it is persisted.

type license struct {
	Valid bool `json:"valid"`
}

type musicFolders struct {
	MusicFolder []musicFolder `json:"musicFolder"`
}

type musicFolder struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// indexes / index back getIndexes, the alphabetical artist browser older
// clients use as their root view.
type indexes struct {
	LastModified    int64        `json:"lastModified"`
	IgnoredArticles string       `json:"ignoredArticles"`
	Index           []indexEntry `json:"index"`
}

type indexEntry struct {
	Name   string       `json:"name"`
	Artist []artistItem `json:"artist"`
}

type artistItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CoverArt   string `json:"coverArt,omitempty"`
	AlbumCount int    `json:"albumCount,omitempty"`
}

type artistsRoot struct {
	IgnoredArticles string        `json:"ignoredArticles"`
	Index           []artistIndex `json:"index"`
}

type artistIndex struct {
	Name   string       `json:"name"`
	Artist []artistItem `json:"artist"`
}

type artistDetail struct {
	artistItem
	Album []albumItem `json:"album"`
}

type albumItem struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Artist    string     `json:"artist,omitempty"`
	ArtistID  string     `json:"artistId,omitempty"`
	CoverArt  string     `json:"coverArt,omitempty"`
	SongCount int        `json:"songCount"`
	Duration  int        `json:"duration"`
	Created   *time.Time `json:"created,omitempty"`
	Year      int        `json:"year,omitempty"`
	Genre     string     `json:"genre,omitempty"`
}

type albumDetail struct {
	albumItem
	Song []child `json:"song"`
}

// child is Subsonic's universal "a song or a directory" element. Every
// track-shaped response uses it.
type child struct {
	ID          string     `json:"id"`
	Parent      string     `json:"parent,omitempty"`
	IsDir       bool       `json:"isDir"`
	Title       string     `json:"title"`
	Album       string     `json:"album,omitempty"`
	Artist      string     `json:"artist,omitempty"`
	Track       int        `json:"track,omitempty"`
	Year        int        `json:"year,omitempty"`
	Genre       string     `json:"genre,omitempty"`
	CoverArt    string     `json:"coverArt,omitempty"`
	Size        int64      `json:"size,omitempty"`
	ContentType string     `json:"contentType,omitempty"`
	Suffix      string     `json:"suffix,omitempty"`
	Duration    int        `json:"duration,omitempty"`
	BitRate     int        `json:"bitRate,omitempty"`
	Path        string     `json:"path,omitempty"`
	DiscNumber  int        `json:"discNumber,omitempty"`
	Created     *time.Time `json:"created,omitempty"`
	AlbumID     string     `json:"albumId,omitempty"`
	ArtistID    string     `json:"artistId,omitempty"`
	Type        string     `json:"type,omitempty"`
	Starred     *time.Time `json:"starred,omitempty"`
	PlayCount   int        `json:"playCount,omitempty"`
}

type directory struct {
	ID     string  `json:"id"`
	Parent string  `json:"parent,omitempty"`
	Name   string  `json:"name"`
	Child  []child `json:"child"`
}

type albumList struct {
	Album []child `json:"album"`
}

type albumList2 struct {
	Album []albumItem `json:"album"`
}

type songs struct {
	Song []child `json:"song"`
}

type starred struct {
	Artist []artistItem `json:"artist"`
	Album  []child      `json:"album"`
	Song   []child      `json:"song"`
}

type starred2 struct {
	Artist []artistItem `json:"artist"`
	Album  []albumItem  `json:"album"`
	Song   []child      `json:"song"`
}

type searchResult2 struct {
	Artist []artistItem `json:"artist"`
	Album  []child      `json:"album"`
	Song   []child      `json:"song"`
}

type searchResult3 struct {
	Artist []artistItem `json:"artist"`
	Album  []albumItem  `json:"album"`
	Song   []child      `json:"song"`
}

type playlists struct {
	Playlist []playlistItem `json:"playlist"`
}

type playlistItem struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Comment   string     `json:"comment,omitempty"`
	Owner     string     `json:"owner,omitempty"`
	Public    bool       `json:"public"`
	SongCount int        `json:"songCount"`
	Duration  int        `json:"duration"`
	Created   *time.Time `json:"created,omitempty"`
	CoverArt  string     `json:"coverArt,omitempty"`
}

type playlistDetail struct {
	playlistItem
	Entry []child `json:"entry"`
}

type genres struct {
	Genre []genreItem `json:"genre"`
}

type genreItem struct {
	Value      string `json:"value"`
	SongCount  int    `json:"songCount"`
	AlbumCount int    `json:"albumCount"`
}

type scanStatus struct {
	Scanning bool `json:"scanning"`
	Count    int  `json:"count"`
}

type nowPlaying struct {
	Entry []child `json:"entry"`
}

type userResponse struct {
	Username            string `json:"username"`
	ScrobblingEnabled   bool   `json:"scrobblingEnabled"`
	AdminRole           bool   `json:"adminRole"`
	SettingsRole        bool   `json:"settingsRole"`
	DownloadRole        bool   `json:"downloadRole"`
	UploadRole          bool   `json:"uploadRole"`
	PlaylistRole        bool   `json:"playlistRole"`
	CoverArtRole        bool   `json:"coverArtRole"`
	CommentRole         bool   `json:"commentRole"`
	PodcastRole         bool   `json:"podcastRole"`
	StreamRole          bool   `json:"streamRole"`
	JukeboxRole         bool   `json:"jukeboxRole"`
	ShareRole           bool   `json:"shareRole"`
	ScanRole            bool   `json:"scanRole"`
	VideoConversionRole bool   `json:"videoConversionRole"`
}

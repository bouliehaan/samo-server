package subsonic_test

import "github.com/bouliehaan/samo-server/internal/catalog"

// stubCatalog satisfies the reader interface with empty results. The auth tests
// exercise the middleware, not the projection, so the catalog is deliberately
// inert — a failure here is an auth failure and nothing else.
type stubCatalog struct{}

func (stubCatalog) ListMusicArtists(catalog.PageRequest) catalog.Page[catalog.MusicArtist] {
	return catalog.Page[catalog.MusicArtist]{}
}
func (stubCatalog) ListMusicAlbums(catalog.PageRequest) catalog.Page[catalog.MusicAlbum] {
	return catalog.Page[catalog.MusicAlbum]{}
}
func (stubCatalog) ListMusicTracks(catalog.PageRequest) catalog.Page[catalog.MusicTrack] {
	return catalog.Page[catalog.MusicTrack]{}
}
func (stubCatalog) ListGenres(catalog.PageRequest) catalog.Page[catalog.GenreSummary] {
	return catalog.Page[catalog.GenreSummary]{}
}
func (stubCatalog) MusicArtist(string) (catalog.MusicArtist, error) {
	return catalog.MusicArtist{}, catalog.ErrNotFound
}
func (stubCatalog) MusicAlbum(string) (catalog.MusicAlbum, error) {
	return catalog.MusicAlbum{}, catalog.ErrNotFound
}
func (stubCatalog) MusicTrack(string) (catalog.MusicTrack, error) {
	return catalog.MusicTrack{}, catalog.ErrNotFound
}
func (stubCatalog) MusicAlbumsForArtist(string) []catalog.MusicAlbum   { return nil }
func (stubCatalog) MusicTracksForAlbum(string) []catalog.MusicTrack    { return nil }
func (stubCatalog) MusicTracksForPlaylist(string) []catalog.MusicTrack { return nil }
func (stubCatalog) ListMusicPlaylistsForUser(string, catalog.PageRequest) catalog.Page[catalog.MusicPlaylist] {
	return catalog.Page[catalog.MusicPlaylist]{}
}
func (stubCatalog) MusicPlaylistForUser(string, string) (catalog.MusicPlaylist, error) {
	return catalog.MusicPlaylist{}, catalog.ErrNotFound
}

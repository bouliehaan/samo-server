package search

import (
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type musicIndex struct {
	artists   []catalog.MusicArtist
	albums    []catalog.MusicAlbum
	tracks    []catalog.MusicTrack
	playlists []catalog.MusicPlaylist
}

func buildMusicIndex(seed catalog.Seed) musicIndex {
	albums := append([]catalog.MusicAlbum(nil), seed.MusicAlbums...)
	tracks := append([]catalog.MusicTrack(nil), seed.MusicTracks...)
	// Match catalog.Service.applySeed: search must see the same aggregated
	// maxBitDepth/maxSampleRate/hiRes fields list endpoints expose.
	catalog.EnrichAlbumAudioQuality(albums, tracks)
	return musicIndex{
		artists:   append([]catalog.MusicArtist(nil), seed.MusicArtists...),
		albums:    albums,
		tracks:    tracks,
		playlists: append([]catalog.MusicPlaylist(nil), seed.MusicPlaylists...),
	}
}

func (idx musicIndex) search(query MusicQuery, overlay PlaybackOverlay) catalog.MusicSearchResults {
	page := catalog.NormalizePage(query.Page)
	results := catalog.MusicSearchResults{Limit: page.Limit, Offset: page.Offset}

	artistMatches := filterMusicArtists(idx.artists, query, overlay)
	albumMatches := filterMusicAlbums(idx.albums, query, overlay)
	trackMatches := filterMusicTracks(idx.tracks, query, overlay)
	playlistMatches := filterMusicPlaylists(idx.playlists, query, overlay)

	sortMusicArtists(artistMatches, query)
	sortMusicAlbums(albumMatches, query)
	sortMusicTracks(trackMatches, query)
	sortMusicPlaylists(playlistMatches, query)

	results.Artists = catalog.Paginate(artistMatches, page).Items
	results.Albums = catalog.Paginate(albumMatches, page).Items
	results.Tracks = catalog.Paginate(trackMatches, page).Items
	results.Playlists = catalog.Paginate(playlistMatches, page).Items
	results.Total = len(artistMatches) + len(albumMatches) + len(trackMatches) + len(playlistMatches)
	return results
}

func filterMusicArtists(items []catalog.MusicArtist, query MusicQuery, overlay PlaybackOverlay) []catalog.MusicArtist {
	matches := make([]catalog.MusicArtist, 0)
	for _, item := range items {
		item.Playback = overlayArtists(overlay, item.ID, item.Playback)
		if !musicEntityMatches(query, item.Genres, 0, item.AddedAt, item.Playback, artistSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterMusicAlbums(items []catalog.MusicAlbum, query MusicQuery, overlay PlaybackOverlay) []catalog.MusicAlbum {
	matches := make([]catalog.MusicAlbum, 0)
	for _, item := range items {
		if item.TrackCount <= 0 {
			continue
		}
		item.Playback = overlayAlbums(overlay, item.ID, item.Playback)
		if !musicEntityMatches(query, item.Genres, item.ReleaseYear, item.AddedAt, item.Playback, albumSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterMusicTracks(items []catalog.MusicTrack, query MusicQuery, overlay PlaybackOverlay) []catalog.MusicTrack {
	matches := make([]catalog.MusicTrack, 0)
	for _, item := range items {
		item.Playback = overlayTracks(overlay, item.ID, item.Playback)
		if !musicEntityMatches(query, item.Genres, item.ReleaseYear, item.AddedAt, item.Playback, trackSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterMusicPlaylists(items []catalog.MusicPlaylist, query MusicQuery, overlay PlaybackOverlay) []catalog.MusicPlaylist {
	matches := make([]catalog.MusicPlaylist, 0)
	for _, item := range items {
		if query.FilterPlaylistsByUser && !catalog.PlaylistVisibleToUser(item, query.PlaylistUserID) {
			continue
		}
		item.Playback = overlayPlaylists(overlay, item.ID, item.Playback)
		if !musicEntityMatches(query, nil, 0, item.CreatedAt, item.Playback, playlistSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func musicEntityMatches(
	query MusicQuery,
	genres []string,
	releaseYear int,
	addedAt *time.Time,
	playback catalog.PlaybackState,
	searchText string,
) bool {
	if query.LibraryID != "" {
		return false
	}
	if !genreMatches(genres, query.Genre) {
		return false
	}
	if query.Year > 0 && releaseYear != query.Year {
		return false
	}
	if !playbackMatches(query, playback, addedAt) {
		return false
	}
	return MatchText(searchText, query.Text)
}

func playbackMatches(query MusicQuery, playback catalog.PlaybackState, addedAt *time.Time) bool {
	if query.Favorite != nil && playback.Favorite != *query.Favorite {
		return false
	}
	if query.Starred != nil && playback.Starred != *query.Starred {
		return false
	}
	if query.Completed != nil && playback.Completed != *query.Completed {
		return false
	}
	if query.MinRating > 0 && playback.Rating < query.MinRating {
		return false
	}
	if query.RecentlyPlayed && playback.LastPlayedAt == nil {
		return false
	}
	if query.RecentlyAdded && addedAt == nil {
		return false
	}
	return true
}

func artistSearchText(item catalog.MusicArtist) string {
	return joinFields(item.Name, item.SortName, item.Disambiguation, item.Country, strings.Join(item.Genres, " "))
}

func albumSearchText(item catalog.MusicAlbum) string {
	return joinFields(
		item.Title, item.SortTitle, item.DisplayArtist,
		strings.Join(item.ArtistNames, " "), strings.Join(item.AlbumArtistNames, " "),
		strings.Join(item.Genres, " "), strings.Join(item.Tags, " "),
	)
}

func trackSearchText(item catalog.MusicTrack) string {
	return joinFields(
		item.Title, item.SortTitle, item.Subtitle, item.AlbumTitle, item.DisplayArtist,
		strings.Join(item.ArtistNames, " "), strings.Join(item.AlbumArtistNames, " "),
		strings.Join(item.Genres, " "), strings.Join(item.Tags, " "),
	)
}

func playlistSearchText(item catalog.MusicPlaylist) string {
	return joinFields(item.Name, item.Description)
}

func sortMusicArtists(items []catalog.MusicArtist, query MusicQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Name, items[i].AddedAt, items[i].Playback, artistSearchText(items[i]),
			items[j].Name, items[j].AddedAt, items[j].Playback, artistSearchText(items[j]))
	})
}

func sortMusicAlbums(items []catalog.MusicAlbum, query MusicQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Title, items[i].AddedAt, items[i].Playback, albumSearchText(items[i]),
			items[j].Title, items[j].AddedAt, items[j].Playback, albumSearchText(items[j]))
	})
}

func sortMusicTracks(items []catalog.MusicTrack, query MusicQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Title, items[i].AddedAt, items[i].Playback, trackSearchText(items[i]),
			items[j].Title, items[j].AddedAt, items[j].Playback, trackSearchText(items[j]))
	})
}

func sortMusicPlaylists(items []catalog.MusicPlaylist, query MusicQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Name, items[i].CreatedAt, items[i].Playback, playlistSearchText(items[i]),
			items[j].Name, items[j].CreatedAt, items[j].Playback, playlistSearchText(items[j]))
	})
}

func musicLess(query MusicQuery, titleI string, addedI *time.Time, playbackI catalog.PlaybackState, textI string,
	titleJ string, addedJ *time.Time, playbackJ catalog.PlaybackState, textJ string) bool {
	switch query.Sort {
	case SortTitle:
		return strings.ToLower(titleI) < strings.ToLower(titleJ)
	case SortAdded:
		return timeAfter(addedI, addedJ)
	case SortPlayed:
		return timeAfter(playbackI.LastPlayedAt, playbackJ.LastPlayedAt)
	default:
		return ScoreText(textI, query.Text) > ScoreText(textJ, query.Text)
	}
}

func overlayArtists(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Artists[id]; ok {
		return state
	}
	return current
}

func overlayAlbums(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Albums[id]; ok {
		return state
	}
	return current
}

func overlayTracks(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Tracks[id]; ok {
		return state
	}
	return current
}

func overlayPlaylists(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Playlists[id]; ok {
		return state
	}
	return current
}

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
	// The explo silo: explo content never surfaces in search. It remains
	// reachable through the Explo tab and the Explore playlist, which
	// resolve by ID, not through this index.
	return musicIndex{
		artists:   catalog.WithoutExploArtists(append([]catalog.MusicArtist(nil), seed.MusicArtists...)),
		albums:    catalog.WithoutExploAlbums(albums),
		tracks:    catalog.WithoutExploTracks(tracks),
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

// Each record's search text is split into the title the ranker scores and
// the secondary text that is only its floor; the filter sees the two joined.

func artistSearchFields(item catalog.MusicArtist) (title, secondary string) {
	return item.Name, joinFields(item.SortName, item.Disambiguation, item.Country, strings.Join(item.Genres, " "))
}

func albumSearchFields(item catalog.MusicAlbum) (title, secondary string) {
	return item.Title, joinFields(
		item.SortTitle, item.DisplayArtist,
		strings.Join(item.ArtistNames, " "), strings.Join(item.AlbumArtistNames, " "),
		strings.Join(item.Genres, " "), strings.Join(item.Tags, " "),
	)
}

func trackSearchFields(item catalog.MusicTrack) (title, secondary string) {
	return item.Title, joinFields(
		item.SortTitle, item.Subtitle, item.AlbumTitle, item.DisplayArtist,
		strings.Join(item.ArtistNames, " "), strings.Join(item.AlbumArtistNames, " "),
		strings.Join(item.Genres, " "), strings.Join(item.Tags, " "),
	)
}

func playlistSearchFields(item catalog.MusicPlaylist) (title, secondary string) {
	return item.Name, joinFields(item.Description)
}

func artistSearchText(item catalog.MusicArtist) string {
	return joinFields(artistSearchFields(item))
}

func albumSearchText(item catalog.MusicAlbum) string {
	return joinFields(albumSearchFields(item))
}

func trackSearchText(item catalog.MusicTrack) string {
	return joinFields(trackSearchFields(item))
}

func playlistSearchText(item catalog.MusicPlaylist) string {
	return joinFields(playlistSearchFields(item))
}

func sortMusicArtists(items []catalog.MusicArtist, query MusicQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.MusicArtist) rankFields {
			title, secondary := artistSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Playback.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Name, items[i].AddedAt, items[i].Playback,
			items[j].Name, items[j].AddedAt, items[j].Playback)
	})
}

func sortMusicAlbums(items []catalog.MusicAlbum, query MusicQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.MusicAlbum) rankFields {
			title, secondary := albumSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Playback.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Title, items[i].AddedAt, items[i].Playback,
			items[j].Title, items[j].AddedAt, items[j].Playback)
	})
}

func sortMusicTracks(items []catalog.MusicTrack, query MusicQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.MusicTrack) rankFields {
			title, secondary := trackSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Playback.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Title, items[i].AddedAt, items[i].Playback,
			items[j].Title, items[j].AddedAt, items[j].Playback)
	})
}

func sortMusicPlaylists(items []catalog.MusicPlaylist, query MusicQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.MusicPlaylist) rankFields {
			title, secondary := playlistSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Playback.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return musicLess(query, items[i].Name, items[i].CreatedAt, items[i].Playback,
			items[j].Name, items[j].CreatedAt, items[j].Playback)
	})
}

// isRelevanceSort mirrors the old comparator's default branch: anything that
// is not one of the explicit orders ranks by relevance.
func isRelevanceSort(sortMode string) bool {
	switch sortMode {
	case SortTitle, SortAdded, SortPlayed:
		return false
	default:
		return true
	}
}

// musicLess orders the explicit sort modes; relevance has its own path.
func musicLess(query MusicQuery, titleI string, addedI *time.Time, playbackI catalog.PlaybackState,
	titleJ string, addedJ *time.Time, playbackJ catalog.PlaybackState) bool {
	switch query.Sort {
	case SortAdded:
		return timeAfter(addedI, addedJ)
	case SortPlayed:
		return timeAfter(playbackI.LastPlayedAt, playbackJ.LastPlayedAt)
	default:
		return strings.ToLower(titleI) < strings.ToLower(titleJ)
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

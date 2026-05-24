package catalog

import (
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrInvalidBrowseView = errors.New("invalid music browse view")

type MusicBrowseView string

const (
	MusicBrowseFavorites      MusicBrowseView = "favorites"
	MusicBrowseStarred        MusicBrowseView = "starred"
	MusicBrowseRecentlyPlayed MusicBrowseView = "recently-played"
	MusicBrowseRecentlyAdded  MusicBrowseView = "recently-added"
)

func ParseMusicBrowseView(raw string) (MusicBrowseView, error) {
	switch MusicBrowseView(strings.TrimSpace(raw)) {
	case MusicBrowseFavorites, MusicBrowseStarred, MusicBrowseRecentlyPlayed, MusicBrowseRecentlyAdded:
		return MusicBrowseView(strings.TrimSpace(raw)), nil
	default:
		return "", ErrInvalidBrowseView
	}
}

type MusicBrowseResults struct {
	View      MusicBrowseView `json:"view"`
	Artists   []MusicArtist   `json:"artists"`
	Albums    []MusicAlbum    `json:"albums"`
	Tracks    []MusicTrack    `json:"tracks"`
	Playlists []MusicPlaylist `json:"playlists"`
	Total     int             `json:"total"`
	Limit     int             `json:"limit"`
	Offset    int             `json:"offset"`
}

type musicBrowseSnapshot struct {
	artists   []MusicArtist
	albums    []MusicAlbum
	tracks    []MusicTrack
	playlists []MusicPlaylist
}

func (s *Service) MusicBrowse(
	trackStates map[string]PlaybackState,
	albumStates map[string]PlaybackState,
	artistStates map[string]PlaybackState,
	playlistStates map[string]PlaybackState,
	view MusicBrowseView,
	page PageRequest,
) MusicBrowseResults {
	return s.musicBrowse(trackStates, albumStates, artistStates, playlistStates, view, page, "", false)
}

func (s *Service) MusicBrowseForUser(
	trackStates map[string]PlaybackState,
	albumStates map[string]PlaybackState,
	artistStates map[string]PlaybackState,
	playlistStates map[string]PlaybackState,
	view MusicBrowseView,
	page PageRequest,
	userID string,
) MusicBrowseResults {
	return s.musicBrowse(trackStates, albumStates, artistStates, playlistStates, view, page, userID, true)
}

func (s *Service) musicBrowse(
	trackStates map[string]PlaybackState,
	albumStates map[string]PlaybackState,
	artistStates map[string]PlaybackState,
	playlistStates map[string]PlaybackState,
	view MusicBrowseView,
	page PageRequest,
	playlistUserID string,
	filterPrivatePlaylists bool,
) MusicBrowseResults {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := overlayMusicBrowsePlayback(s.musicArtists, s.musicAlbums, s.musicTracks, s.musicPlaylists, trackStates, albumStates, artistStates, playlistStates)
	if filterPrivatePlaylists {
		snapshot.playlists = visibleMusicBrowsePlaylists(snapshot.playlists, playlistUserID)
	}
	var matches musicBrowseSnapshot
	switch view {
	case MusicBrowseFavorites:
		matches = filterMusicBrowse(snapshot, func(playback PlaybackState) bool { return playback.Favorite })
	case MusicBrowseStarred:
		matches = filterMusicBrowse(snapshot, func(playback PlaybackState) bool { return playback.Starred })
	case MusicBrowseRecentlyPlayed:
		matches = filterMusicBrowse(snapshot, func(playback PlaybackState) bool { return playback.LastPlayedAt != nil })
		sortMusicBrowseByLastPlayed(&matches)
	case MusicBrowseRecentlyAdded:
		matches = snapshot
		sortMusicBrowseByAddedAt(&matches)
	default:
		return MusicBrowseResults{View: view}
	}

	total := len(matches.artists) + len(matches.albums) + len(matches.tracks) + len(matches.playlists)
	page = normalizePage(page)
	paged := paginateMusicBrowse(matches, page)
	return MusicBrowseResults{
		View:      view,
		Artists:   paged.artists,
		Albums:    paged.albums,
		Tracks:    paged.tracks,
		Playlists: paged.playlists,
		Total:     total,
		Limit:     page.Limit,
		Offset:    page.Offset,
	}
}

func visibleMusicBrowsePlaylists(items []MusicPlaylist, userID string) []MusicPlaylist {
	out := make([]MusicPlaylist, 0, len(items))
	for _, item := range items {
		if PlaylistVisibleToUser(item, userID) {
			out = append(out, item)
		}
	}
	return out
}

func overlayMusicBrowsePlayback(
	artists []MusicArtist,
	albums []MusicAlbum,
	tracks []MusicTrack,
	playlists []MusicPlaylist,
	trackStates map[string]PlaybackState,
	albumStates map[string]PlaybackState,
	artistStates map[string]PlaybackState,
	playlistStates map[string]PlaybackState,
) musicBrowseSnapshot {
	out := musicBrowseSnapshot{
		artists:   append([]MusicArtist(nil), artists...),
		albums:    append([]MusicAlbum(nil), albums...),
		tracks:    append([]MusicTrack(nil), tracks...),
		playlists: append([]MusicPlaylist(nil), playlists...),
	}
	for index, artist := range out.artists {
		if state, ok := artistStates[artist.ID]; ok {
			out.artists[index].Playback = state
		}
	}
	for index, album := range out.albums {
		if state, ok := albumStates[album.ID]; ok {
			out.albums[index].Playback = state
		}
	}
	for index, track := range out.tracks {
		if state, ok := trackStates[track.ID]; ok {
			out.tracks[index].Playback = state
		}
	}
	for index, playlist := range out.playlists {
		if state, ok := playlistStates[playlist.ID]; ok {
			out.playlists[index].Playback = state
		}
	}
	return out
}

func filterMusicBrowse(snapshot musicBrowseSnapshot, match func(PlaybackState) bool) musicBrowseSnapshot {
	out := musicBrowseSnapshot{}
	for _, artist := range snapshot.artists {
		if match(artist.Playback) {
			out.artists = append(out.artists, artist)
		}
	}
	for _, album := range snapshot.albums {
		if match(album.Playback) {
			out.albums = append(out.albums, album)
		}
	}
	for _, track := range snapshot.tracks {
		if match(track.Playback) {
			out.tracks = append(out.tracks, track)
		}
	}
	for _, playlist := range snapshot.playlists {
		if match(playlist.Playback) {
			out.playlists = append(out.playlists, playlist)
		}
	}
	return out
}

func sortMusicBrowseByLastPlayed(snapshot *musicBrowseSnapshot) {
	sort.SliceStable(snapshot.artists, func(i, j int) bool {
		return playbackTimeAfter(snapshot.artists[i].Playback, snapshot.artists[j].Playback)
	})
	sort.SliceStable(snapshot.albums, func(i, j int) bool {
		return playbackTimeAfter(snapshot.albums[i].Playback, snapshot.albums[j].Playback)
	})
	sort.SliceStable(snapshot.tracks, func(i, j int) bool {
		return playbackTimeAfter(snapshot.tracks[i].Playback, snapshot.tracks[j].Playback)
	})
	sort.SliceStable(snapshot.playlists, func(i, j int) bool {
		return playbackTimeAfter(snapshot.playlists[i].Playback, snapshot.playlists[j].Playback)
	})
}

func playbackTimeAfter(left, right PlaybackState) bool {
	if left.LastPlayedAt == nil {
		return false
	}
	if right.LastPlayedAt == nil {
		return true
	}
	return left.LastPlayedAt.After(*right.LastPlayedAt)
}

func sortMusicBrowseByAddedAt(snapshot *musicBrowseSnapshot) {
	sort.SliceStable(snapshot.artists, func(i, j int) bool { return addedAtAfter(snapshot.artists[i].AddedAt, snapshot.artists[j].AddedAt) })
	sort.SliceStable(snapshot.albums, func(i, j int) bool { return addedAtAfter(snapshot.albums[i].AddedAt, snapshot.albums[j].AddedAt) })
	sort.SliceStable(snapshot.tracks, func(i, j int) bool { return addedAtAfter(snapshot.tracks[i].AddedAt, snapshot.tracks[j].AddedAt) })
	sort.SliceStable(snapshot.playlists, func(i, j int) bool {
		return addedAtAfter(snapshot.playlists[i].CreatedAt, snapshot.playlists[j].CreatedAt)
	})
}

func addedAtAfter(left, right *time.Time) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	return left.After(*right)
}

type browseEntry struct {
	kind     string
	artist   MusicArtist
	album    MusicAlbum
	track    MusicTrack
	playlist MusicPlaylist
}

func paginateMusicBrowse(snapshot musicBrowseSnapshot, page PageRequest) musicBrowseSnapshot {
	flat := make([]browseEntry, 0, len(snapshot.artists)+len(snapshot.albums)+len(snapshot.tracks)+len(snapshot.playlists))
	for _, artist := range snapshot.artists {
		flat = append(flat, browseEntry{kind: "artist", artist: artist})
	}
	for _, album := range snapshot.albums {
		flat = append(flat, browseEntry{kind: "album", album: album})
	}
	for _, track := range snapshot.tracks {
		flat = append(flat, browseEntry{kind: "track", track: track})
	}
	for _, playlist := range snapshot.playlists {
		flat = append(flat, browseEntry{kind: "playlist", playlist: playlist})
	}
	page = normalizePage(page)
	if page.Offset > len(flat) {
		return musicBrowseSnapshot{}
	}
	end := page.Offset + page.Limit
	if end > len(flat) {
		end = len(flat)
	}

	out := musicBrowseSnapshot{}
	for _, entry := range flat[page.Offset:end] {
		switch entry.kind {
		case "artist":
			out.artists = append(out.artists, entry.artist)
		case "album":
			out.albums = append(out.albums, entry.album)
		case "track":
			out.tracks = append(out.tracks, entry.track)
		case "playlist":
			out.playlists = append(out.playlists, entry.playlist)
		}
	}
	return out
}

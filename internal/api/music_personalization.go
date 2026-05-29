package api

import (
	"context"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

// applyMusicListPlaybackToOptions loads user playback for sorted list endpoints.
// Returns false after writing an error response.
func (s *Server) applyMusicListPlaybackToOptions(
	w http.ResponseWriter,
	r *http.Request,
	options *catalog.MusicListOptions,
) bool {
	principal, ok := s.currentUser(r)
	if !ok || s.playback == nil {
		return true
	}
	states, err := s.loadMusicBrowseStates(r, principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	options.Playback = catalog.MusicListPlaybackOverlay{
		TrackStates:  states.tracks,
		AlbumStates:  states.albums,
		ArtistStates: states.artists,
	}
	return true
}

func (s *Server) musicArtistWithUserPlayback(
	ctx context.Context,
	userID string,
	artist catalog.MusicArtist,
) (catalog.MusicArtist, error) {
	if userID == "" || s.playback == nil {
		return artist, nil
	}

	scopeTracks := s.catalog.MusicTracksForArtist(artist.ID)
	trackIDs := musicTrackIDs(scopeTracks)

	trackStates, err := s.playback.ListForUserByIDs(ctx, userID, playback.TargetMusicTrack, trackIDs)
	if err != nil {
		return catalog.MusicArtist{}, err
	}

	artistStates := map[string]catalog.PlaybackState{
		artist.ID: s.userPlayback(ctx, userID, playback.TargetMusicArtist, artist.ID),
	}
	items := []catalog.MusicArtist{artist}
	catalog.OverlayMusicArtists(items, artistStates, scopeTracks, trackStates)
	return items[0], nil
}

func (s *Server) musicAlbumWithUserPlayback(
	ctx context.Context,
	userID string,
	album catalog.MusicAlbum,
) (catalog.MusicAlbum, error) {
	if userID == "" || s.playback == nil {
		return album, nil
	}

	scopeTracks := s.catalog.MusicTracksForAlbum(album.ID)
	trackIDs := musicTrackIDs(scopeTracks)

	trackStates, err := s.playback.ListForUserByIDs(ctx, userID, playback.TargetMusicTrack, trackIDs)
	if err != nil {
		return catalog.MusicAlbum{}, err
	}

	albumStates := map[string]catalog.PlaybackState{
		album.ID: s.userPlayback(ctx, userID, playback.TargetMusicAlbum, album.ID),
	}
	items := []catalog.MusicAlbum{album}
	catalog.OverlayMusicAlbums(items, albumStates, scopeTracks, trackStates)
	return items[0], nil
}

func (s *Server) musicTracksWithUserPlayback(
	ctx context.Context,
	userID string,
	tracks []catalog.MusicTrack,
) ([]catalog.MusicTrack, error) {
	if userID == "" || s.playback == nil || len(tracks) == 0 {
		return tracks, nil
	}

	trackStates, err := s.playback.ListForUserByIDs(
		ctx,
		userID,
		playback.TargetMusicTrack,
		musicTrackIDs(tracks),
	)
	if err != nil {
		return nil, err
	}

	items := append([]catalog.MusicTrack(nil), tracks...)
	catalog.OverlayMusicTracks(items, trackStates)
	return items, nil
}

func (s *Server) musicAlbumsWithUserPlayback(
	ctx context.Context,
	userID string,
	artistID string,
	albums []catalog.MusicAlbum,
) ([]catalog.MusicAlbum, error) {
	if userID == "" || s.playback == nil || len(albums) == 0 {
		return albums, nil
	}

	scopeTracks := s.catalog.MusicTracksForArtist(artistID)
	trackIDs := musicTrackIDs(scopeTracks)
	albumIDs := make([]string, 0, len(albums))
	for _, album := range albums {
		albumIDs = append(albumIDs, album.ID)
	}

	trackStates, err := s.playback.ListForUserByIDs(ctx, userID, playback.TargetMusicTrack, trackIDs)
	if err != nil {
		return nil, err
	}
	albumStates, err := s.playback.ListForUserByIDs(ctx, userID, playback.TargetMusicAlbum, albumIDs)
	if err != nil {
		return nil, err
	}

	items := append([]catalog.MusicAlbum(nil), albums...)
	catalog.OverlayMusicAlbums(items, albumStates, scopeTracks, trackStates)
	return items, nil
}

func (s *Server) musicPlaylistWithUserPlayback(
	ctx context.Context,
	userID string,
	playlist catalog.MusicPlaylist,
) (catalog.MusicPlaylist, error) {
	if userID == "" || s.playback == nil {
		return playlist, nil
	}
	playlist.Playback = s.userPlayback(ctx, userID, playback.TargetMusicPlaylist, playlist.ID)
	return playlist, nil
}

func musicTrackIDs(tracks []catalog.MusicTrack) []string {
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		if track.ID != "" {
			ids = append(ids, track.ID)
		}
	}
	return ids
}

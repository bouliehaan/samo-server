package subsonic

import (
	"context"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

type musicPlaybackMaps struct {
	artists   map[string]catalog.PlaybackState
	albums    map[string]catalog.PlaybackState
	tracks    map[string]catalog.PlaybackState
	playlists map[string]catalog.PlaybackState
}

func (s *Server) loadMusicPlayback(ctx context.Context, userID string) (musicPlaybackMaps, error) {
	if s.playback == nil {
		return musicPlaybackMaps{}, nil
	}
	trackStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicTrack)
	if err != nil {
		return musicPlaybackMaps{}, err
	}
	albumStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicAlbum)
	if err != nil {
		return musicPlaybackMaps{}, err
	}
	artistStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicArtist)
	if err != nil {
		return musicPlaybackMaps{}, err
	}
	playlistStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicPlaylist)
	if err != nil {
		return musicPlaybackMaps{}, err
	}
	return musicPlaybackMaps{
		artists:   artistStates,
		albums:    albumStates,
		tracks:    trackStates,
		playlists: playlistStates,
	}, nil
}

func overlayArtist(item catalog.MusicArtist, states musicPlaybackMaps) catalog.MusicArtist {
	if state, ok := states.artists[item.ID]; ok {
		item.Playback = state
	}
	return item
}

func overlayAlbum(item catalog.MusicAlbum, states musicPlaybackMaps) catalog.MusicAlbum {
	if state, ok := states.albums[item.ID]; ok {
		item.Playback = state
	}
	return item
}

func overlayTrack(item catalog.MusicTrack, states musicPlaybackMaps) catalog.MusicTrack {
	if state, ok := states.tracks[item.ID]; ok {
		item.Playback = state
	}
	return item
}

func subsonicStarredAt(playback catalog.PlaybackState) int64 {
	if !playback.Starred {
		return 0
	}
	if playback.LastPlayedAt != nil {
		return playback.LastPlayedAt.UnixMilli()
	}
	return time.Now().UTC().UnixMilli()
}

func applyPlaybackChild(item child, playback catalog.PlaybackState) child {
	if starred := subsonicStarredAt(playback); starred > 0 {
		item.Starred = starred
	}
	if playback.Rating > 0 {
		item.UserRating = playback.Rating
	}
	return item
}

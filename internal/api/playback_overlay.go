package api

import (
	"context"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) userPlayback(ctx context.Context, userID string, kind playback.TargetKind, id string) catalog.PlaybackState {
	if s.playback == nil || userID == "" {
		return catalog.PlaybackState{}
	}
	state, err := s.playback.Get(ctx, userID, kind, id)
	if err != nil {
		return catalog.PlaybackState{}
	}
	return state
}

func (s *Server) musicTrackWithUserPlayback(ctx context.Context, userID, trackID string) (catalog.MusicTrack, error) {
	track, err := s.catalog.MusicTrack(trackID)
	if err != nil {
		return catalog.MusicTrack{}, err
	}
	if userID == "" {
		return track, nil
	}
	track.Playback = s.userPlayback(ctx, userID, playback.TargetMusicTrack, track.ID)
	return track, nil
}

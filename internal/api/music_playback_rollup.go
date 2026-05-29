package api

import (
	"context"

	"github.com/bouliehaan/samo-server/internal/playback"
)

// rollupMusicTrackPlayback mirrors track play events onto parent album and
// artist playback so browse/list sorts reflect listening history.
func (s *Server) rollupMusicTrackPlayback(
	ctx context.Context,
	userID string,
	trackID string,
	patch playback.PatchInput,
) {
	if !patch.IncrementPlayCount && !patch.TouchLastPlayedAt {
		return
	}
	track, err := s.catalog.MusicTrack(trackID)
	if err != nil {
		return
	}

	parentPatch := playback.PatchInput{
		IncrementPlayCount: patch.IncrementPlayCount,
		TouchLastPlayedAt:  patch.TouchLastPlayedAt,
	}
	if track.AlbumID != "" {
		_, _ = s.playbackService().Patch(ctx, userID, playback.TargetMusicAlbum, track.AlbumID, parentPatch)
	}

	seen := make(map[string]struct{}, len(track.ArtistIDs)+len(track.AlbumArtistIDs))
	for _, artistID := range append(track.ArtistIDs, track.AlbumArtistIDs...) {
		if artistID == "" {
			continue
		}
		if _, ok := seen[artistID]; ok {
			continue
		}
		seen[artistID] = struct{}{}
		_, _ = s.playbackService().Patch(ctx, userID, playback.TargetMusicArtist, artistID, parentPatch)
	}
}

package lastfm

// Loving a track is Last.fm's own idea, not part of measuring a listen, so it
// stays here rather than in the shared engine.

import (
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func loveStateChanged(before, after catalog.PlaybackState, patch *playback.PatchInput) (loved bool, unloved bool) {
	beforeLoved := before.Favorite || before.Starred
	afterLoved := after.Favorite || after.Starred
	if patch != nil {
		if patch.Favorite != nil {
			afterLoved = *patch.Favorite || after.Starred
		}
		if patch.Starred != nil {
			afterLoved = after.Favorite || *patch.Starred
		}
	}
	if !beforeLoved && afterLoved {
		return true, false
	}
	if beforeLoved && !afterLoved {
		return false, true
	}
	return false, false
}

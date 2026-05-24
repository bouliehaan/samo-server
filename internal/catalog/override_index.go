package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Override target_kind constants. These string values are persisted in
// metadata_overrides.target_kind and on the wire in the metadata-apply API,
// so they MUST stay stable. Migration 016 rewrites old shelf-* values to
// the audiobook / podcast / podcast-episode forms below.
const (
	OverrideKindMusicArtist    = "music-artist"
	OverrideKindMusicAlbum     = "music-album"
	OverrideKindMusicTrack     = "music-track"
	OverrideKindMusicPlaylist  = "music-playlist"
	OverrideKindAudiobook      = "audiobook"
	OverrideKindPodcast        = "podcast"
	OverrideKindPodcastEpisode = "podcast-episode"
	OverrideKindPodcastFeed    = "podcast-feed"
)

// OverrideIndex caches metadata override patches for write-time guarding.
type OverrideIndex struct {
	patches                map[MetadataOverrideKey]MetadataOverridePatch
	podcastFeedByPodcastID map[string]MetadataOverridePatch
}

func LoadOverrideIndex(ctx context.Context, db *sql.DB) (*OverrideIndex, error) {
	patches, err := LoadMetadataOverrides(ctx, db)
	if err != nil {
		return nil, err
	}
	feedPodcastIDs, err := LoadPodcastFeedPodcastIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	idx := &OverrideIndex{
		patches:                patches,
		podcastFeedByPodcastID: map[string]MetadataOverridePatch{},
	}
	for feedID, podcastID := range feedPodcastIDs {
		if patch, ok := patches[MetadataOverrideKey{TargetKind: OverrideKindPodcastFeed, TargetID: feedID}]; ok && len(patch) > 0 {
			idx.podcastFeedByPodcastID[podcastID] = patch
		}
	}
	return idx, nil
}

func (idx *OverrideIndex) IsEmpty() bool {
	return idx == nil || len(idx.patches) == 0
}

func (idx *OverrideIndex) Patch(kind, targetID string) MetadataOverridePatch {
	if idx == nil {
		return nil
	}
	return idx.patches[MetadataOverrideKey{TargetKind: kind, TargetID: targetID}]
}

func (idx *OverrideIndex) HasField(kind, targetID, field string) bool {
	patch := idx.Patch(kind, targetID)
	_, ok := patch[field]
	return ok
}

func (idx *OverrideIndex) FeedPatchForPodcast(podcastID string) MetadataOverridePatch {
	if idx == nil {
		return nil
	}
	return idx.podcastFeedByPodcastID[podcastID]
}

// CombinedPodcastPatch merges a podcast-level override with the patch from
// its podcast-feed (RSS) override row. Feed overrides win on conflict —
// they're typically the user's "fix the RSS title once" surface, while
// per-show overrides are the rarer manual tweaks.
func (idx *OverrideIndex) CombinedPodcastPatch(podcastID string) MetadataOverridePatch {
	patch := idx.Patch(OverrideKindPodcast, podcastID)
	feedPatch := idx.FeedPatchForPodcast(podcastID)
	if len(feedPatch) == 0 {
		return patch
	}
	if len(patch) == 0 {
		return feedPatch
	}
	merged := MetadataOverridePatch{}
	for field, value := range patch {
		merged[field] = append(json.RawMessage(nil), value...)
	}
	for field, value := range feedPatch {
		if field == "externalIds" {
			merged[field] = mergeOverrideExternalIDs(merged[field], value)
			continue
		}
		merged[field] = append(json.RawMessage(nil), value...)
	}
	return merged
}

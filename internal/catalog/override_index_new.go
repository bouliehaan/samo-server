package catalog

// NewOverrideIndex assembles an index from already-loaded patches. It exists so
// internal/catalogstore can construct one after reading the tables without the
// model exposing its internals, or the store having to know how feed patches
// are keyed to podcasts.
func NewOverrideIndex(
	patches map[MetadataOverrideKey]MetadataOverridePatch,
	feedPodcastIDs map[string]string,
) *OverrideIndex {
	idx := &OverrideIndex{
		patches:                patches,
		podcastFeedByPodcastID: map[string]MetadataOverridePatch{},
	}
	for feedID, podcastID := range feedPodcastIDs {
		key := MetadataOverrideKey{TargetKind: OverrideKindPodcastFeed, TargetID: feedID}
		if patch, ok := patches[key]; ok && len(patch) > 0 {
			idx.podcastFeedByPodcastID[podcastID] = patch
		}
	}
	return idx
}

package catalogstore

import (
	"context"
	"database/sql"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// LoadOverrideIndex reads the override tables and hands the rows to the model
// to assemble. The keying rule (which feed patch applies to which podcast) is
// domain logic and lives in catalog.NewOverrideIndex; this function owns only
// the two queries.
func LoadOverrideIndex(ctx context.Context, db *sql.DB) (*catalog.OverrideIndex, error) {
	patches, err := LoadMetadataOverrides(ctx, db)
	if err != nil {
		return nil, err
	}
	feedPodcastIDs, err := LoadPodcastFeedPodcastIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	return catalog.NewOverrideIndex(patches, feedPodcastIDs), nil
}

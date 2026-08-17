package explo

import (
	"github.com/bouliehaan/samo-server/internal/artwork"
)

// placeholderPNG renders the deterministic cover tile for an album with no
// fetchable art yet.
//
// It exists to honor the "no explo album is ever a blank tile" guarantee — the
// cover backfill keeps retrying real sources and replaces this the moment one
// of them has art.
//
// The rendering itself moved to internal/artwork when channels needed the same
// tile. Same function, same bytes for the same album id, so everything already
// in the cover store stays valid.
func placeholderPNG(albumID string) []byte {
	return artwork.GradientTile(albumID)
}

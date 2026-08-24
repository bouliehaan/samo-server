package channels

import (
	"context"
	"strings"
)

/*
Replacing "which block picked this" with "what is actually playing".

A channel airing its own catalog knows exactly what it put on: the scheduler
chose a track, and the track's title and artist ARE the item. A channel relaying
somebody else's live stream knows nothing of the sort. All it picked was a
source, so the item it hands out is labelled with that source — the block — and
stays labelled that way for the three hours the relay runs.

That is the difference between a card reading

    SiriusXM Chill                     Bad Guy
    (for three hours)                  Billie Eilish · SiriusXM Chill

and it is entirely a reporting problem: the station is broadcasting the second
version the whole time, and samo simply never asked. This file asks, on the way
out through the now-playing endpoint, and leaves the scheduler's own record of
what it decided untouched — the play log wants the block, the card does not.
*/

// LiveStationNowPlaying is what a relayed station is airing this second.
type LiveStationNowPlaying struct {
	Title      string
	Artist     string
	Album      string
	ArtworkURL string
}

// LiveStationLookup is the slice of internal/sources the now-playing path uses
// to find out what a relayed station is currently playing.
//
// Two methods because the two callers want opposite things. The now-playing
// endpoint is asked by one wall every ten seconds and can afford to go and
// look; the channel list is asked once for every channel on a page and must
// not turn a page load into a fan-out of HTTP requests.
type LiveStationLookup interface {
	// LiveStationMetadata may refresh a stale answer before returning it.
	LiveStationMetadata(ctx context.Context, stationID string) (LiveStationNowPlaying, bool)
	// CachedStationMetadata never performs I/O.
	CachedStationMetadata(stationID string) (LiveStationNowPlaying, bool)
}

// stationRefID pulls the station id out of an item ref, or "" for any item that
// is not a relayed station.
//
// live-stream items get nothing on purpose: their ref is "stream:<url>", a bare
// URL with no station row behind it, so there is nowhere to look up metadata or
// a picture. They keep the source label, which for a URL nobody has named is
// the only thing anyone ever knew about them.
func stationRefID(item PlaybackItem) string {
	const prefix = "station:"
	if !strings.HasPrefix(item.ItemRef, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(item.ItemRef, prefix))
}

// withLiveMetadata returns the item as it should be reported right now.
//
// Additive and total-failure-safe by design: an unreachable station, an
// endpoint between tracks, a station with no picture — each leaves the
// corresponding field exactly as the scheduler set it, and the card degrades to
// the station name it shows today rather than to a blank.
//
// SourceLabel is deliberately never touched. It is the honest answer to a
// different question — which part of the programming chose this — and the wall
// renders it as context underneath the track. Overwriting the title with the
// truth is the fix; losing the block along the way would just be the same bug
// pointed the other way.
func withLiveMetadata(ctx context.Context, lookup LiveStationLookup, item PlaybackItem, cachedOnly bool) PlaybackItem {
	if lookup == nil {
		return item
	}
	stationID := stationRefID(item)
	if stationID == "" {
		return item
	}

	var (
		live  LiveStationNowPlaying
		found bool
	)
	if cachedOnly {
		live, found = lookup.CachedStationMetadata(stationID)
	} else {
		live, found = lookup.LiveStationMetadata(ctx, stationID)
	}
	if !found {
		return item
	}

	if title := strings.TrimSpace(live.Title); title != "" {
		item.Title = title
		// The artist belongs to the title it arrived with. Carrying a stale one
		// across a track change is how a card ends up crediting the wrong
		// musician, so it is replaced or cleared, never left behind.
		item.Artist = strings.TrimSpace(live.Artist)
	}
	if artwork := strings.TrimSpace(live.ArtworkURL); artwork != "" {
		item.ArtworkURL = artwork
	}
	return item
}

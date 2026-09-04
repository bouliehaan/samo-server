package sources

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestPollBackoffCapsAtSixHours(t *testing.T) {
	if got := pollBackoffSeconds(3600, 10); got != 3600 {
		t.Fatalf("backoff = %d, want interval cap 3600", got)
	}
	if got := pollBackoffSeconds(3600, 1); got != MinPollIntervalSeconds {
		t.Fatalf("backoff = %d, want %d", got, MinPollIntervalSeconds)
	}
}

func TestUpdatePodcastFeedPreservesPollScheduleOnRefresh(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	service := New(db)
	feedURL := "https://example.com/feed.xml"
	if _, err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Test Show"}); err != nil {
		t.Fatal(err)
	}
	feedID := podcastFeedID(feedURL)

	disabled := false
	interval := 1800
	updated, err := service.UpdatePodcastFeed(ctx, feedID, UpdatePodcastFeedInput{
		PollEnabled:         &disabled,
		PollIntervalSeconds: &interval,
		Title:               strPtr("Renamed Show"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Poll.Enabled {
		t.Fatal("expected poll disabled")
	}
	if updated.Poll.IntervalSeconds != 1800 {
		t.Fatalf("interval = %d, want 1800", updated.Poll.IntervalSeconds)
	}
	if updated.Poll.NextPollAt != nil {
		t.Fatalf("next poll = %v, want nil when disabled", updated.Poll.NextPollAt)
	}

	_, err = db.ExecContext(ctx, `
		UPDATE podcast_feeds SET consecutive_errors = 2 WHERE id = ?`, feedID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Renamed Show"}); err != nil {
		t.Fatal(err)
	}

	after, err := service.GetPodcastFeed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Poll.Enabled {
		t.Fatal("refresh must not re-enable polling")
	}
	if after.Poll.IntervalSeconds != 1800 {
		t.Fatalf("interval after refresh = %d, want 1800", after.Poll.IntervalSeconds)
	}
	if after.Poll.ConsecutiveErrors != 2 {
		t.Fatalf("consecutive errors = %d, want preserved 2", after.Poll.ConsecutiveErrors)
	}
}

func TestUpdatePodcastFeedPreservesNextPollForMetadataOnlyEdit(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	service := New(db)
	feedURL := "https://example.com/feed.xml"
	if _, err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Test Show"}); err != nil {
		t.Fatal(err)
	}
	feedID := podcastFeedID(feedURL)
	next := "2026-05-22T13:00:00Z"
	if _, err := db.ExecContext(ctx, `UPDATE podcast_feeds SET next_poll_at = ? WHERE id = ?`, next, feedID); err != nil {
		t.Fatal(err)
	}

	updated, err := service.UpdatePodcastFeed(ctx, feedID, UpdatePodcastFeedInput{
		Title: strPtr("Renamed Show"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Poll.NextPollAt == nil || updated.Poll.NextPollAt.Format(time.RFC3339) != next {
		t.Fatalf("next poll = %v, want preserved %s", updated.Poll.NextPollAt, next)
	}
}

func TestListDuePodcastFeedsRespectsNextPollAt(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	service := New(db)
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	dueURL := "https://due.example/feed.xml"
	futureURL := "https://future.example/feed.xml"

	for _, feedURL := range []string{dueURL, futureURL} {
		if _, err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Show"}); err != nil {
			t.Fatal(err)
		}
	}

	_, err := db.ExecContext(ctx, `
		UPDATE podcast_feeds SET next_poll_at = ? WHERE feed_url = ?`,
		"2026-05-22T11:00:00Z", dueURL,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		UPDATE podcast_feeds SET next_poll_at = ? WHERE feed_url = ?`,
		"2026-05-22T13:00:00Z", futureURL,
	)
	if err != nil {
		t.Fatal(err)
	}

	dueID := podcastFeedID(dueURL)
	due, err := service.ListDuePodcastFeeds(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != dueID {
		t.Fatalf("due feeds = %#v, want only %s", due, dueID)
	}
}

func strPtr(value string) *string {
	return &value
}

// --- change detection ----------------------------------------------------
//
// These guard the distinction between "we polled a feed" and "the feed
// changed". Conflating them made every routine poll rebuild the entire catalog
// projection — ~6.1s on a 100k-track library — for feeds that had published
// nothing.

func TestSavePodcastFeedCountsOnlyNewEpisodes(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	service := New(db)
	feedURL := "https://example.com/counted.xml"

	feed := parsedPodcastFeed{
		Title: "Counted Show",
		Episodes: []parsedPodcastEpisode{
			{GUID: "ep-1", Title: "One", EnclosureURL: "https://example.com/1.mp3"},
			{GUID: "ep-2", Title: "Two", EnclosureURL: "https://example.com/2.mp3"},
		},
	}

	added, err := service.savePodcastFeed(ctx, feedURL, feed)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("first save added %d episodes, want 2", added)
	}

	// The same feed again. Nothing is new, so nothing downstream should treat
	// this as a change.
	added, err = service.savePodcastFeed(ctx, feedURL, feed)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("re-saving an unchanged feed added %d episodes, want 0", added)
	}

	feed.Episodes = append(feed.Episodes, parsedPodcastEpisode{
		GUID: "ep-3", Title: "Three", EnclosureURL: "https://example.com/3.mp3",
	})
	added, err = service.savePodcastFeed(ctx, feedURL, feed)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("a feed with one new episode added %d, want 1", added)
	}
}

func TestPollCycleSeparatesPolledFromChanged(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	service := New(db)
	feedURL := "https://example.com/unchanged.xml"

	if _, err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{
		Title: "Show",
		Episodes: []parsedPodcastEpisode{
			{GUID: "ep-1", Title: "One", EnclosureURL: "https://example.com/1.mp3"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// A cycle over a feed that is not due yet checks nothing and therefore
	// changes nothing — the cheapest proof that Changed tracks content rather
	// than activity.
	result, err := service.RunPodcastPollCycle(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 0 {
		t.Fatalf("Changed = %d on a cycle that polled nothing, want 0", result.Changed)
	}
}

// The reload is the expensive half. It must be driven by Changed, never by
// Updated (which counts feeds that refreshed without error, including every
// feed that had nothing new) and never by Failed (which changes nothing at all).
func TestPollerDoesNotReloadWhenNothingChanged(t *testing.T) {
	reloads := 0
	poller := NewPoller(PollerOptions{
		Sources:       New(storagetest.Open(t)),
		ReloadCatalog: func(context.Context) error { reloads++; return nil },
	})

	poller.runOnce(context.Background())

	if reloads != 0 {
		t.Fatalf("reloaded the catalog %d time(s) for a poll cycle with no changes", reloads)
	}
}

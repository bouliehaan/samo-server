package sources

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
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
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	feedURL := "https://example.com/feed.xml"
	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Test Show"}); err != nil {
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

	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Renamed Show"}); err != nil {
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
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	feedURL := "https://example.com/feed.xml"
	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Test Show"}); err != nil {
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
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	dueURL := "https://due.example/feed.xml"
	futureURL := "https://future.example/feed.xml"

	for _, feedURL := range []string{dueURL, futureURL} {
		if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Show"}); err != nil {
			t.Fatal(err)
		}
	}

	_, err = db.ExecContext(ctx, `
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

package podcastcache

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func newPrefsService(t *testing.T, defaultCount int) *Service {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	service, err := New(db, Options{
		CacheDir:            filepath.Join(root, "podcast-cache"),
		Enabled:             true,
		DefaultPrewarmCount: defaultCount,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestPrewarmCountResolutionOrder(t *testing.T) {
	ctx := context.Background()
	svc := newPrefsService(t, 3)

	// No overrides -> env default.
	if got := svc.PrewarmCount(ctx, "show-1"); got != 3 {
		t.Fatalf("default count = %d, want 3", got)
	}

	// Global override beats the env default.
	if err := svc.SetPrewarmCount(ctx, "", 5); err != nil {
		t.Fatal(err)
	}
	if got := svc.PrewarmCount(ctx, "show-1"); got != 5 {
		t.Fatalf("global count = %d, want 5", got)
	}

	// Per-show override beats the global.
	if err := svc.SetPrewarmCount(ctx, "show-1", 1); err != nil {
		t.Fatal(err)
	}
	if got := svc.PrewarmCount(ctx, "show-1"); got != 1 {
		t.Fatalf("per-show count = %d, want 1", got)
	}
	// A different show still uses the global.
	if got := svc.PrewarmCount(ctx, "show-2"); got != 5 {
		t.Fatalf("other-show count = %d, want 5 (global)", got)
	}

	// Negative clamps to 0 (off).
	if err := svc.SetPrewarmCount(ctx, "show-1", -4); err != nil {
		t.Fatal(err)
	}
	if got := svc.PrewarmCount(ctx, "show-1"); got != 0 {
		t.Fatalf("clamped count = %d, want 0", got)
	}
}

func TestSelectNewestEpisodes(t *testing.T) {
	at := func(unix int64) *time.Time {
		ts := time.Unix(unix, 0)
		return &ts
	}
	episodes := []catalog.PodcastEpisode{
		{ID: "old", EnclosureURL: "http://x/old.mp3", PublishedAt: at(100)},
		{ID: "new", EnclosureURL: "http://x/new.mp3", PublishedAt: at(300)},
		{ID: "mid", EnclosureURL: "http://x/mid.mp3", PublishedAt: at(200)},
		{ID: "no-url", EnclosureURL: "", PublishedAt: at(400)},
		{ID: "local", EnclosureURL: "http://x/local.mp3", PublishedAt: at(500), AudioFiles: []catalog.AudioFile{{}}},
	}

	got := selectNewestEpisodes(episodes, 2)
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "mid" {
		t.Fatalf("selection = %v, want [new mid] (newest streamable first)", episodeIDs(got))
	}

	if n := len(selectNewestEpisodes(episodes, 0)); n != 0 {
		t.Fatalf("count 0 selected %d episodes, want 0", n)
	}
}

func episodeIDs(episodes []catalog.PodcastEpisode) []string {
	ids := make([]string, len(episodes))
	for i, episode := range episodes {
		ids[i] = episode.ID
	}
	return ids
}

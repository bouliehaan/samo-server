package playback

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// The channel scheduler asks two questions of this table and must not get one
// answer: "has a person here heard this" and "has the station itself aired
// this". Merged, an airing to an empty room read as somebody having listened.
func TestAnyListenerByIDsApartKeepsOneAccountSeparate(t *testing.T) {
	ctx := t.Context()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash)
		VALUES ('user-1', 'listener', 'Listener', 'user', ''),
		       ('user-2', 'other', 'Other', 'user', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, playback_json, added_at, updated_at)
		VALUES
			('track-a', 'A', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('track-b', 'B', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('track-c', 'C', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	patch := func(user, track string, progress int, completed bool) {
		t.Helper()
		if _, err := service.Patch(ctx, user, TargetMusicTrack, track, PatchInput{
			ProgressSeconds: &progress, Completed: &completed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// track-a: the station (user-server) played it in full; a person got
	// forty seconds in. track-b: only the station. track-c: only people.
	patch("user-server", "track-a", 600, true)
	patch("user-1", "track-a", 40, false)
	patch("user-server", "track-b", 600, true)
	patch("user-1", "track-c", 100, false)
	patch("user-2", "track-c", 300, true)

	people, station, err := service.AnyListenerByIDsApart(ctx, TargetMusicTrack,
		[]string{"track-a", "track-b", "track-c", "track-none"}, "user-server")
	if err != nil {
		t.Fatal(err)
	}

	if got := people["track-a"]; got.Completed || got.ProgressSeconds != 40 {
		t.Fatalf("track-a for people = %+v, want the person's own forty seconds, not the station's airing", got)
	}
	if got := station["track-a"]; !got.Completed || got.ProgressSeconds != 600 {
		t.Fatalf("track-a for the station = %+v, want its full airing", got)
	}
	if _, ok := people["track-b"]; ok {
		t.Fatal("track-b was only ever aired by the station, yet came back as heard by a person")
	}
	if got := station["track-b"]; !got.Completed {
		t.Fatalf("track-b for the station = %+v, want completed", got)
	}
	// People still merge the way AnyListenerByIDs always has: completed if
	// anyone completed it, progress the furthest anyone reached.
	if got := people["track-c"]; !got.Completed || got.ProgressSeconds != 300 {
		t.Fatalf("track-c for people = %+v, want merged across both listeners", got)
	}
	if _, ok := station["track-c"]; ok {
		t.Fatal("track-c came back as aired by the station, which never touched it")
	}
	if _, ok := people["track-none"]; ok {
		t.Fatal("a track nobody has played came back with state")
	}

	// And with nobody set apart, everything merges — the station included.
	all, none, err := service.AnyListenerByIDsApart(ctx, TargetMusicTrack, []string{"track-a"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := all["track-a"]; !got.Completed || got.ProgressSeconds != 600 {
		t.Fatalf("with nobody apart, track-a = %+v, want everything merged", got)
	}
	if len(none) != 0 {
		t.Fatalf("with nobody apart, %d rows were set apart anyway", len(none))
	}
}

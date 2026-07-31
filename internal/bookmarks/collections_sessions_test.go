package bookmarks_test

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/bookmarks"
)

// Collections and listening sessions carry the same per-user scoping rule as
// bookmarks, and had the same amount of test coverage: none.

func mustCreateCollection(t *testing.T, svc *bookmarks.Service, userID, name string, books ...string) bookmarks.Collection {
	t.Helper()
	c, err := svc.CreateCollection(context.Background(), userID, bookmarks.CreateCollectionInput{
		Name:         name,
		Description:  "a shelf",
		AudiobookIDs: books,
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	return c
}

func TestCollectionRoundTripWithMembers(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	created := mustCreateCollection(t, svc, alice, "Favourites", book, other)
	if created.ID == "" || created.Name != "Favourites" {
		t.Fatalf("unexpected collection: %+v", created)
	}

	got, err := svc.GetCollection(ctx, alice, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AudiobookCount != 2 {
		t.Fatalf("expected 2 members, got %d (%v)", got.AudiobookCount, got.AudiobookIDs)
	}
}

func TestCollectionsAreScopedToTheirOwner(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateCollection(t, svc, alice, "Private", book)

	list, err := svc.ListCollections(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == created.ID {
			t.Fatal("bob's collection list includes alice's collection")
		}
	}

	if _, err := svc.GetCollection(ctx, bob, created.ID); err == nil {
		t.Fatal("bob was allowed to read alice's collection by id")
	}
}

func TestCollectionUpdateAndDeleteRefuseAnotherUser(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateCollection(t, svc, alice, "Private", book)

	newName := "Hijacked"
	if _, err := svc.UpdateCollection(ctx, bob, created.ID, bookmarks.UpdateCollectionInput{Name: &newName}); err == nil {
		t.Fatal("bob was allowed to rename alice's collection")
	}
	if err := svc.DeleteCollection(ctx, bob, created.ID); err == nil {
		t.Fatal("bob was allowed to delete alice's collection")
	}

	still, err := svc.GetCollection(ctx, alice, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Name != "Private" {
		t.Fatalf("alice's collection was modified: %q", still.Name)
	}
}

func TestCollectionUpdateReplacesMembership(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateCollection(t, svc, alice, "Shelf", book, other)

	updated, err := svc.UpdateCollection(ctx, alice, created.ID, bookmarks.UpdateCollectionInput{
		AudiobookIDs: []string{book},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AudiobookCount != 1 {
		t.Fatalf("membership should have been replaced, got %d (%v)", updated.AudiobookCount, updated.AudiobookIDs)
	}
}

func TestCollectionDeleteRemovesIt(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateCollection(t, svc, alice, "Temp", book)

	if err := svc.DeleteCollection(ctx, alice, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetCollection(ctx, alice, created.ID); err == nil {
		t.Fatal("collection survived deletion")
	}
}

func TestRecordSessionAndListForAudiobook(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	session, err := svc.RecordSession(ctx, alice, bookmarks.RecordSessionInput{
		AudiobookID:          book,
		StartPositionSeconds: 0,
		EndPositionSeconds:   600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" {
		t.Fatal("expected a session id")
	}

	got, err := svc.ListSessionsForAudiobook(ctx, alice, book, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 session, got %d", len(got))
	}
}

func TestSessionsAreScopedToTheirOwner(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if _, err := svc.RecordSession(ctx, alice, bookmarks.RecordSessionInput{
		AudiobookID: book, EndPositionSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ListSessionsForAudiobook(ctx, bob, book, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("bob can see alice's listening sessions: %+v", got)
	}

	recent, err := svc.ListRecentSessions(ctx, bob, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Fatalf("bob's recent sessions include alice's: %+v", recent)
	}
}

// Listening history is the kind of list that grows forever, so the limit has
// to actually bound the result.
func TestListRecentSessionsHonoursLimit(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := svc.RecordSession(ctx, alice, bookmarks.RecordSessionInput{
			AudiobookID:          book,
			StartPositionSeconds: i * 100,
			EndPositionSeconds:   (i + 1) * 100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.ListRecentSessions(ctx, alice, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("limit not honoured: got %d sessions, want 2", len(got))
	}
}

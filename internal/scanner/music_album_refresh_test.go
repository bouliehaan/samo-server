package scanner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestRefreshOneMusicAlbumPicksMajorityTitleAndTrackCover(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	albumID := stableID("album", "meta", "kanye west", "graduation")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, display_artist, images_json, updated_at)
		VALUES (?, 'Stale Title', '', '[]', CURRENT_TIMESTAMP)`, albumID); err != nil {
		t.Fatalf("insert album: %v", err)
	}
	coverPath := filepath.Join(t.TempDir(), "cover.jpg")
	imagesJSON := jsonText([]catalog.Image{{ID: stableID("image", coverPath), Path: coverPath, MimeType: "image/jpeg"}})

	for _, row := range []struct {
		id, title, albumTitle string
	}{
		{"track_a", "Good Morning", "Graduation"},
		{"track_b", "Champion", "Graduation"},
		{"track_c", "Flashing Lights", "Graduation (Deluxe)"},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO music_tracks (id, title, album_id, album_title, images_json, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			row.id, row.title, albumID, row.albumTitle, imagesJSON); err != nil {
			t.Fatalf("insert track %s: %v", row.id, err)
		}
	}

	scanner := New(db)
	if err := scanner.refreshOneMusicAlbum(ctx, albumID); err != nil {
		t.Fatalf("refresh album: %v", err)
	}

	var title, imagesJSONOut string
	if err := db.QueryRowContext(ctx, `SELECT title, images_json FROM music_albums WHERE id = ?`, albumID).
		Scan(&title, &imagesJSONOut); err != nil {
		t.Fatalf("read album: %v", err)
	}
	if title != "Graduation" {
		t.Fatalf("title = %q, want majority Graduation", title)
	}
	if imagesJSONOut == "" || imagesJSONOut == "[]" {
		t.Fatalf("expected album cover copied from tracks, got %q", imagesJSONOut)
	}
}

func TestAlbumIdentityReleaseDateKeyUsesYearOnly(t *testing.T) {
	tagsA := normalizeTags(map[string]string{"date": "1964"})
	tagsB := normalizeTags(map[string]string{"date": "1964-08-15"})
	if albumIdentityReleaseDateKey(tagsA) != "1964" || albumIdentityReleaseDateKey(tagsB) != "1964" {
		t.Fatalf("expected both dates to normalize to 1964")
	}
	artists := []string{"Miles Davis"}
	title := "Quiet Nights"
	a := resolveMusicAlbumID(tagsA, title, "Miles Davis/Quiet Nights", artists)
	b := resolveMusicAlbumID(tagsB, title, "Miles Davis/Quiet Nights", artists)
	if a != b {
		t.Fatalf("year-normalized meta ids should match, got %q and %q", a, b)
	}
}

func TestResolveMusicAlbumIDUsesFolderArtistForTaggedAlbums(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"album":  "Graduation",
		"artist": "Kanye West",
	})
	id := resolveMusicAlbumID(tags, "Graduation", "Kanye West/Graduation", nil)
	want := stableID("album", "meta", "kanye west", "graduation")
	if id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
}

func TestResolveMusicAlbumIDPrefersReleaseIDOverReleaseGroup(t *testing.T) {
	withRelease := resolveMusicAlbumID(
		normalizeTags(map[string]string{
			"musicbrainz_albumid":        "release-a",
			"musicbrainz_releasegroupid": "group-shared",
		}),
		"Album", "Artist/Album", []string{"Artist"},
	)
	withGroupOnly := resolveMusicAlbumID(
		normalizeTags(map[string]string{
			"musicbrainz_releasegroupid": "group-shared",
		}),
		"Album", "Artist/Album", []string{"Artist"},
	)
	if withRelease == withGroupOnly {
		t.Fatal("release id and release group should not share the same album id when both tags are present")
	}
}

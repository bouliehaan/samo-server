package explo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/metadata"
)

const requestedAlbumTitle = "Chet Baker Sings and Plays"

// albumCoverFixture is a request fixture whose cover pass verifies through a
// fake cover store, with the text-searched sources stubbed to find nothing.
func albumCoverFixture(t *testing.T) (requestFixture, *fakeCoverStore, *int, *int) {
	t.Helper()
	f := newRequestFixture(t, acoustidRequestedSong)
	covers := newFakeCoverStore(t)
	f.service.covers = covers
	f.service.metadataApply = metadata.NewMetadataApplyServiceWithOptions(f.db, metadata.MetadataApplyOptions{CoverDownloader: covers})
	itunesHits, deezerHits := withStubCoverSources(t, "", "")
	return f, covers, itunesHits, deezerHits
}

// addRequestedAlbumDrop adds a downloaded track of the requested album:
// staged in the drop folder, identified and credited to the album, its cover
// pass due.
func (f requestFixture) addRequestedAlbumDrop(t *testing.T, trackID, title string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO music_tracks (id, title, album_id) VALUES (?, ?, 'album-drop')`, trackID, title); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name)
		VALUES (?, 'lib-1', ?, ?, ?, ?)`,
		"file-"+trackID, trackID, filepath.Join(f.drop, trackID+".mp3"), "explo/"+trackID+".mp3", trackID+".mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`
		INSERT INTO explo_tracks (track_id, status, musicbrainz_release_group_id, matched_title, matched_artist, matched_album)
		VALUES (?, 'matched', ?, ?, 'Chet Baker', ?)`, trackID, requestedAlbum, title, requestedAlbumTitle); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`
		INSERT INTO explo_requests (recording_id, title, artist, album, album_id, album_artist, state, track_id)
		VALUES (?, ?, 'Chet Baker', ?, ?, 'Chet Baker', ?, ?)`,
		"rec-"+trackID, title, requestedAlbumTitle, requestedAlbum, RequestIdentifying, trackID); err != nil {
		t.Fatal(err)
	}
}

// An album requested whole wears one cover. The source let the first track
// down and served the second; the moment it did, the second's cover went —
// in the same pass, with no further search — to the first (a placeholder
// until then), to a track not tried yet, and to a library copy kept before
// the album had any art. A download that turned out to be another song keeps
// its own.
func TestAlbumCoverGoesToEveryTrackTheMomentOneGetsIt(t *testing.T) {
	ctx := context.Background()
	f, covers, itunesHits, deezerHits := albumCoverFixture(t)
	albumArt := caaBaseURL + "/release-group/" + requestedAlbum + "/front-500"
	covers.allow(albumArt)
	covers.refuse(albumArt, 1)

	f.addRequestedAlbumDrop(t, "track-a", "Let's Get Lost")
	f.addRequestedAlbumDrop(t, "track-b", "But Not For Me")
	f.addRequestedAlbumDrop(t, "track-c", "Time After Time")
	// Kept an hour after identification while no track had art: its drop has
	// rotated out, and the copy carries none.
	mustExec(t, f.db, `
		INSERT INTO music_albums (id, title, track_count) VALUES ('album-kept', '`+requestedAlbumTitle+`', 1);
		INSERT INTO music_tracks (id, title, album_id) VALUES ('library-d', 'My Funny Valentine', 'album-kept');
		INSERT INTO explo_requests (recording_id, title, artist, album, album_id, state, track_id, library_track_id)
		VALUES ('rec-d', 'My Funny Valentine', 'Chet Baker', '`+requestedAlbumTitle+`', '`+requestedAlbum+`', 'in-library', 'track-d-rotated', 'library-d');`)
	// Asked for as a track of the album, but the audio is another record's.
	f.addRequestedAlbumDrop(t, "track-wrong", "I Fall in Love Too Easily")
	mustExec(t, f.db, `UPDATE explo_tracks SET matched_album = 'Chet Baker in Tokyo', musicbrainz_release_group_id = 'rg-tokyo',
		cover_status = 'placeholder', cover_attempted_at = '2999-01-01T00:00:00Z' WHERE track_id = 'track-wrong'`)

	_, placeholders, err := f.service.backfillMissingCovers(ctx, []string{f.drop})
	if err != nil {
		t.Fatal(err)
	}
	if placeholders != 1 {
		t.Fatalf("placeholders = %d; want one, for the track the source refused", placeholders)
	}
	cover := f.service.existingTrackCover(ctx, "track-b")
	if !cover.real() {
		t.Fatalf("track-b has no real cover: %+v", cover)
	}
	for _, id := range []string{"track-a", "track-c", "library-d"} {
		if got := f.service.existingTrackCover(ctx, id); !got.real() || got.localPath != cover.localPath {
			t.Errorf("%s wears %+v; want the album's cover %s", id, got, cover.localPath)
		}
	}
	for _, id := range []string{"track-a", "track-b", "track-c"} {
		var status string
		if err := f.db.QueryRow(`SELECT cover_status FROM explo_tracks WHERE track_id = ?`, id).Scan(&status); err != nil || status != coverStatusDone {
			t.Errorf("%s cover status %q %v; want done", id, status, err)
		}
	}
	// Only track-a, before any track had art, searched past Cover Art Archive.
	if *itunesHits != 2 || *deezerHits != 2 {
		t.Fatalf("itunes/deezer searches = %d/%d; want 2/2, all track-a's", *itunesHits, *deezerHits)
	}
	if got := f.service.existingTrackCover(ctx, "track-wrong"); got.localPath == cover.localPath {
		t.Fatal("a download of another record took the album's cover")
	}

	// The request waiting on track-a's art is kept with it now.
	if !f.service.adoptAlbumCover(ctx, "track-a") {
		t.Fatal("track-a does not wear its album's cover")
	}
}

// A track of a requested album that came with art of its own — a sharer's
// embedded picture — wears the album's cover once the album has one, so the
// album does not show two.
func TestRequestedAlbumTrackWearsTheAlbumCoverOverItsOwn(t *testing.T) {
	ctx := context.Background()
	f, covers, itunesHits, deezerHits := albumCoverFixture(t)
	const albumArt = "https://cdn-images.dzcdn.net/images/cover/e99d83c7/1000x1000.jpg"
	covers.allow(albumArt)
	album, err := covers.DownloadFromURL(ctx, albumArt)
	if err != nil {
		t.Fatal(err)
	}
	sharerFile := covers.writeFile("sharer.jpg")

	f.addRequestedAlbumDrop(t, "track-a", "Let's Get Lost")
	f.addRequestedAlbumDrop(t, "track-b", "But Not For Me")
	mustExec(t, f.db, `UPDATE explo_tracks SET cover_status = 'done', cover_attempted_at = '2026-10-01T18:00:00Z' WHERE track_id = 'track-a'`)
	if _, err := f.db.Exec(`INSERT INTO metadata_overrides (target_kind, target_id, fields_json) VALUES ('music-track', 'track-a', ?)`,
		`{"cover":{"id":"`+album.ID+`","url":"`+albumArt+`","path":"`+album.Path+`"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE music_tracks SET images_json = ? WHERE id = 'track-b'`,
		`[{"id":"cover-sharer","path":"`+sharerFile+`"}]`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.service.backfillMissingCovers(ctx, []string{f.drop}); err != nil {
		t.Fatal(err)
	}
	if got := f.service.existingTrackCover(ctx, "track-b"); got.localPath != album.Path {
		t.Fatalf("track-b wears %s; want the album's cover %s", got.localPath, album.Path)
	}
	if *itunesHits != 0 || *deezerHits != 0 {
		t.Fatalf("searched for art the album already had: itunes/deezer = %d/%d", *itunesHits, *deezerHits)
	}
}

// An album only Deezer lists has no release group to look its art up by. Its
// tracks wear Deezer's cover of it, the one Search for new showed, without a
// text search: Czarface's "Czarface Meets Frankie Pulitzer" on 2026-10-03 sat
// at "Fetching cover art" for every track with its cover on the search page.
func TestDeezerAlbumWearsTheCoverSearchShowed(t *testing.T) {
	ctx := context.Background()
	f, covers, itunesHits, deezerHits := albumCoverFixture(t)
	const album = "deezer-750402811"
	deezerArt := deezerAlbumURL + "/750402811/image?size=xl"
	covers.allow(deezerArt)
	f.addRequestedAlbumDrop(t, "track-a", "Brothers Grimm")
	f.addRequestedAlbumDrop(t, "track-b", "All it Takes is One Bad Day")
	mustExec(t, f.db, `UPDATE explo_tracks SET musicbrainz_release_group_id = '', matched_album = 'Czarface Meets Frankie Pulitzer', matched_artist = 'Czarface';
		UPDATE explo_requests SET album_id = '`+album+`', album = 'Czarface Meets Frankie Pulitzer', artist = 'Czarface', album_artist = 'Czarface';`)

	applied, placeholders, err := f.service.backfillMissingCovers(ctx, []string{f.drop})
	if err != nil {
		t.Fatal(err)
	}
	if applied < 2 || placeholders != 0 {
		t.Fatalf("applied %d, placeholders %d", applied, placeholders)
	}
	for _, id := range []string{"track-a", "track-b"} {
		if got := f.service.existingTrackCover(ctx, id); !got.real() {
			t.Fatalf("%s has no real cover: %+v", id, got)
		}
	}
	if *itunesHits != 0 || *deezerHits != 0 {
		t.Fatalf("searched for art the request named: itunes/deezer = %d/%d", *itunesHits, *deezerHits)
	}
}

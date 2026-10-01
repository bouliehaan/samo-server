package explo

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const requestedAlbum = "d0c2b5a1-7e3f-4b6a-9c8d-1e2f3a4b5c6d"

// albumJob is the requested song asked for as track 3 of a different album
// from the one identification would choose for it (Chet Baker Sings).
func albumJob(state string) DownloadJob {
	job := requestedJob(state)
	job.Song.AlbumTrack = &AlbumTrack{AlbumID: requestedAlbum, AlbumTitle: "Chet Baker Sings and Plays", AlbumArtist: "Chet Baker",
		Number: 3, TrackTotal: 10, Disc: 1, DiscTotal: 1, Year: 1955}
	return job
}

// A track of a whole album lands in that album: credited to it at
// identification, given its art, and kept at its place on it — even though
// identification alone files the song under another record, and the library
// already has the song on that other record.
func TestRequestedAlbumTrackIsKeptAsThatAlbum(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	if _, err := f.db.Exec(`
		INSERT INTO music_albums (id, title, track_count) VALUES ('album-sings', 'Chet Baker Sings', 1);
		INSERT INTO music_tracks (id, title, display_artist, album_id, album_title, duration_seconds, external_ids_json)
		VALUES ('library-sings', 'You Don''t Know What Love Is', 'Chet Baker', 'album-sings', 'Chet Baker Sings', 400,
		        '{"musicBrainzRecordingId":"` + requestedRecording + `"}')`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RecordRequest(ctx, albumJob("queued"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	staged := albumJob("staged")
	staged.File = "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3"
	remote := exploStub(t, &staged)
	f.service.AdvanceRequests(ctx, remote)
	if _, err := f.service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	var album, releaseGroup string
	if err := f.db.QueryRow(`SELECT matched_album, musicbrainz_release_group_id FROM explo_tracks WHERE track_id = 'track-drop'`).Scan(&album, &releaseGroup); err != nil {
		t.Fatal(err)
	}
	if album != "Chet Baker Sings and Plays" || releaseGroup != requestedAlbum {
		t.Fatalf("identified as %q [%s]; want the requested album", album, releaseGroup)
	}

	f.settleCover(t)
	f.service.AdvanceRequests(ctx, remote)
	if got := f.request(t); got.State != RequestInLibrary || got.AlbumID != requestedAlbum {
		t.Fatalf("request after keep = %+v", got)
	}
	kept := filepath.Join(f.root, "Chet Baker", "Chet Baker Sings and Plays", "03 - You Don’t Know What Love Is.mp3")
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("no library copy at its place on the album, %s: %v", kept, err)
	}

	view, ok, err := f.service.AlbumRequest(ctx, requestedAlbum)
	if err != nil || !ok || view.Title != "Chet Baker Sings and Plays" || len(view.Tracks) != 1 ||
		view.Tracks[0].Number != 3 || view.Tracks[0].State != RequestInLibrary {
		t.Fatalf("album request = %+v ok=%v err=%v", view, ok, err)
	}
}

// The identify pass can reach the download before the request is linked to
// it, and credit it to the song's own album. The request moves it to the
// requested one, and waits for that album's art.
func TestAlbumTrackIdentifiedBeforeItsRequestIsMovedToTheAlbum(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	if _, err := f.service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	f.settleCover(t)
	staged := albumJob("staged")
	staged.File = "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3"
	if err := f.service.RecordRequest(ctx, staged, "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	var releaseGroup, coverStatus string
	if err := f.db.QueryRow(`SELECT musicbrainz_release_group_id, cover_status FROM explo_tracks WHERE track_id = 'track-drop'`).Scan(&releaseGroup, &coverStatus); err != nil {
		t.Fatal(err)
	}
	if releaseGroup != requestedAlbum || coverStatus != "" {
		t.Fatalf("not moved to the requested album, or its art not re-fetched: %s %q", releaseGroup, coverStatus)
	}
	if got := f.request(t); got.State != RequestIdentifying {
		t.Fatalf("kept before the album's art: %+v", got)
	}
	f.settleCover(t)
	f.service.AdvanceRequests(ctx, nil)
	kept := filepath.Join(f.root, "Chet Baker", "Chet Baker Sings and Plays", "03 - You Don’t Know What Love Is.mp3")
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("no library copy at %s: %v", kept, err)
	}
}

// A song asked for on its own after its album was requested does not take
// the track off the album.
func TestSongRequestLeavesItsAlbumAlone(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	if err := f.service.RecordRequest(ctx, albumJob("failed"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RecordRequest(ctx, requestedJob("queued"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	row, ok, err := f.service.loadRequest(ctx, requestedRecording)
	if err != nil || !ok || row.albumID != requestedAlbum || row.album != "Chet Baker Sings and Plays" || row.trackNumber != 3 {
		t.Fatalf("album placement lost: %+v", row)
	}
}

func TestKeepDestinationNumbersDiscs(t *testing.T) {
	placed := (&keepPlacement{Title: "Album", Artist: "Artist", TrackNumber: 1, DiscNumber: 2, DiscTotal: 2}).apply(catalog.MusicTrack{Title: "Song"})
	if got := filepath.Base(keepDestination("/lib", placed, "Album", ".mp3")); got != "2-01 - Song.mp3" {
		t.Fatalf("second disc's first track named %q", got)
	}
	single := (&keepPlacement{Title: "Album", Artist: "Artist", TrackNumber: 7, DiscNumber: 1, DiscTotal: 1}).apply(catalog.MusicTrack{Title: "Song"})
	if got := keepDestination("/lib", single, "Album", ".mp3"); got != filepath.Join("/lib", "Artist", "Album", "07 - Song.mp3") {
		t.Fatalf("single-disc track at %q", got)
	}
}

// A track of an album only Deezer lists. MusicBrainz has never heard of it,
// so AcoustID and the text searches come back empty; the request is
// identified from the listing it was chosen from and kept as the album's
// track — and no Deezer id ends up where a MusicBrainz id belongs.
func TestDeezerAlbumTrackIsIdentifiedFromItsListing(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, `{"status": "ok", "results": []}`)
	job := DownloadJob{ID: "deezer-2657539542", State: "queued", Song: Song{ID: "deezer-2657539542", Source: "deezer",
		Title: "You Don't Know What Love Is", Artist: "Chet Baker", Album: "Puer Aeternus", DurationMS: 398000,
		AlbumTrack: &AlbumTrack{AlbumID: "deezer-546260002", AlbumTitle: "Puer Aeternus", AlbumArtist: "Chet Baker", Number: 1, TrackTotal: 10, Disc: 1, DiscTotal: 1, Year: 2024}}}
	if err := f.service.RecordRequest(ctx, job, "user-owner"); err != nil {
		t.Fatal(err)
	}
	job.State, job.File = "staged", "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3"
	remote := exploStub(t, &job)
	f.service.AdvanceRequests(ctx, remote)
	candidates, err := f.service.findCandidateTracks(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].musicBrainzRecordingID != "" || candidates[0].requestRecording != job.ID {
		t.Fatalf("a Deezer id offered as MusicBrainz evidence: %+v %v", candidates, err)
	}
	if result, err := f.service.ProcessNewTracks(ctx); err != nil || result.Unmatched != 1 {
		t.Fatalf("identify pass: %+v %v", result, err)
	}

	f.service.AdvanceRequests(ctx, remote)
	var status, album, releaseGroup, recording, title string
	if err := f.db.QueryRow(`SELECT status, matched_album, musicbrainz_release_group_id, musicbrainz_recording_id, matched_title
		FROM explo_tracks WHERE track_id = 'track-drop'`).Scan(&status, &album, &releaseGroup, &recording, &title); err != nil {
		t.Fatal(err)
	}
	if status != "matched-fallback" || album != "Puer Aeternus" || releaseGroup != "" || recording != "" || title != "You Don't Know What Love Is" {
		t.Fatalf("ledger = %s %q [%q] rec=%q %q", status, album, releaseGroup, recording, title)
	}
	if got, _, _ := f.service.Request(ctx, job.ID); got.State != RequestIdentifying || !strings.Contains(got.Message, "Deezer") {
		t.Fatalf("request after adoption = %+v", got)
	}

	f.settleCover(t)
	f.service.AdvanceRequests(ctx, remote)
	if got, _, _ := f.service.Request(ctx, job.ID); got.State != RequestInLibrary {
		t.Fatalf("request after keep = %+v", got)
	}
	kept := filepath.Join(f.root, "Chet Baker", "Puer Aeternus", "01 - You Don't Know What Love Is.mp3")
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("no library copy at %s: %v", kept, err)
	}
	if placement := (songRequestRow{albumID: "deezer-546260002", album: "Puer Aeternus"}).placement(); placement.ReleaseGroupID != "" {
		t.Fatalf("a Deezer album id written as a release group: %+v", placement)
	}
}

// The listing names the song but cannot hear it; the one check left is its
// length, and a download far from it waits for review.
func TestDeezerTrackOfTheWrongLengthWaitsForReview(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, `{"status": "ok", "results": []}`)
	job := DownloadJob{ID: "deezer-2657539542", State: "staged", File: "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3",
		Song: Song{ID: "deezer-2657539542", Title: "You Don't Know What Love Is", Artist: "Chet Baker", DurationMS: 102000}}
	if err := f.service.RecordRequest(ctx, job, "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if _, err := f.service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	got, _, _ := f.service.Request(ctx, job.ID)
	if got.State != RequestNeedsReview || !strings.Contains(got.Message, "6:40") || !strings.Contains(got.Message, "1:42") {
		t.Fatalf("request = %+v", got)
	}
}

func TestCatalogIDs(t *testing.T) {
	for id, want := range map[string][2]bool{
		"4a7fea2e-545b-4c63-bc9a-9943cc3a29d7": {true, true},
		"deezer-546260002":                     {false, true},
		"deezer-":                              {false, false},
		"deezer-1x":                            {false, false},
		"546260002":                            {false, false},
		"../deezer-1":                          {false, false},
	} {
		if MusicBrainzID(id) != want[0] || ValidCatalogID(id) != want[1] {
			t.Errorf("%q: musicBrainz=%v valid=%v", id, MusicBrainzID(id), ValidCatalogID(id))
		}
	}
}

// A placeholder is not art: a request waits the hour for the cover pass's
// next try before keeping a copy that would carry none for good — and is
// kept once the hour is up, so a source that is down for the day does not
// hold the song back.
func TestRequestWaitsOutAPlaceholderCover(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	staged := requestedJob("staged")
	staged.File = "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3"
	if err := f.service.RecordRequest(ctx, staged, "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if _, err := f.service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE explo_tracks SET cover_status = ? WHERE track_id = 'track-drop'`, coverStatusPlaceholder); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if got := f.request(t); got.State != RequestIdentifying || !strings.Contains(got.Message, "cover") {
		t.Fatalf("kept with a placeholder inside the hour: %+v", got)
	}
	if _, err := f.db.Exec(`UPDATE explo_tracks SET processed_at = '2026-01-01T00:00:00Z' WHERE track_id = 'track-drop'`); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if got := f.request(t); got.State != RequestInLibrary {
		t.Fatalf("still waiting after the hour: %+v", got)
	}
}

// Tracks identified as one album share the real cover one of them got, with
// no network; a drop identified as another album does not.
func TestSiblingOnTheSameAlbumSharesItsCover(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	covers := newFakeCoverStore(t)
	f.service.covers = covers
	const url = "https://cdn-images.dzcdn.net/images/cover/e99d83c7/1000x1000.jpg"
	covers.allow(url)
	cover := covers.writeFile("puer.jpg")
	if _, err := f.db.Exec(`
		INSERT INTO music_tracks (id, title, album_id) VALUES ('track-sibling', 'cut-throat eel', 'album-drop'), ('track-other', 'other', 'album-drop');
		INSERT INTO explo_tracks (track_id, status, matched_title, matched_artist, matched_album, cover_status, processed_at)
		VALUES ('track-sibling', 'matched-fallback', 'cut-throat eel', 'Quangou', 'Puer Aeternus', 'done', '2026-10-01T18:26:55Z'),
		       ('track-drop', 'matched-fallback', 'puer aeternus', 'quangou', 'puer aeternus', 'placeholder', '2026-10-01T18:26:36Z'),
		       ('track-other', 'matched-fallback', 'x', 'Quangou', 'sloth in motion EP', 'placeholder', '2026-10-01T18:26:36Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO metadata_overrides (target_kind, target_id, fields_json) VALUES ('music-track', 'track-sibling', ?)`,
		`{"cover":{"url":"`+url+`","path":"`+cover+`"}}`); err != nil {
		t.Fatal(err)
	}
	if !f.service.adoptAlbumCover(ctx, "track-drop") {
		t.Fatal("the album's cover was not shared")
	}
	if f.service.adoptAlbumCover(ctx, "track-other") {
		t.Fatal("a cover was shared across albums")
	}
	var status string
	if err := f.db.QueryRow(`SELECT cover_status FROM explo_tracks WHERE track_id = 'track-drop'`).Scan(&status); err != nil || status != coverStatusDone {
		t.Fatalf("cover status %q %v", status, err)
	}
}

// Puer Aeternus track 1: the identify pass errored (fpcalc could not read
// the file), not merely matched nothing. A retry an hour later cannot do
// better for a recording MusicBrainz does not know, so the listing is taken
// on the first pass all the same.
func TestDeezerTrackWhoseFingerprintFailsIsTakenFromItsListing(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, `{"status": "ok", "results": []}`)
	f.service.fpcalcPath = fakeFpcalc(t, `echo 'ERROR: Error decoding audio frame (End of file)' >&2; exit 3`)
	job := DownloadJob{ID: "deezer-2657539542", State: "staged", File: "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3",
		Song: Song{ID: "deezer-2657539542", Title: "You Don't Know What Love Is", Artist: "Chet Baker", Album: "Puer Aeternus", DurationMS: 400000}}
	if err := f.service.RecordRequest(ctx, job, "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if result, err := f.service.ProcessNewTracks(ctx); err != nil || result.Errored != 1 {
		t.Fatalf("identify pass: %+v %v", result, err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if got, _, _ := f.service.Request(ctx, job.ID); got.State != RequestIdentifying || !strings.Contains(got.Message, "Deezer") {
		t.Fatalf("request = %+v", got)
	}
}

// testFrames is n frames of a raw fingerprint: seed picks the audio, so equal
// seeds are the same recording and different ones are not. The frames are
// hashed (splitmix64) so unrelated audio shares about half its bits, as real
// chromaprint frames do.
func testFrames(seed uint32, n int) []uint32 {
	out := make([]uint32, n)
	x := uint64(seed) << 32
	for i := range out {
		x += 0x9e3779b97f4a7c15
		z := (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		out[i] = uint32(z ^ (z >> 31))
	}
	return out
}

// fingerprintFrames is testFrames as fpcalc -raw -json prints them.
func fingerprintFrames(seed uint32) string {
	frames := make([]string, 0, 60)
	for _, frame := range testFrames(seed, 60) {
		frames = append(frames, strconv.FormatUint(uint64(frame), 10))
	}
	return "[" + strings.Join(frames, ",") + "]"
}

// Quangou's "sloth in motion EP", 2026-10-01: YouTube's one upload from the EP
// was track 1, titled with the EP's name, and the title track was downloaded
// from it too — 2:02 of audio against a 2:09 listing, inside the length check,
// so it was kept as "sloth in motion" with "forest of no return"'s audio. Two
// tracks of one album are never one recording: the one whose listing the audio
// fits worse waits for review, and nothing is decided while a track of the
// album is still downloading.
func TestDeezerAlbumTrackWithItsSiblingsAudioWaitsForReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dropAudio uint32
		want      string
	}{
		{"same audio as track 1", 1, RequestNeedsReview},
		{"its own audio", 2, RequestIdentifying},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newRequestFixture(t, `{"status": "ok", "results": []}`)
			kept := filepath.Join(f.root, "Quangou", "sloth in motion EP", "01 - forest of no return.mp3")
			if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(kept, []byte("ID3 not really audio"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE music_tracks SET duration_seconds = 122 WHERE id = 'track-drop'`); err != nil {
				t.Fatal(err)
			}
			f.service.fpcalcPath = fakeFpcalc(t, `case "$*" in
*-raw*forest*) echo '{"duration": 121.8, "fingerprint": `+fingerprintFrames(1)+`}' ;;
*-raw*) echo '{"duration": 121.8, "fingerprint": `+fingerprintFrames(tc.dropAudio)+`}' ;;
*) echo '{"duration": 121.8, "fingerprint": "FP-REQUESTED"}' ;;
esac`)
			ep := func(id, title string, number, ms int, state, file string) DownloadJob {
				return DownloadJob{ID: id, State: state, File: file, Song: Song{ID: id, Source: "deezer", Title: title, Artist: "Quangou",
					Album: "sloth in motion EP", DurationMS: ms, AlbumTrack: &AlbumTrack{AlbumID: "deezer-708905921", AlbumTitle: "sloth in motion EP",
						AlbumArtist: "Quangou", Number: number, TrackTotal: 8, Disc: 1, DiscTotal: 1, Year: 2025}}}
			}
			titleTrack := ep("deezer-3220654741", "sloth in motion (feat. opio lindsey from: hieroglyphics)", 2, 129000,
				"staged", "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3")
			opener := ep("deezer-3220654731", "forest of no return", 1, 122000, "downloading", "")
			for _, job := range []DownloadJob{titleTrack, opener} {
				if err := f.service.RecordRequest(ctx, job, "user-owner"); err != nil {
					t.Fatal(err)
				}
			}
			f.service.AdvanceRequests(ctx, nil)
			if _, err := f.service.ProcessNewTracks(ctx); err != nil {
				t.Fatal(err)
			}
			f.service.AdvanceRequests(ctx, nil)
			if got, _, _ := f.service.Request(ctx, titleTrack.ID); got.State != RequestIdentifying || !strings.Contains(got.Message, "rest of the album") {
				t.Fatalf("decided while track 1 was still downloading: %+v", got)
			}

			if _, err := f.db.Exec(`UPDATE explo_requests SET state = ?, library_path = ? WHERE recording_id = ?`,
				RequestInLibrary, kept, opener.ID); err != nil {
				t.Fatal(err)
			}
			f.service.AdvanceRequests(ctx, nil)
			got, _, _ := f.service.Request(ctx, titleTrack.ID)
			if got.State != tc.want {
				t.Fatalf("request = %+v, want %s", got, tc.want)
			}
			if tc.want == RequestNeedsReview && !strings.Contains(got.Message, `track 1, "forest of no return"`) {
				t.Fatalf("review does not name the track it duplicates: %q", got.Message)
			}
		})
	}
}

func TestFingerprintSimilarity(t *testing.T) {
	frames := testFrames
	a := frames(1, 200)
	if got := fingerprintSimilarity(a, a); got != 1 {
		t.Fatalf("identical = %.3f", got)
	}
	// A rip with three seconds more lead-in is still the same recording.
	if got := fingerprintSimilarity(a, append(frames(9, 24), a...)); got != 1 {
		t.Fatalf("offset = %.3f", got)
	}
	if got := fingerprintSimilarity(a, frames(2, 200)); got > 0.6 {
		t.Fatalf("unrelated = %.3f", got)
	}
	if got := fingerprintSimilarity(a[:10], a[:10]); got != 0 {
		t.Fatalf("too short to say = %.3f", got)
	}
}

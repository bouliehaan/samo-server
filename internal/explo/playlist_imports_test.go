package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

const importSource = "PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5"

type importFixture struct {
	db        *sql.DB
	service   *Service
	playlists *playlists.Service
	owner     string
	installed []catalog.MusicPlaylist
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	if err := users.New(users.ServiceOptions{DB: db}).Bootstrap(ctx, users.BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-pass-123"}); err != nil {
		t.Fatal(err)
	}
	f := &importFixture{db: db, playlists: playlists.New(db)}
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&f.owner); err != nil {
		t.Fatal(err)
	}
	f.service = NewService(ServiceOptions{DB: db, Playlists: f.playlists, Logger: t.Logf,
		ApplyPlaylist: func(playlist catalog.MusicPlaylist) { f.installed = append(f.installed, playlist) }})
	f.track(t, "lib-rainbow", "Over the Rainbow", "Chet Baker", 209, false)
	// The same song in the explo silo: never the library's copy.
	f.track(t, "drop-valentine", "My Funny Valentine", "Chet Baker", 140, true)
	return f
}

func (f *importFixture) track(t *testing.T, id, title, artist string, seconds int, isExplo bool) {
	t.Helper()
	explo := 0
	if isExplo {
		explo = 1
	}
	if _, err := f.db.Exec(`INSERT INTO music_tracks (id, title, display_artist, duration_seconds, is_explo) VALUES (?, ?, ?, ?, ?)`,
		id, title, artist, seconds, explo); err != nil {
		t.Fatal(err)
	}
}

// land is a playlist track's request kept into the library.
func (f *importFixture) land(t *testing.T, recordingID, trackID, title string) {
	t.Helper()
	f.track(t, trackID, title, "Chet Baker", 100, false)
	if _, err := f.db.Exec(`UPDATE explo_requests SET state = ?, library_track_id = ? WHERE recording_id = ?`,
		RequestInLibrary, trackID, recordingID); err != nil {
		t.Fatal(err)
	}
}

func (f *importFixture) view(t *testing.T, playlistID string) PlaylistImport {
	t.Helper()
	view, ok, err := f.service.PlaylistImport(context.Background(), playlistID)
	if err != nil || !ok {
		t.Fatalf("import missing: ok=%v err=%v", ok, err)
	}
	return view
}

func states(view PlaylistImport) []string {
	out := make([]string, 0, len(view.Tracks))
	for _, track := range view.Tracks {
		out = append(out, track.State)
	}
	return out
}

func (f *importFixture) playlist(t *testing.T, id string) []string {
	t.Helper()
	playlist, err := f.playlists.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return playlist.TrackIDs
}

// exploPlaylistStub is Explo taking playlist tracks: it refuses the ids in
// refuse (404, as when the playlist no longer holds them) and stops taking
// any after full have been taken (429).
type exploPlaylistStub struct {
	mu     sync.Mutex
	added  []string
	refuse map[string]bool
	full   int
}

func (stub *exploPlaylistStub) remote(t *testing.T) *Remote {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		if r.Method != http.MethodPost || r.URL.Path != "/api/samo/downloads" || json.NewDecoder(r.Body).Decode(&input) != nil || input["playlist"] != importSource {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stub.mu.Lock()
		defer stub.mu.Unlock()
		switch {
		case stub.refuse[input["id"]]:
			w.WriteHeader(http.StatusNotFound)
			return
		case stub.full > 0 && len(stub.added) >= stub.full:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		stub.added = append(stub.added, input["id"])
		_ = json.NewEncoder(w).Encode(DownloadJob{ID: input["id"], State: "queued", Song: Song{ID: input["id"], Title: "From Explo", Artist: "Chet Baker"}})
	}))
	t.Cleanup(server.Close)
	return NewRemote(server.URL, "integration-token")
}

func chetListing() Playlist {
	return Playlist{ID: importSource, Title: "CHET BAKER BALLADS", Unavailable: 2, Tracks: []Song{
		{ID: "youtube-aaaaaaaaaaa", Source: "youtube", Title: "My Funny Valentine", Artist: "Chet Baker", Album: "Chet Baker Sings", DurationMS: 140000},
		{ID: "youtube-bbbbbbbbbbb", Source: "youtube", Title: "Over The Rainbow", Artist: "Chet Baker", Album: "Chet Is Back!", DurationMS: 210000},
		{ID: "youtube-ccccccccccc", Source: "youtube", Title: "Almost Blue", Artist: "Chet Baker", Album: "Let's Get Lost", DurationMS: 300000},
		// Same title as a library track, at a live take's length: not it.
		{ID: "youtube-ddddddddddd", Source: "youtube", Title: "Over the Rainbow", Artist: "Chet Baker", DurationMS: 400000},
		{ID: "youtube-eeeeeeeeeee", Source: "youtube", Title: "Gone From The Playlist", Artist: "Chet Baker", DurationMS: 100000},
	}}
}

func TestPlaylistImportKeepsYouTubeMusicOrderAsDownloadsLand(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	stub := &exploPlaylistStub{refuse: map[string]bool{"youtube-eeeeeeeeeee": true}}
	remote := stub.remote(t)

	playlist, err := f.service.ImportPlaylist(ctx, f.owner, "", "https://music.youtube.com/playlist?list="+importSource, false, chetListing())
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Name != "CHET BAKER BALLADS" || !slices.Equal(playlist.TrackIDs, []string{"lib-rainbow"}) {
		t.Fatalf("playlist = %q %v", playlist.Name, playlist.TrackIDs)
	}
	if len(f.installed) == 0 || !slices.Equal(f.installed[len(f.installed)-1].TrackIDs, []string{"lib-rainbow"}) {
		t.Fatal("imported playlist was not installed into the projection")
	}
	view := f.view(t, playlist.ID)
	if got := states(view); !slices.Equal(got, []string{"queued", "in-library", "queued", "queued", "queued"}) || view.Unavailable != 2 || view.Title != "CHET BAKER BALLADS" {
		t.Fatalf("after import: %v, unavailable %d", got, view.Unavailable)
	}

	f.service.AdvancePlaylistImports(ctx, remote)
	if !slices.Equal(stub.added, []string{"youtube-aaaaaaaaaaa", "youtube-ccccccccccc", "youtube-ddddddddddd"}) {
		t.Fatalf("handed to Explo: %v", stub.added)
	}
	view = f.view(t, playlist.ID)
	if got := states(view); !slices.Equal(got, []string{"downloading", "in-library", "downloading", "downloading", "unavailable"}) {
		t.Fatalf("after feeding: %v", got)
	}
	if request, ok, _ := f.service.Request(ctx, "youtube-aaaaaaaaaaa"); !ok || request.State != RequestDownloading {
		t.Fatalf("request = %+v", request)
	}
	var requestedBy string
	var forPlaylist bool
	_ = f.db.QueryRow(`SELECT requested_by, for_playlist FROM explo_requests WHERE recording_id = 'youtube-ccccccccccc'`).Scan(&requestedBy, &forPlaylist)
	if requestedBy != f.owner || !forPlaylist {
		t.Fatalf("requested by %q, for the playlist %v", requestedBy, forPlaylist)
	}

	// Track 3 finishes first: it goes after track 2, not at the end.
	f.land(t, "youtube-ccccccccccc", "kept-blue", "Almost Blue")
	f.service.AdvancePlaylistImports(ctx, remote)
	if got := f.playlist(t, playlist.ID); !slices.Equal(got, []string{"lib-rainbow", "kept-blue"}) {
		t.Fatalf("after track 3: %v", got)
	}
	// Track 1 has nothing before it: it goes before the first that is there.
	f.land(t, "youtube-aaaaaaaaaaa", "kept-valentine", "My Funny Valentine")
	f.service.AdvancePlaylistImports(ctx, remote)
	if got := f.playlist(t, playlist.ID); !slices.Equal(got, []string{"kept-valentine", "lib-rainbow", "kept-blue"}) {
		t.Fatalf("after track 1: %v", got)
	}
	if got := states(f.view(t, playlist.ID)); !slices.Equal(got, []string{"in-library", "in-library", "in-library", "downloading", "unavailable"}) {
		t.Fatalf("after landing: %v", got)
	}
	if len(stub.added) != 3 {
		t.Fatalf("asked Explo again: %v", stub.added)
	}

	// Someone adds their own song; importing again reads the playlist
	// afresh, keeps it, and asks again for what Explo refused.
	mine := "lib-mine"
	f.track(t, mine, "Mine", "Someone", 200, false)
	if _, err := f.playlists.Update(ctx, f.owner, playlist.ID, playlists.UpdateInput{TrackIDs: append(f.playlist(t, playlist.ID), mine)}); err != nil {
		t.Fatal(err)
	}
	again, err := f.service.ImportPlaylist(ctx, f.owner, "", "", false, chetListing())
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != playlist.ID || !slices.Equal(again.TrackIDs, []string{"kept-valentine", "lib-rainbow", "kept-blue", mine}) {
		t.Fatalf("re-import: %s %v", again.ID, again.TrackIDs)
	}
	if got := states(f.view(t, playlist.ID)); !slices.Equal(got, []string{"in-library", "in-library", "in-library", "downloading", "queued"}) {
		t.Fatalf("after re-import: %v", got)
	}
}

func TestPlaylistImportWaitsWhileExploIsFullAndRetriesFailures(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	stub := &exploPlaylistStub{full: 1}
	remote := stub.remote(t)
	playlist, err := f.service.ImportPlaylist(ctx, f.owner, "Ballads", "", false, chetListing())
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Name != "Ballads" {
		t.Fatalf("named %q", playlist.Name)
	}
	f.service.AdvancePlaylistImports(ctx, remote)
	if got := states(f.view(t, playlist.ID)); !slices.Equal(got, []string{"downloading", "in-library", "queued", "queued", "queued"}) {
		t.Fatalf("while full: %v", got)
	}
	stub.full = 0
	f.service.AdvancePlaylistImports(ctx, remote)
	if got := states(f.view(t, playlist.ID)); !slices.Equal(got, []string{"downloading", "in-library", "downloading", "downloading", "downloading"}) {
		t.Fatalf("with room: %v", got)
	}

	if _, err := f.db.Exec(`UPDATE explo_requests SET state = ?, message = 'YouTube (yt-dlp): found nothing for this song.' WHERE recording_id = 'youtube-ccccccccccc'`, RequestFailed); err != nil {
		t.Fatal(err)
	}
	view := f.view(t, playlist.ID)
	if view.Tracks[2].State != RequestFailed || !strings.Contains(view.Tracks[2].Message, "found nothing") {
		t.Fatalf("failed track: %+v", view.Tracks[2])
	}
	retried, err := f.service.RetryPlaylistImport(ctx, playlist.ID)
	if err != nil || retried != 1 {
		t.Fatalf("retried %d: %v", retried, err)
	}
	f.service.AdvancePlaylistImports(ctx, remote)
	if stub.added[len(stub.added)-1] != "youtube-ccccccccccc" {
		t.Fatalf("not asked again: %v", stub.added)
	}
	if request, _, _ := f.service.Request(ctx, "youtube-ccccccccccc"); request.State != RequestDownloading {
		t.Fatalf("retried request = %+v", request)
	}
}

func TestPlaylistImportIsForgottenWithItsPlaylist(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	remote := (&exploPlaylistStub{}).remote(t)
	playlist, err := f.service.ImportPlaylist(ctx, f.owner, "", "", false, chetListing())
	if err != nil {
		t.Fatal(err)
	}
	f.service.AdvancePlaylistImports(ctx, remote)
	if err := f.playlists.Delete(ctx, f.owner, playlist.ID); err != nil {
		t.Fatal(err)
	}
	f.land(t, "youtube-aaaaaaaaaaa", "kept-valentine", "My Funny Valentine")
	f.service.AdvancePlaylistImports(ctx, remote)
	if _, ok, err := f.service.PlaylistImport(ctx, playlist.ID); ok || err != nil {
		t.Fatalf("import outlived its playlist: ok=%v err=%v", ok, err)
	}
	// The song still landed in the library.
	if request, _, _ := f.service.Request(ctx, "youtube-aaaaaaaaaaa"); request.State != RequestInLibrary {
		t.Fatalf("request = %+v", request)
	}
}

func TestYouTubeIDsAndListingTrust(t *testing.T) {
	for id, want := range map[string]string{
		"youtube-lwRkYBRCd6U":  "lwRkYBRCd6U",
		"youtube-lwRkYBRCd6":   "",
		"youtube-lwRkYBRCd6U1": "",
		"youtube-lwRk/BRCd6U":  "",
		"deezer-123":           "",
	} {
		if got := YouTubeID(id); got != want {
			t.Errorf("YouTubeID(%q) = %q", id, got)
		}
	}
	if ValidCatalogID("youtube-lwRkYBRCd6U") || !ValidSongID("youtube-lwRkYBRCd6U") {
		t.Fatal("a playlist track is a song id, never an album id")
	}
	// Only a song YouTube Music lists as one (Explo gives it its album) may
	// be named by the listing.
	if source, _ := listingSource("youtube-lwRkYBRCd6U", "Chet Is Back!"); source != "youtube-music" {
		t.Fatalf("art track source = %q", source)
	}
	if source, _ := listingSource("youtube-lwRkYBRCd6U", ""); source != "" {
		t.Fatalf("a video's listing was trusted: %q", source)
	}
	if source, _ := listingSource("deezer-123", ""); source != "deezer" {
		t.Fatalf("deezer source = %q", source)
	}
	if source, _ := listingSource(requestedRecording, "Album"); source != "" {
		t.Fatalf("a MusicBrainz request named by its listing: %q", source)
	}
}

// A song from a YouTube Music playlist that AcoustID cannot place is named by
// the playlist's listing, as a Deezer track is — the label's own audio under
// the label's names — and kept like any other request.
func TestYouTubeMusicSongIsIdentifiedFromItsListing(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, `{"status": "ok", "results": []}`)
	job := DownloadJob{ID: "youtube-lwRkYBRCd6U", State: "staged", File: "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3",
		Song: Song{ID: "youtube-lwRkYBRCd6U", Source: "youtube", Title: "You Don't Know What Love Is", Artist: "Chet Baker",
			Album: "Chet Baker Sings", DurationMS: 399000}}
	if err := f.service.RecordRequest(ctx, job, "user-owner"); err != nil {
		t.Fatal(err)
	}
	remote := exploStub(t, &job)
	f.service.AdvanceRequests(ctx, remote)
	candidates, err := f.service.findCandidateTracks(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].musicBrainzRecordingID != "" {
		t.Fatalf("a YouTube id offered as MusicBrainz evidence: %+v %v", candidates, err)
	}
	if result, err := f.service.ProcessNewTracks(ctx); err != nil || result.Unmatched != 1 {
		t.Fatalf("identify pass: %+v %v", result, err)
	}
	f.service.AdvanceRequests(ctx, remote)
	var status, album, recording string
	if err := f.db.QueryRow(`SELECT status, matched_album, musicbrainz_recording_id FROM explo_tracks WHERE track_id = 'track-drop'`).
		Scan(&status, &album, &recording); err != nil {
		t.Fatal(err)
	}
	if status != "matched-fallback" || album != "Chet Baker Sings" || recording != "" {
		t.Fatalf("ledger = %s %q rec=%q", status, album, recording)
	}
	if got, _, _ := f.service.Request(ctx, job.ID); got.State != RequestIdentifying || !strings.Contains(got.Message, "YouTube Music") {
		t.Fatalf("request after adoption = %+v", got)
	}
	f.settleCover(t)
	f.service.AdvanceRequests(ctx, remote)
	if got, _, _ := f.service.Request(ctx, job.ID); got.State != RequestInLibrary {
		t.Fatalf("request after keep = %+v", got)
	}
}

// A music video or anyone's upload has no album from Explo: its names are
// the uploader's, so it waits for identification, never for its listing.
func TestYouTubeVideoIsNotNamedByItsListing(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, `{"status": "ok", "results": []}`)
	job := DownloadJob{ID: "youtube-jrxI_euTX4A", State: "staged", File: "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3",
		Song: Song{ID: "youtube-jrxI_euTX4A", Source: "youtube", Title: "You Don't Know What Love Is", Artist: "Chet Baker", DurationMS: 399000}}
	if err := f.service.RecordRequest(ctx, job, "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if _, err := f.service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	var status string
	_ = f.db.QueryRow(`SELECT status FROM explo_tracks WHERE track_id = 'track-drop'`).Scan(&status)
	got, _, _ := f.service.Request(ctx, job.ID)
	if status == "matched-fallback" || got.State != RequestIdentifying || strings.Contains(got.Message, "listing") {
		t.Fatalf("video adopted its listing: ledger %q, request %+v", status, got)
	}
}

// YouTube Music's "Live" is MusicBrainz's "Lïve", with a typographic
// apostrophe in the title: the same song, not one for review.
func TestRequestMatchesFoldsAccents(t *testing.T) {
	asked := songRequestRow{recordingID: "youtube-uSvtFd74Nlo", title: "The Dolphin's Cry", artist: "Live"}
	if !requestMatches(asked, "a-recording", "The Dolphin’s Cry", "Lïve") {
		t.Fatal("Live / Lïve taken for different songs")
	}
	if requestMatches(asked, "a-recording", "Lightning Crashes", "Lïve") {
		t.Fatal("a different song by the band taken for the one asked for")
	}
}

// A song that stopped for review is kept by hand as the request: settled,
// recorded as the copy Keep made, and so ready to join a waiting playlist.
func TestKeepingAReviewedRequestSettlesIt(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, `{"status": "ok", "results": [{"id": "acoustid-2", "score": 0.95, "recordings": [{
		"id": "11111111-2222-3333-4444-555555555555", "title": "My Funny Valentine", "artists": [{"name": "Miles Davis"}],
		"releasegroups": [{"id": "rg-cookin", "title": "Cookin'", "type": "Album"}]
	}]}]}`)
	staged := requestedJob("staged")
	staged.File = "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3"
	if err := f.service.RecordRequest(ctx, staged, "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, nil)
	if _, err := f.service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	f.settleCover(t)
	f.service.AdvanceRequests(ctx, nil)
	if got := f.request(t); got.State != RequestNeedsReview {
		t.Fatalf("request = %+v", got)
	}
	results, err := f.service.Keep(ctx, []string{"track-drop"})
	if err != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("keep: %+v %v", results, err)
	}
	got := f.request(t)
	var copied bool
	_ = f.db.QueryRow(`SELECT library_copy FROM explo_requests WHERE recording_id = ?`, requestedRecording).Scan(&copied)
	if got.State != RequestInLibrary || got.LibraryTrackID == "" || got.LibraryTrackID != results[0].LibraryTrackID || !copied {
		t.Fatalf("kept request = %+v copied=%v", got, copied)
	}
}

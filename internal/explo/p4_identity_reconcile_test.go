package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

// The 2026-09-15 Creep row, as the live ledger held it: the file is tagged
// Radiohead / Creep / Pablo Honey and carries the real recording id, and
// identification had filed it under the one-source mis-tag AcoustID listed
// first. The overrides and the ledger say Klangsberg; the file says Radiohead.
const (
	creepRealRecording = "70595637-9310-45f2-a266-58f8de4874a7"
	creepMisTag        = "1387bfbc-6a6e-4ed5-9e60-e990ed6d3cab"
)

// creepAcoustIDBody is the stub response for the Creep fingerprint: the
// mis-tag first with one source, the real recording second with thousands —
// the shape that produced the bug.
const creepAcoustIDBody = `{"status": "ok", "results": [{"id": "cfe630f8", "score": 0.9999, "recordings": [
	{"id": "` + creepMisTag + `", "sources": 1, "duration": 238.0, "title": "Radiohead - Creep", "artists": [{"name": "Klangsberg"}],
	 "releasegroups": [{"id": "33677a84", "title": "Klangsberg"}]},
	{"id": "` + creepRealRecording + `", "sources": 9972, "duration": 237.0, "title": "Creep", "artists": [{"name": "Radiohead"}],
	 "releasegroups": [{"id": "rg-pablo-honey", "title": "Pablo Honey", "type": "Album"}]}
]}]}`

// junkAcoustIDBody names a recording that will never agree with the file
// tagged "Someone Else / Other Song": a row that stays contradicted.
const junkAcoustIDBody = `{"status": "ok", "results": [{"id": "junk", "score": 0.95, "recordings": [
	{"id": "mb-real", "sources": 5, "duration": 200.0, "title": "Real Song", "artists": [{"name": "Real Band"}],
	 "releasegroups": [{"id": "rg-real", "title": "Real Album", "type": "Album"}]}
]}]}`

// countingAcoustIDStub answers by fingerprint and counts the lookups made
// for each, so a test can tell a re-check from no re-check.
func countingAcoustIDStub(t *testing.T, byFingerprint map[string]string) (*httptest.Server, func(fingerprint string) int) {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fingerprint := r.URL.Query().Get("fingerprint")
		mu.Lock()
		counts[fingerprint]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		body, ok := byFingerprint[fingerprint]
		if !ok {
			_, _ = w.Write([]byte(`{"status": "ok", "results": []}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, func(fingerprint string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[fingerprint]
	}
}

// seedMisidentifiedCreep plants the Creep file, its wrong ledger row and the
// wrong overrides (with a cover fetched for the wrong record), plus a second
// drop whose tags nothing will ever agree with.
func seedMisidentifiedCreep(t *testing.T, ctx context.Context, db *sql.DB, exploDir string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, display_artist, track_count, duration_seconds)
		VALUES ('album-creep', 'Pablo Honey', 'Radiohead', 1, 235),
		       ('album-junk', 'Other Album', 'Someone Else', 1, 200);

		INSERT INTO music_tracks (id, title, display_artist, album_id, album_title, duration_seconds, external_ids_json)
		VALUES ('track-creep', 'Creep', 'Radiohead', 'album-creep', 'Pablo Honey', 235,
		        '{"musicBrainzRecordingId":"`+creepRealRecording+`","musicBrainzTrackId":"52509c36-eb0a-3bb0-953d-3e4abf3a1a36"}'),
		       ('track-junk', 'Other Song', 'Someone Else', 'album-junk', 'Other Album', 200, '');

		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-creep', 'lib-1', 'track-creep', '`+exploDir+`/Radiohead - Pablo Honey - 02 - Creep.flac', 'explo/Radiohead - Pablo Honey - 02 - Creep.flac', 'Radiohead - Pablo Honey - 02 - Creep.flac', 235),
		       ('file-junk', 'lib-1', 'track-junk', '`+exploDir+`/Someone Else - Other Song.flac', 'explo/Someone Else - Other Song.flac', 'Someone Else - Other Song.flac', 200);

		INSERT INTO explo_tracks (track_id, status, acoustid_id, musicbrainz_recording_id, musicbrainz_release_group_id,
		                          matched_title, matched_artist, matched_album, score, cover_status, processed_at)
		VALUES ('track-creep', 'matched', 'cfe630f8', '`+creepMisTag+`', '33677a84',
		        'Radiohead - Creep', 'Klangsberg', 'Klangsberg', 0.9999, 'done', '2026-09-15T06:46:22Z');
	`); err != nil {
		t.Fatal(err)
	}
	trackPatch := catalog.MetadataOverridePatch{
		"title":         json.RawMessage(`"Radiohead - Creep"`),
		"displayArtist": json.RawMessage(`"Klangsberg"`),
		"externalIds":   json.RawMessage(`{"musicBrainzRecordingId":"` + creepMisTag + `"}`),
		"cover":         json.RawMessage(`[{"id":"cover_klangsberg","path":"/var/lib/samo/covers/cover_klangsberg.jpg","mimeType":"image/jpeg"}]`),
	}
	if err := catalogstore.UpsertMetadataOverride(ctx, db, string(metadata.ApplyTargetMusicTrack), "track-creep", trackPatch); err != nil {
		t.Fatal(err)
	}
	albumPatch := catalog.MetadataOverridePatch{
		"title":         json.RawMessage(`"Klangsberg"`),
		"displayArtist": json.RawMessage(`"Klangsberg"`),
	}
	if err := catalogstore.UpsertMetadataOverride(ctx, db, string(metadata.ApplyTargetMusicAlbum), "album-creep", albumPatch); err != nil {
		t.Fatal(err)
	}
}

func newCreepService(t *testing.T, db *sql.DB, exploDir string, stubURL string, client *http.Client, trackByID func(string) (catalog.MusicTrack, error)) *Service {
	t.Helper()
	fpcalc := fakeFpcalc(t, `
case "$2" in
  *"Creep.flac") echo '{"duration": 235.0, "fingerprint": "FP-CREEP"}' ;;
  *"Other Song.flac") echo '{"duration": 200.0, "fingerprint": "FP-JUNK"}' ;;
  *) echo '{"duration": 100.0, "fingerprint": "FP-NOTHING"}' ;;
esac`)
	service := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "test-key",
		FpcalcPath:     fpcalc,
		HTTPClient:     client,
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:      playlists.New(db),
		ReloadCatalog:  func(context.Context) error { return nil },
		TrackByID:      trackByID,
	})
	orig := acoustidLookupURL
	acoustidLookupURL = stubURL
	t.Cleanup(func() { acoustidLookupURL = orig })
	return service
}

type ledgerState struct {
	status, recording, releaseGroup, title, artist, album, coverStatus string
}

func readLedger(t *testing.T, ctx context.Context, db *sql.DB, trackID string) ledgerState {
	t.Helper()
	var row ledgerState
	if err := db.QueryRowContext(ctx, `
		SELECT status, musicbrainz_recording_id, musicbrainz_release_group_id, matched_title, matched_artist, matched_album, cover_status
		FROM explo_tracks WHERE track_id = ?`, trackID).Scan(
		&row.status, &row.recording, &row.releaseGroup, &row.title, &row.artist, &row.album, &row.coverStatus); err != nil {
		t.Fatal(err)
	}
	return row
}

func readOverride(t *testing.T, ctx context.Context, db *sql.DB, kind, id string) catalog.MetadataOverridePatch {
	t.Helper()
	record, err := catalogstore.GetMetadataOverride(ctx, db, kind, id)
	if err != nil {
		t.Fatalf("override %s/%s: %v", kind, id, err)
	}
	return record.Fields
}

// The first pass after boot re-identifies the row whose ledger identity
// contradicts its file, and — with the evidence now counted — moves it to
// the recording the file names: overrides, ledger, and the cover fetched for
// the wrong record all follow.
func TestIdentityCheckMovesAMisidentifiedDropToItsOwnRecording(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	seedMisidentifiedCreep(t, ctx, db, exploDir)
	stub, lookups := countingAcoustIDStub(t, map[string]string{"FP-CREEP": creepAcoustIDBody, "FP-JUNK": junkAcoustIDBody})
	service := newCreepService(t, db, exploDir, stub.URL, stub.Client(), nil)

	if _, err := service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}

	ledger := readLedger(t, ctx, db, "track-creep")
	if ledger.recording != creepRealRecording || ledger.artist != "Radiohead" || ledger.title != "Creep" || ledger.album != "Pablo Honey" {
		t.Fatalf("ledger after the check = %+v, want Radiohead / Creep [Pablo Honey] on %s", ledger, creepRealRecording)
	}
	if ledger.releaseGroup != "rg-pablo-honey" || ledger.status != "matched" {
		t.Fatalf("ledger release group/status = %q/%q", ledger.releaseGroup, ledger.status)
	}
	if ledger.coverStatus != "" {
		t.Fatalf("cover_status = %q; the cover state must be re-opened so art is fetched for the right record", ledger.coverStatus)
	}

	track := readOverride(t, ctx, db, string(metadata.ApplyTargetMusicTrack), "track-creep")
	if title, _ := catalog.DecodePatchString(track, "title"); title != "Creep" {
		t.Fatalf("track override title = %q", title)
	}
	if artist, _ := catalog.DecodePatchString(track, "displayArtist"); artist != "Radiohead" {
		t.Fatalf("track override displayArtist = %q", artist)
	}
	if _, hasCover := track["cover"]; hasCover {
		t.Fatal("the cover fetched for the wrong record is still on the track; the cover engine would keep it forever")
	}
	if !strings.Contains(string(track["externalIds"]), creepRealRecording) || strings.Contains(string(track["externalIds"]), creepMisTag) {
		t.Fatalf("track override externalIds = %s", track["externalIds"])
	}
	album := readOverride(t, ctx, db, string(metadata.ApplyTargetMusicAlbum), "album-creep")
	if title, _ := catalog.DecodePatchString(album, "title"); title != "Pablo Honey" {
		t.Fatalf("album override title = %q", title)
	}
	if artist, _ := catalog.DecodePatchString(album, "displayArtist"); artist != "Radiohead" {
		t.Fatalf("album override displayArtist = %q", artist)
	}

	// The card and Keep read the same projection; after the check both see
	// Radiohead / Creep.
	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range seed.MusicTracks {
		if item.ID == "track-creep" && (item.Title != "Creep" || item.DisplayArtist != "Radiohead") {
			t.Fatalf("projection shows %s / %s", item.DisplayArtist, item.Title)
		}
	}

	// One lookup for the re-check; the row now agrees with its file, so a
	// later check has nothing to ask about it.
	if n := lookups("FP-CREEP"); n != 1 {
		t.Fatalf("Creep was looked up %d time(s) in the first pass, want 1", n)
	}
	service.identityCheckDue.Store(true)
	if _, err := service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	if n := lookups("FP-CREEP"); n != 1 {
		t.Fatalf("a row that agrees with its file was looked up again (%d total)", n)
	}
}

// The check runs once per boot and again when the operator asks for a
// reprocess: a row that stays contradicted (nothing AcoustID lists agrees
// with its tags) is looked up on those occasions and on no ordinary pass.
func TestIdentityCheckRunsOncePerBootAndOnReprocess(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	seedMisidentifiedCreep(t, ctx, db, exploDir)
	stub, lookups := countingAcoustIDStub(t, map[string]string{"FP-CREEP": creepAcoustIDBody, "FP-JUNK": junkAcoustIDBody})
	service := newCreepService(t, db, exploDir, stub.URL, stub.Client(), nil)

	// Pass 1: track-junk is new, identified in the loop (one lookup); the
	// check ran before the loop, so it was not looked up a second time.
	if _, err := service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	if n := lookups("FP-JUNK"); n != 1 {
		t.Fatalf("pass 1 looked up the new drop %d time(s), want 1", n)
	}
	if service.identityCheckDue.Load() {
		t.Fatal("the check must disarm itself after running")
	}
	junk := readLedger(t, ctx, db, "track-junk")
	if junk.artist != "Real Band" || junk.status != "matched" {
		t.Fatalf("junk ledger = %+v", junk)
	}

	// Pass 2: nothing due, the contradicted row is left alone.
	if _, err := service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	if n := lookups("FP-JUNK"); n != 1 {
		t.Fatalf("an ordinary pass re-checked the contradicted row (%d lookups)", n)
	}

	// Reprocess re-arms the check; pass 3 re-checks the contradicted row,
	// which comes out the same and changes nothing.
	if _, err := service.Reprocess(ctx); err != nil {
		t.Fatal(err)
	}
	if !service.identityCheckDue.Load() {
		t.Fatal("Reprocess must re-arm the identity check")
	}
	if _, err := service.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	if n := lookups("FP-JUNK"); n != 2 {
		t.Fatalf("after Reprocess the contradicted row was looked up %d time(s) in total, want 2", n)
	}
	if again := readLedger(t, ctx, db, "track-junk"); again.artist != "Real Band" || again.recording != "mb-real" {
		t.Fatalf("an unchanged re-identification altered the ledger: %+v", again)
	}
}

// A dry run makes the lookups and reports what would move, and writes
// nothing anywhere.
func TestIdentityCheckDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	seedMisidentifiedCreep(t, ctx, db, exploDir)
	stub, _ := countingAcoustIDStub(t, map[string]string{"FP-CREEP": creepAcoustIDBody})
	service := newCreepService(t, db, exploDir, stub.URL, stub.Client(), nil)

	report, err := service.reconcileIdentities(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Checked != 1 || report.Changed != 1 || len(report.Changes) != 1 {
		t.Fatalf("report = %+v", report)
	}
	change := report.Changes[0]
	if change.Applied || change.TrackID != "track-creep" || change.Before.Artist != "Klangsberg" || change.After.Artist != "Radiohead" || change.After.MusicBrainzRecordingID != creepRealRecording {
		t.Fatalf("change = %+v", change)
	}
	if ledger := readLedger(t, ctx, db, "track-creep"); ledger.artist != "Klangsberg" || ledger.coverStatus != "done" {
		t.Fatalf("dry run wrote to the ledger: %+v", ledger)
	}
	track := readOverride(t, ctx, db, string(metadata.ApplyTargetMusicTrack), "track-creep")
	if artist, _ := catalog.DecodePatchString(track, "displayArtist"); artist != "Klangsberg" {
		t.Fatalf("dry run wrote the track override: %q", artist)
	}
	if _, hasCover := track["cover"]; !hasCover {
		t.Fatal("dry run removed the cover")
	}
}

// When the catalog already shows the track under a name the pipeline never
// wrote, somebody fixed it by hand. The correction stands: only the ledger
// is brought up to date, so Keep files under the right record without the
// override being rewritten under the listener.
func TestIdentityCheckKeepsAManualCorrection(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	seedMisidentifiedCreep(t, ctx, db, exploDir)
	stub, _ := countingAcoustIDStub(t, map[string]string{"FP-CREEP": creepAcoustIDBody})
	byHand := func(id string) (catalog.MusicTrack, error) {
		if id == "track-creep" {
			return catalog.MusicTrack{ID: id, Title: "Creep (Jacob's edit)", DisplayArtist: "Radiohead"}, nil
		}
		return catalog.MusicTrack{}, catalog.ErrNotFound
	}
	service := newCreepService(t, db, exploDir, stub.URL, stub.Client(), byHand)

	report, err := service.reconcileIdentities(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed != 1 || report.Changes[0].Applied || !strings.Contains(report.Changes[0].Note, "manual") {
		t.Fatalf("report = %+v", report)
	}
	if ledger := readLedger(t, ctx, db, "track-creep"); ledger.recording != creepRealRecording || ledger.album != "Pablo Honey" {
		t.Fatalf("ledger was not updated: %+v", ledger)
	}
	track := readOverride(t, ctx, db, string(metadata.ApplyTargetMusicTrack), "track-creep")
	if title, _ := catalog.DecodePatchString(track, "title"); title != "Radiohead - Creep" {
		t.Fatalf("the override was rewritten over a manual correction: title = %q", title)
	}
}

// A row that AcoustID no longer recognises at all keeps what it has: a miss
// today says nothing about whether the match was wrong.
func TestIdentityCheckKeepsARowThatNoLongerIdentifies(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	seedMisidentifiedCreep(t, ctx, db, exploDir)
	stub, _ := countingAcoustIDStub(t, map[string]string{}) // nothing matches
	service := newCreepService(t, db, exploDir, stub.URL, stub.Client(), nil)

	report, err := service.reconcileIdentities(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Checked != 1 || report.Changed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if ledger := readLedger(t, ctx, db, "track-creep"); ledger.artist != "Klangsberg" || ledger.status != "matched" {
		t.Fatalf("a miss downgraded the row: %+v", ledger)
	}
}

// What counts as contradicted: a name the file disagrees with, or a different
// recording than the one the file carries. A file that says nothing about
// itself never contradicts anything.
func TestIdentityContradictedFollowsTheFilesEvidence(t *testing.T) {
	klangsberg := identifiedTrack{Title: "Radiohead - Creep", Artist: "Klangsberg", MusicBrainzRecordingID: creepMisTag}
	radiohead := identifiedTrack{Title: "Creep", Artist: "Radiohead", MusicBrainzRecordingID: creepRealRecording}
	cases := []struct {
		name      string
		candidate candidateTrack
		match     identifiedTrack
		want      bool
	}{
		{"tags disagree", candidateTrack{title: "Creep", artist: "Radiohead", path: "/d/x.flac"}, klangsberg, true},
		{"tags agree, same recording", candidateTrack{title: "Creep", artist: "Radiohead", path: "/d/x.flac", musicBrainzRecordingID: creepRealRecording}, radiohead, false},
		{"tags agree, other recording", candidateTrack{title: "Creep", artist: "Radiohead", path: "/d/x.flac", musicBrainzRecordingID: "another-mbid"}, radiohead, true},
		{"filename disagrees", candidateTrack{path: "/d/Radiohead - Creep.flac"}, klangsberg, true},
		{"nothing known", candidateTrack{path: "/d/01.flac"}, klangsberg, false},
		{"guest credit still agrees", candidateTrack{title: "Runaway (feat. Pusha T)", artist: "Kanye West", path: "/d/x.flac"}, identifiedTrack{Title: "Runaway", Artist: "Kanye West, Pusha T"}, false},
	}
	for _, tc := range cases {
		row := ledgerIdentity{candidate: tc.candidate, match: tc.match}
		if got := identityContradicted(row); got != tc.want {
			t.Errorf("%s: contradicted = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The album tag is evidence too: a ledger row that puts a track on a sampler
// while the file names its record is contradicted (Keep would file it under
// the sampler), and the drop folder's name — the scanner's stand-in for no
// album tag — is not evidence of anything.
func TestIdentityContradictedByTheAlbumTag(t *testing.T) {
	onSampler := identifiedTrack{Title: "Float On", Artist: "Modest Mouse", Album: "Spex CD #39"}
	if !identityContradicted(ledgerIdentity{candidate: candidateTrack{title: "Float On", artist: "Modest Mouse", album: "Good News for People Who Love Bad News", path: "/d/x.flac"}, match: onSampler}) {
		t.Fatal("a sampler in the ledger against the record in the tag must be contradicted")
	}
	if identityContradicted(ledgerIdentity{candidate: candidateTrack{title: "Float On", artist: "Modest Mouse", album: "", path: "/d/x.flac"}, match: onSampler}) {
		t.Fatal("no album tag cannot contradict the album")
	}
	service := &Service{dirs: []string{"/mnt/media/Music/explo/Weekly-Exploration"}, logger: func(string, ...any) {}}
	if !service.isDropFolderName("Weekly-Exploration") {
		t.Fatal("fixture: the drop folder's name must be recognised")
	}
}

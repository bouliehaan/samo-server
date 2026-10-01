package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

const requestedRecording = "8c3b9a3e-5a2b-4d43-9b1c-2f0e6a1d7c11"

// requestFixture is a library with one requested download staged in its explo
// folder, the way Search for new leaves it: a YouTube file named after the
// song, tagged with what was asked for, not yet identified.
type requestFixture struct {
	db      *sql.DB
	service *Service
	root    string
	drop    string
	source  string
}

func newRequestFixture(t *testing.T, acoustidBody string) requestFixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	if err := users.New(users.ServiceOptions{DB: db}).Bootstrap(ctx, users.BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-pass-123"}); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	drop := filepath.Join(root, "explo", "Weekly-Exploration")
	if err := os.MkdirAll(drop, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(drop, "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3")
	if err := os.WriteFile(source, []byte("ID3 not really audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO libraries (id, name, kind, path) VALUES ('lib-1', 'Music', 'music', ?)`, []any{root}},
		{`INSERT INTO music_albums (id, title, track_count) VALUES ('album-drop', 'The Chronicle of Jazz', 1)`, nil},
		{`INSERT INTO music_tracks (id, title, display_artist, album_id, album_title, duration_seconds)
		  VALUES ('track-drop', 'You Don''t Know What Love Is', 'Chet Baker', 'album-drop', 'The Chronicle of Jazz', 400)`, nil},
		{`INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		  VALUES ('file-drop', 'lib-1', 'track-drop', ?, 'explo/Weekly-Exploration/You_Don_t_Know_What_Love_Is-Chet_Baker.mp3',
		          'You_Don_t_Know_What_Love_Is-Chet_Baker.mp3', 400)`, []any{source}},
	} {
		if _, err := db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}

	acoustid := acoustidStub(t, map[string]string{"FP-REQUESTED": acoustidBody})
	t.Cleanup(acoustid.Close)
	acoustidLookupURL = acoustid.URL
	t.Cleanup(func() { acoustidLookupURL = "https://api.acoustid.org/v2/lookup" })

	// ffmpeg that copies its input to its output: Keep's remux, minus the
	// tags, which TestRemuxArgsWritesEffectiveTags covers.
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nfor out; do :; done\ncp \"$6\" \"$out\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{drop},
		AcoustIDAPIKey: "test-key",
		FpcalcPath:     fakeFpcalc(t, `echo '{"duration": 400.0, "fingerprint": "FP-REQUESTED"}'`),
		HTTPClient:     acoustid.Client(),
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:      playlists.New(db),
		FFmpegPath:     ffmpeg,
		// The projection as it stands after identification applied its match.
		TrackByID: func(id string) (catalog.MusicTrack, error) {
			var track catalog.MusicTrack
			var path string
			err := db.QueryRowContext(ctx, `
				SELECT mt.id, COALESCE(et.matched_title, mt.title), COALESCE(et.matched_artist, mt.display_artist), mf.path
				FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
				LEFT JOIN explo_tracks et ON et.track_id = mt.id
				WHERE mt.id = ?`, id).Scan(&track.ID, &track.Title, &track.DisplayArtist, &path)
			track.AudioFiles = []catalog.AudioFile{{Path: path}}
			return track, err
		},
		// The scanner, cataloguing whatever Keep wrote.
		ScanSubpaths: func(ctx context.Context, paths []string) error {
			for _, dir := range paths {
				entries, err := os.ReadDir(dir)
				if err != nil {
					return err
				}
				for i, entry := range entries {
					path := filepath.Join(dir, entry.Name())
					id := "kept-" + string(rune('a'+i))
					if _, err := db.ExecContext(ctx, `
						INSERT INTO music_albums (id, title, track_count) VALUES ('album-kept', 'Chet Baker Sings', 1)
						ON CONFLICT (id) DO NOTHING`); err != nil {
						return err
					}
					if _, err := db.ExecContext(ctx, `INSERT INTO music_tracks (id, title, album_id) VALUES (?, ?, 'album-kept')`,
						id, entry.Name()); err != nil {
						return err
					}
					if _, err := db.ExecContext(ctx, `
						INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name)
						VALUES (?, 'lib-1', ?, ?, ?, ?)`, "file-"+id, id, path, entry.Name(), entry.Name()); err != nil {
						return err
					}
				}
			}
			return nil
		},
	})
	return requestFixture{db: db, service: service, root: root, drop: drop, source: source}
}

// exploStub is Explo's song-search API with one job in it, or none.
func exploStub(t *testing.T, job *DownloadJob) *Remote {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if job == nil || r.URL.Path != "/api/samo/downloads/"+job.ID {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"download no longer tracked"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
	}))
	t.Cleanup(server.Close)
	return NewRemote(server.URL, "integration-token")
}

func requestedJob(state string) DownloadJob {
	return DownloadJob{
		ID:    requestedRecording,
		State: state,
		Song:  Song{ID: requestedRecording, Title: "You Don't Know What Love Is", Artist: "Chet Baker", Album: "The Chronicle of Jazz", DurationMS: 400000},
	}
}

func (f requestFixture) request(t *testing.T) SongRequest {
	t.Helper()
	request, ok, err := f.service.Request(context.Background(), requestedRecording)
	if err != nil || !ok {
		t.Fatalf("request missing: ok=%v err=%v", ok, err)
	}
	return request
}

// settleCover is the cover pass having found real art for the drop.
func (f requestFixture) settleCover(t *testing.T) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE explo_tracks SET cover_status = ? WHERE track_id = 'track-drop'`, coverStatusDone); err != nil {
		t.Fatal(err)
	}
}

const acoustidRequestedSong = `{"status": "ok", "results": [{"id": "acoustid-1", "score": 0.95, "recordings": [{
	"id": "` + requestedRecording + `", "title": "You Don’t Know What Love Is", "artists": [{"name": "Chet Baker"}],
	"releasegroups": [{"id": "rg-sings", "title": "Chet Baker Sings", "type": "Album"}]
}]}]}`

// The whole of Search for new after the download: staged, found, identified
// as the song that was asked for, and kept into the library with the album
// identification chose — not left in the explo silo, and not filed under the
// compilation the search result happened to name.
func TestRequestedSongIsIdentifiedAndKeptIntoTheLibrary(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)

	if err := f.service.RecordRequest(ctx, requestedJob("queued"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	if got := f.request(t).State; got != RequestDownloading {
		t.Fatalf("new request state = %q", got)
	}

	staged := requestedJob("staged")
	staged.File = "You_Don_t_Know_What_Love_Is-Chet_Baker.mp3"
	remote := exploStub(t, &staged)

	// Before the identify pass: the staged file is found and linked.
	f.service.AdvanceRequests(ctx, remote)
	var linked string
	if err := f.db.QueryRow(`SELECT track_id FROM explo_requests WHERE recording_id = ?`, requestedRecording).Scan(&linked); err != nil || linked != "track-drop" {
		t.Fatalf("staged file not linked to its track: %q %v", linked, err)
	}
	if got := f.request(t); got.State != RequestIdentifying {
		t.Fatalf("staged request = %+v", got)
	}

	if result, err := f.service.ProcessNewTracks(ctx); err != nil || result.Matched != 1 {
		t.Fatalf("identify pass: %+v %v", result, err)
	}

	// Identified, but the cover pass has not been yet: it waits for the art.
	f.service.AdvanceRequests(ctx, remote)
	if got := f.request(t); got.State != RequestIdentifying || !strings.Contains(got.Message, "cover") {
		t.Fatalf("kept before the cover pass: %+v", got)
	}

	f.settleCover(t)
	f.service.AdvanceRequests(ctx, remote)
	got := f.request(t)
	if got.State != RequestInLibrary || got.LibraryTrackID == "" || got.LibraryAlbumID != "album-kept" {
		t.Fatalf("request after keep = %+v", got)
	}
	kept := filepath.Join(f.root, "Chet Baker", "Chet Baker Sings", "You Don’t Know What Love Is.mp3")
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("no library copy at %s: %v", kept, err)
	}

	// Settled requests are not touched again.
	f.service.AdvanceRequests(ctx, remote)
	if again := f.request(t); again != got {
		t.Fatalf("settled request moved: %+v", again)
	}
}

// A YouTube upload can be the wrong song entirely. Identification files the
// audio under what it really is; a request must not carry that into the
// library unasked.
func TestRequestedSongThatIsAnotherSongWaitsForReview(t *testing.T) {
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

	got := f.request(t)
	if got.State != RequestNeedsReview || !strings.Contains(got.Message, "My Funny Valentine") {
		t.Fatalf("wrong song kept or not flagged: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Miles Davis")); !os.IsNotExist(err) {
		t.Fatalf("a song nobody asked for reached the library: %v", err)
	}
}

// Explo keeps its jobs in memory. A restart between staging and samo's next
// look loses the job, not the file, and the file says what it is.
func TestRequestedSongIsFoundAfterExploForgetsTheJob(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	if _, err := f.db.Exec(`UPDATE music_tracks SET external_ids_json = ? WHERE id = 'track-drop'`,
		`{"musicBrainzRecordingId":"`+requestedRecording+`"}`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RecordRequest(ctx, requestedJob("downloading"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	f.service.AdvanceRequests(ctx, exploStub(t, nil))
	if got := f.request(t); got.State != RequestIdentifying {
		t.Fatalf("forgotten job's staged file not found: %+v", got)
	}
}

func TestRequestFailsWhenExploFailsTheDownload(t *testing.T) {
	ctx := context.Background()
	f := newRequestFixture(t, acoustidRequestedSong)
	if err := f.service.RecordRequest(ctx, requestedJob("queued"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	failed := requestedJob("failed")
	failed.Message = "YouTube (yt-dlp): no matching downloadable audio passed the title, artist and quality checks."
	f.service.AdvanceRequests(ctx, exploStub(t, &failed))
	if got := f.request(t); got.State != RequestFailed || got.Message != failed.Message {
		t.Fatalf("failed download = %+v", got)
	}

	// Asking again starts over.
	if err := f.service.RecordRequest(ctx, requestedJob("queued"), "user-owner"); err != nil {
		t.Fatal(err)
	}
	if got := f.request(t); got.State != RequestDownloading || got.Message != "" {
		t.Fatalf("retry did not start over: %+v", got)
	}
}

func TestCleanStagedFileStaysInsideTheDropFolder(t *testing.T) {
	for in, want := range map[string]string{
		"song.mp3":            "song.mp3",
		"Artist/Album/03.mp3": "Artist/Album/03.mp3",
		`Artist\Song.mp3`:     "Artist/Song.mp3",
		"/abs/song.mp3":       "abs/song.mp3",
		"../escape.mp3":       "",
		"a/../../escape.mp3":  "",
		"..":                  "",
		"":                    "",
	} {
		if got := cleanStagedFile(in); got != want {
			t.Errorf("cleanStagedFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequestMatchesAllowsNamingVariants(t *testing.T) {
	row := songRequestRow{recordingID: requestedRecording, title: "You Don't Know What Love Is", artist: "Chet Baker"}
	for _, test := range []struct {
		recording, title, artist string
		want                     bool
	}{
		{requestedRecording, "Anything", "Anyone", true},
		{"", "You Don’t Know What Love Is", "Chet Baker", true},
		{"", "You Don't Know What Love Is (Remastered)", "Chet Baker Quartet", true},
		{"", "My Funny Valentine", "Chet Baker", false},
		{"", "You Don't Know What Love Is", "Billie Holiday", false},
	} {
		if got := requestMatches(row, test.recording, test.title, test.artist); got != test.want {
			t.Errorf("requestMatches(%q, %q, %q) = %v", test.recording, test.title, test.artist, got)
		}
	}
}

// A rescan can hand the identify pass a whole week at once; a requested song
// must not wait behind it, for identification or for its cover.
func TestRequestedSongIsIdentifiedAndCoveredFirst(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	mustExec(t, db, `
		UPDATE music_tracks SET added_at = '2026-09-29T06:40:00Z' WHERE id = 'track-matched';
		UPDATE music_tracks SET added_at = '2026-10-01T14:19:00Z' WHERE id = 'track-unmatched';
		INSERT INTO explo_requests (recording_id, title, artist, state, track_id)
		VALUES ('`+requestedRecording+`', 'Track Three', 'Someone', 'identifying', 'track-unmatched');
	`)
	service := NewService(ServiceOptions{DB: db, Dirs: []string{exploDir}})

	candidates, err := service.findCandidateTracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].trackID != "track-unmatched" || candidates[0].requestTitle != "Track Three" {
		t.Fatalf("requested drop not identified first: %+v", candidates)
	}

	mustExec(t, db, `INSERT INTO explo_tracks (track_id, status) VALUES ('track-matched', 'matched'), ('track-unmatched', 'matched')`)
	targets, err := service.findCoverTargets(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].trackID != "track-unmatched" {
		t.Fatalf("requested drop not covered first: %+v", targets)
	}
}

package explo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

// fakeCoverStore verifies like the real one: DownloadFromURL succeeds only
// for allow-listed URLs, and success means a real file on disk (the engine's
// albumHasLocalCover fast path os.Stat's override paths, so the bytes must
// exist). It doubles as the metadata apply layer's CoverDownloader.
type fakeCoverStore struct {
	t         *testing.T
	dir       string
	mu        sync.Mutex
	okURLs    map[string]bool
	downloads []string
	generated []string
}

func newFakeCoverStore(t *testing.T) *fakeCoverStore {
	return &fakeCoverStore{t: t, dir: t.TempDir(), okURLs: map[string]bool{}}
}

func (f *fakeCoverStore) allow(url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.okURLs[url] = true
}

func (f *fakeCoverStore) writeFile(name string) string {
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, []byte("image-bytes"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fakeCoverStore) DownloadFromURL(ctx context.Context, url string) (*catalog.Image, error) {
	f.mu.Lock()
	ok := f.okURLs[url]
	f.downloads = append(f.downloads, url)
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("fetch cover: status 404")
	}
	sum := sha256.Sum256([]byte(url))
	id := "cover_" + hex.EncodeToString(sum[:6])
	return &catalog.Image{ID: id, Path: f.writeFile(id + ".jpg"), MimeType: "image/jpeg"}, nil
}

func (f *fakeCoverStore) StoreGenerated(ctx context.Context, key string, data []byte, mimeType string) (*catalog.Image, error) {
	f.mu.Lock()
	f.generated = append(f.generated, key)
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(key))
	id := "cover_" + hex.EncodeToString(sum[:6])
	return &catalog.Image{ID: id, Path: f.writeFile(id + ".png"), MimeType: mimeType}, nil
}

func (f *fakeCoverStore) downloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.downloads)
}

// withStubCoverSources points the iTunes and Deezer rungs at stub servers.
// Each returns the given JSON body (or an empty result set when blank).
func withStubCoverSources(t *testing.T, itunesBody, deezerBody string) (itunesHits, deezerHits *int) {
	t.Helper()
	itunesHits = new(int)
	deezerHits = new(int)
	if itunesBody == "" {
		itunesBody = `{"results":[]}`
	}
	if deezerBody == "" {
		deezerBody = `{"data":[]}`
	}
	itunesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*itunesHits++
		_, _ = w.Write([]byte(itunesBody))
	}))
	deezerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*deezerHits++
		_, _ = w.Write([]byte(deezerBody))
	}))
	oldITunes, oldDeezer, oldDeezerTrack := itunesSearchURL, deezerSearchURL, deezerTrackSearchURL
	itunesSearchURL = itunesSrv.URL
	deezerSearchURL = deezerSrv.URL
	deezerTrackSearchURL = deezerSrv.URL
	t.Cleanup(func() {
		itunesSearchURL = oldITunes
		deezerSearchURL = oldDeezer
		deezerTrackSearchURL = oldDeezerTrack
		itunesSrv.Close()
		deezerSrv.Close()
	})
	return itunesHits, deezerHits
}

func TestCoverChainFallsThroughToPlaceholderThenUpgrades(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	// MusicBrainz release refs for rec-1: one release under the rg.
	mbSrv := withStubMusicBrainz(t, `{"releases":[{"id":"rel-1","release-group":{"id":"rg-1","primary-type":"Album"}}]}`, 0)
	// iTunes and Deezer both miss on the first pass.
	itunesHits, deezerHits := withStubCoverSources(t, "", "")

	store := newFakeCoverStore(t)
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "k",
		FpcalcPath:     "/fake/fpcalc",
		HTTPClient:     mbSrv.Client(),
		MetadataApply:  apply,
		Playlists:      playlists.New(db),
		Covers:         store,
		Logger:         func(format string, args ...any) { t.Logf(format, args...) },
	})

	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, musicbrainz_release_group_id, matched_title, matched_artist)
		VALUES ('track-matched', 'matched', 'rec-1', 'rg-1', 'Real Song Title', 'Real Artist');
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-album', 'album-explo', '{"title":"Real Album"}');
	`)

	applied, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 || placeholders != 1 {
		t.Fatalf("first pass applied=%d placeholders=%d, want 0/1", applied, placeholders)
	}
	// The chain tried CAA by release group, CAA by release, then the text rungs.
	joined := strings.Join(store.downloads, "\n")
	if !strings.Contains(joined, "/release-group/rg-1/front-500") {
		t.Fatalf("chain never tried the release-group rung; downloads:\n%s", joined)
	}
	if !strings.Contains(joined, "/release/rel-1/front-500") {
		t.Fatalf("chain never tried the per-release rung; downloads:\n%s", joined)
	}
	// Album rung and song rung each queried both services (all missed).
	if *itunesHits != 2 || *deezerHits != 2 {
		t.Fatalf("itunes/deezer hits = %d/%d, want 2/2 (album + song rungs)", *itunesHits, *deezerHits)
	}
	if len(store.generated) != 1 || store.generated[0] != "explo-placeholder:track-matched" {
		t.Fatalf("generated = %v, want the track placeholder", store.generated)
	}
	var coverStatus string
	var coverAttempts int
	if err := db.QueryRowContext(ctx, `SELECT cover_status, cover_attempts FROM explo_tracks WHERE track_id='track-matched'`).Scan(&coverStatus, &coverAttempts); err != nil {
		t.Fatal(err)
	}
	if coverStatus != "placeholder" || coverAttempts != 1 {
		t.Fatalf("after miss: cover_status=%q attempts=%d, want placeholder/1", coverStatus, coverAttempts)
	}
	// The placeholder landed as the TRACK override cover, path on disk.
	var fieldsJSON string
	if err := db.QueryRowContext(ctx, `SELECT fields_json FROM metadata_overrides WHERE target_kind='music-track' AND target_id='track-matched'`).Scan(&fieldsJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fieldsJSON, `"cover"`) || !strings.Contains(fieldsJSON, store.dir) {
		t.Fatalf("override missing local placeholder cover: %s", fieldsJSON)
	}

	// Not due yet (30-minute rung) — an immediate second pass is a no-op.
	applied2, placeholders2, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied2 != 0 || placeholders2 != 0 {
		t.Fatalf("undue pass applied=%d placeholders=%d, want 0/0", applied2, placeholders2)
	}

	// CAA gains the art. Force the row due, then the chain must verify the
	// release-group cover, replace the placeholder, and finish 'done'.
	store.allow(caaBaseURL + "/release-group/rg-1/front-500")
	mustExec(t, db, `UPDATE explo_tracks SET cover_attempted_at = '2000-01-01T00:00:00Z'`)
	applied3, placeholders3, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied3 != 1 || placeholders3 != 0 {
		t.Fatalf("upgrade pass applied=%d placeholders=%d, want 1/0", applied3, placeholders3)
	}
	if err := db.QueryRowContext(ctx, `SELECT cover_status FROM explo_tracks WHERE track_id='track-matched'`).Scan(&coverStatus); err != nil {
		t.Fatal(err)
	}
	if coverStatus != "done" {
		t.Fatalf("after verified fetch: cover_status=%q, want done", coverStatus)
	}
	// Terminal: nothing further to do, no more network.
	before := store.downloadCount()
	applied4, placeholders4, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied4 != 0 || placeholders4 != 0 || store.downloadCount() != before {
		t.Fatalf("terminal pass did work: applied=%d placeholders=%d downloads=%d->%d", applied4, placeholders4, before, store.downloadCount())
	}
}

func TestCoverChainAcceptsITunesWhenCAAEmpty(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	// Recording resolves, but CAA has nothing for either id.
	mbSrv := withStubMusicBrainz(t, `{"releases":[{"id":"rel-1","release-group":{"id":"rg-1","primary-type":"Album"}}]}`, 0)
	itunesHits, deezerHits := withStubCoverSources(t,
		`{"results":[{"artistName":"Real Artist","collectionName":"Real Album","artworkUrl100":"https://itunes.example/art/100x100bb.jpg"}]}`,
		"")

	store := newFakeCoverStore(t)
	store.allow("https://itunes.example/art/600x600bb.jpg")
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "k",
		FpcalcPath:     "/fake/fpcalc",
		HTTPClient:     mbSrv.Client(),
		MetadataApply:  apply,
		Playlists:      playlists.New(db),
		Covers:         store,
	})

	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, musicbrainz_release_group_id, matched_artist)
		VALUES ('track-matched', 'matched', 'rec-1', 'rg-1', 'Real Artist');
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-album', 'album-explo', '{"title":"Real Album"}');
	`)

	applied, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 || placeholders != 0 {
		t.Fatalf("applied=%d placeholders=%d, want 1/0 (iTunes art accepted)", applied, placeholders)
	}
	if *itunesHits != 1 {
		t.Fatalf("itunes hits = %d, want 1", *itunesHits)
	}
	if *deezerHits != 0 {
		t.Fatalf("deezer hits = %d, want 0 (iTunes already verified)", *deezerHits)
	}
	var coverStatus string
	if err := db.QueryRowContext(ctx, `SELECT cover_status FROM explo_tracks WHERE track_id='track-matched'`).Scan(&coverStatus); err != nil {
		t.Fatal(err)
	}
	if coverStatus != "done" {
		t.Fatalf("cover_status=%q, want done", coverStatus)
	}
}

// TestBackfillAppliesDistinctPerTrackCovers is the end-to-end proof of the
// explo artwork fix. Two identified drops share the ONE folder-derived album
// (the real explo layout), yet each must end up with its OWN cover on the
// track — read back through the SAME projection (LoadSeedFromDB applies the
// metadata overrides) that the playlist and track APIs serve. Before the fix,
// covers were album-wide, so both tracks would carry the single shared cover.
func TestBackfillAppliesDistinctPerTrackCovers(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	store := newFakeCoverStore(t)
	store.allow(caaBaseURL + "/release-group/rg-1/front-500")
	store.allow(caaBaseURL + "/release-group/rg-2/front-500")
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		FpcalcPath:    "/fake/fpcalc",
		MetadataApply: apply,
		Playlists:     playlists.New(db),
		Covers:        store,
		Logger:        func(f string, a ...any) { t.Logf(f, a...) },
	})

	// A second identified drop in the same lumped album, each with its own
	// release group already persisted (so CAA-by-release-group resolves each
	// track's own cover with no MusicBrainz call).
	mustExec(t, db, `
		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds)
		VALUES ('track-b', '04 Track Four', 'Unknown Artist', 'album-explo', 210);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-b', 'lib-1', 'track-b', '/music/explo/04 Track Four.mp3', 'explo/04 Track Four.mp3', '04 Track Four.mp3', 210);
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, musicbrainz_release_group_id, matched_title, matched_artist)
		VALUES
		  ('track-matched', 'matched', 'rec-1', 'rg-1', 'Song A', 'Artist A'),
		  ('track-b',       'matched', 'rec-2', 'rg-2', 'Song B', 'Artist B');
	`)

	applied, _, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("applied=%d, want 2 (one cover resolved per track)", applied)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tr := range seed.MusicTracks {
		if tr.ID == "track-matched" || tr.ID == "track-b" {
			if len(tr.Images) == 0 {
				t.Fatalf("track %s has no projected cover image after backfill", tr.ID)
			}
			got[tr.ID] = tr.Images[0].ID
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 covered tracks in the projection, got %#v", got)
	}
	if got["track-matched"] == got["track-b"] {
		t.Fatalf("both tracks share cover %q — per-track covers are NOT distinct", got["track-matched"])
	}
}

// TestBackfillPlaceholdersUnmatchedTracks proves an unidentified drop gets its
// OWN generated placeholder — distinct per track — so it never borrows the
// playlist's first cover and the Explore grid gets distinct tiles instead of
// collapsing onto one shared album cover.
func TestBackfillPlaceholdersUnmatchedTracks(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	store := newFakeCoverStore(t)
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		FpcalcPath:    "/fake/fpcalc",
		MetadataApply: apply,
		Playlists:     playlists.New(db),
		Covers:        store,
		Logger:        func(f string, a ...any) { t.Logf(f, a...) },
	})
	// Both fixture tracks are unidentified (no MB ids, no matched artist/title).
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status) VALUES ('track-matched', 'unmatched'), ('track-unmatched', 'unmatched');
	`)

	_, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if placeholders != 2 {
		t.Fatalf("placeholders=%d, want 2 (one per unmatched track)", placeholders)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tr := range seed.MusicTracks {
		if len(tr.Images) > 0 {
			got[tr.ID] = tr.Images[0].ID
		}
	}
	if got["track-matched"] == "" || got["track-unmatched"] == "" {
		t.Fatalf("both unmatched tracks should carry a placeholder cover, got %#v", got)
	}
	if got["track-matched"] == got["track-unmatched"] {
		t.Fatalf("placeholders are identical (%q) — not distinct per track", got["track-matched"])
	}
}

func TestCoverPassSkipsTracksWithVerifiedLocalArt(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	store := newFakeCoverStore(t)
	// A real cover file already on disk, referenced by the TRACK override —
	// e.g. scanner/embedded art or a verified fetch from a prior pass. The pass
	// must mark it done without any network.
	existing := store.writeFile("existing-art.jpg")
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "k",
		FpcalcPath:     "/fake/fpcalc",
		MetadataApply:  apply,
		Playlists:      playlists.New(db),
		Covers:         store,
	})

	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id) VALUES ('track-matched', 'matched', 'rec-1');
	`)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-track', 'track-matched', ?)`,
		`{"cover":[{"id":"cover_x","path":"`+existing+`"}]}`); err != nil {
		t.Fatal(err)
	}

	applied, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 || placeholders != 0 {
		t.Fatalf("applied=%d placeholders=%d, want 0/0 (fast path)", applied, placeholders)
	}
	// No real art was fetched over the network. (The placeholder-identity probe
	// idempotently touches the local store to compare paths, so we assert the
	// meaningful invariant directly below instead of counting store writes.)
	if store.downloadCount() != 0 {
		t.Fatalf("fast path fetched over the network: %v", store.downloads)
	}
	// The real cover override is left intact — never stamped over by a placeholder.
	var fieldsJSON string
	if err := db.QueryRowContext(ctx, `SELECT fields_json FROM metadata_overrides WHERE target_kind='music-track' AND target_id='track-matched'`).Scan(&fieldsJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fieldsJSON, existing) {
		t.Fatalf("fast path replaced the real cover; override now: %s", fieldsJSON)
	}
	var coverStatus string
	var coverAttempts int
	if err := db.QueryRowContext(ctx, `SELECT cover_status, cover_attempts FROM explo_tracks WHERE track_id='track-matched'`).Scan(&coverStatus, &coverAttempts); err != nil {
		t.Fatal(err)
	}
	if coverStatus != "done" || coverAttempts != 0 {
		t.Fatalf("fast path wrote status=%q attempts=%d, want done/0", coverStatus, coverAttempts)
	}
}

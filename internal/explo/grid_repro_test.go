package explo

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

// distinctPNG renders a small, VALID png whose pixels derive from seed, so the
// real cover store accepts the download and de-dupes distinct seeds to distinct
// cover ids (mirroring real per-track cover art).
func distinctPNG(seed int) []byte {
	m := image.NewRGBA(image.Rect(0, 0, 16, 16))
	c := color.RGBA{R: uint8(seed * 40), G: uint8(seed * 90), B: uint8(seed * 150), A: 255}
	for x := 0; x < 16; x++ {
		for y := 0; y < 16; y++ {
			m.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, m)
	return buf.Bytes()
}

// TestExplorePlaylistGridsFromPerTrackCovers reproduces the real-device bug:
// the Explore playlist tile shows a SINGLE cover while regular playlists grid
// fine. Both platforms only trigger the 2x2 grid when the server sends
// images.length > 1, so this drives the exact server path — apply per-track
// covers through the REAL cover store, rebuild the catalog the way reloadCatalog
// does (LoadSeedFromDB → NewService), and assert MusicPlaylistCoverImages yields
// TWO distinct covers, not one. Explore's tracks all share one folder-derived
// album, so a collapse to one cover is the whole "stuck on the ship" symptom.
func TestExplorePlaylistGridsFromPerTrackCovers(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	coverDir := t.TempDir()
	coverStore, err := covers.New(db, covers.Options{CoverDir: coverDir})
	if err != nil {
		t.Fatal(err)
	}

	// Cover Art Archive stub: a VALID, distinct image per release group so the
	// real cover store downloads and stores real per-track covers.
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seed := 1
		if strings.Contains(r.URL.Path, "rg-2") {
			seed = 2
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(distinctPNG(seed))
	}))
	defer img.Close()
	oldCAA := caaBaseURL
	caaBaseURL = img.URL
	t.Cleanup(func() { caaBaseURL = oldCAA })

	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: coverStore})
	svc := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		FpcalcPath:    "/fake/fpcalc",
		MetadataApply: apply,
		Playlists:     playlists.New(db),
		Covers:        coverStore,
		Logger:        func(f string, a ...any) { t.Logf(f, a...) },
	})

	// Two identified drops in the ONE lump album, each with its own release
	// group, plus a system "Explore" playlist containing both.
	mustExec(t, db, `
		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds)
		VALUES ('track-b', 'Song B', 'Artist B', 'album-explo', 200);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-b', 'lib-1', 'track-b', '/music/explo/03 Song B.mp3', 'explo/03 Song B.mp3', '03 Song B.mp3', 200);
		INSERT INTO explo_tracks (track_id, status, musicbrainz_release_group_id, matched_title, matched_artist)
		VALUES
		  ('track-matched', 'matched', 'rg-1', 'Song A', 'Artist A'),
		  ('track-b',       'matched', 'rg-2', 'Song B', 'Artist B');
		INSERT INTO music_playlists (id, owner_id, name, track_ids_json, track_count, duration_seconds, images_json, playback_json, created_at, updated_at, system)
		VALUES ('pl-explore', 'owner', 'Explore', '["track-matched","track-b"]', 2, 520, '[]', '{}',
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 1);
	`)

	if err := svc.BackfillCovers(ctx); err != nil {
		t.Fatal(err)
	}

	// Rebuild the catalog exactly like reloadCatalog does.
	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewService(seed)

	images := cat.MusicPlaylistCoverImages("pl-explore")
	distinct := map[string]bool{}
	for _, im := range images {
		key := im.ID
		if key == "" {
			key = im.Path
		}
		if key == "" {
			key = im.URL
		}
		distinct[key] = true
	}
	t.Logf("Explore MusicPlaylistCoverImages = %d image(s), %d distinct: %#v", len(images), len(distinct), images)
	if len(distinct) < 2 {
		t.Fatalf("Explore playlist collapsed to %d distinct cover(s) — want >=2 (the 2x2 grid input); this is the 'stuck on one cover' bug", len(distinct))
	}

	// Now exercise the actual /cover composite with those sources — the step the
	// app hits for a grid playlist. If ffmpeg chokes here, the tile silently
	// falls back to the first cover on BOTH platforms.
	var sourcePaths []string
	for _, im := range images {
		sourcePaths = append(sourcePaths, im.Path)
	}
	composite, cerr := coverStore.Composite(ctx, "pl-explore", "grid-hash", sourcePaths)
	if cerr != nil {
		t.Fatalf("COMPOSITE FAILED for the Explore grid sources (this is the tile bug): %v", cerr)
	}
	t.Logf("composite OK: id=%s path=%s", composite.ID, composite.Path)
}

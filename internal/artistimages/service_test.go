package artistimages

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

type patchSpy struct {
	calls int
}

func (p *patchSpy) SetMusicArtistImages(string, []catalog.Image) {
	p.calls++
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedArtist(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO music_artists (id, name) VALUES (?, ?)`, id, name); err != nil {
		t.Fatal(err)
	}
}

func TestResolveMusicArtistCoverUsesNegativeCache(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Action Bronson")
	if err := saveCacheRow(ctx, db, "artist-1", "", "lastfm"); err != nil {
		t.Fatal(err)
	}

	lastfmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("last.fm should not be called when negative cache is fresh")
	}))
	defer lastfmServer.Close()

	lastfmService := lastfm.NewService(lastfm.ServiceOptions{DB: db, HTTPClient: lastfmServer.Client()})
	lastfmService.Configure("test-key", "test-secret")
	if client, ok := lastfmService.ActiveClient(); ok {
		client.SetAPIBaseURL(lastfmServer.URL + "/")
	}

	coverService, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceOptions{
		DB:     db,
		LastFM: lastfmService,
		Covers: coverService,
	})

	artist := catalog.MusicArtist{ID: "artist-1", Name: "Action Bronson"}
	images, ok := service.ResolveMusicArtistCover(ctx, artist)
	if ok || len(images) != 0 {
		t.Fatalf("expected negative cache miss, got ok=%v images=%#v", ok, images)
	}
}

func TestResolveMusicArtistCoverCoalescesInflightRequests(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Action Bronson")

	requests := 0
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer imageServer.Close()
	imageURL := imageServer.URL + "/artist.jpg"

	lastfmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"artist": {
				"name": "Action Bronson",
				"image": [
					{"#text":"` + imageURL + `","size":"large"}
				]
			}
		}`))
	}))
	defer lastfmServer.Close()

	lastfmService := lastfm.NewService(lastfm.ServiceOptions{DB: db, HTTPClient: lastfmServer.Client()})
	lastfmService.Configure("test-key", "test-secret")
	if client, ok := lastfmService.ActiveClient(); ok {
		client.SetAPIBaseURL(lastfmServer.URL + "/")
	}

	coverService, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	coverService.SetRemoteOptions(covers.RemoteOptions{
		HTTPClient:        imageServer.Client(),
		AllowPrivateHosts: true,
	})

	spy := &patchSpy{}
	service := NewService(ServiceOptions{
		DB:      db,
		LastFM:  lastfmService,
		Covers:  coverService,
		Catalog: spy,
	})

	artist := catalog.MusicArtist{ID: "artist-1", Name: "Action Bronson"}
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			images, ok := service.ResolveMusicArtistCover(ctx, artist)
			if !ok || len(images) == 0 {
				t.Errorf("expected resolved image, got ok=%v images=%#v", ok, images)
			}
			done <- struct{}{}
		}()
	}
	<-done
	<-done

	if requests != 1 {
		t.Fatalf("requests = %d, want 1 coalesced lookup", requests)
	}
	if spy.calls != 1 {
		t.Fatalf("catalog patch calls = %d, want 1", spy.calls)
	}

	var coverID string
	if err := db.QueryRowContext(ctx, `SELECT cover_id FROM music_artist_external_images WHERE artist_id = 'artist-1'`).Scan(&coverID); err != nil {
		t.Fatal(err)
	}
	if coverID == "" {
		t.Fatal("expected cached cover id")
	}
}

func TestResolveMusicArtistCoverUsesStoredCache(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Cached Artist")

	coverService, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(coverService.CoverDir(), "cover_cached123.jpg")
	if err := os.WriteFile(imagePath, []byte{0xff, 0xd8, 0xff, 0xd9}, 0o644); err != nil {
		t.Fatal(err)
	}
	image := &catalog.Image{ID: "cover_cached123", Path: imagePath}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO extracted_covers (id, source_path, source_checksum, path, mime_type, width, height, updated_at)
		VALUES (?, ?, '', ?, 'image/jpeg', 0, 0, CURRENT_TIMESTAMP)`,
		image.ID, "test:"+image.ID, image.Path,
	); err != nil {
		t.Fatal(err)
	}
	if err := saveCacheRow(ctx, db, "artist-1", image.ID, "lastfm"); err != nil {
		t.Fatal(err)
	}

	lastfmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("last.fm should not be called when positive cache exists")
	}))
	defer lastfmServer.Close()
	lastfmService := lastfm.NewService(lastfm.ServiceOptions{DB: db, HTTPClient: lastfmServer.Client()})
	lastfmService.Configure("test-key", "test-secret")

	service := NewService(ServiceOptions{
		DB:     db,
		LastFM: lastfmService,
		Covers: coverService,
	})

	artist := catalog.MusicArtist{ID: "artist-1", Name: "Cached Artist"}
	images, ok := service.ResolveMusicArtistCover(ctx, artist)
	if !ok || len(images) == 0 || images[0].ID != image.ID {
		t.Fatalf("ResolveMusicArtistCover = ok=%v images=%#v", ok, images)
	}
}

func TestResolveMusicArtistCoverFallsBackToDeezer(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer imageServer.Close()

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Kanye West","picture_xl":"` + imageServer.URL + `/photo.jpg"}]}`))
	}))
	defer deezerServer.Close()

	lastfmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artist":{"name":"Kanye West","image":[{"#text":"","size":"large"}]}}`))
	}))
	defer lastfmServer.Close()

	lastfmService := lastfm.NewService(lastfm.ServiceOptions{DB: db, HTTPClient: lastfmServer.Client()})
	lastfmService.Configure("test-key", "test-secret")
	if client, ok := lastfmService.ActiveClient(); ok {
		client.SetAPIBaseURL(lastfmServer.URL + "/")
	}

	coverService, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	coverService.SetRemoteOptions(covers.RemoteOptions{
		HTTPClient:        imageServer.Client(),
		AllowPrivateHosts: true,
	})

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "api.deezer.com") {
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(strings.TrimPrefix(deezerServer.URL, "https://"), "http://")
			req.URL.Path = "/search/artist"
		}
		return http.DefaultTransport.RoundTrip(req)
	})}

	service := NewService(ServiceOptions{
		DB:         db,
		LastFM:     lastfmService,
		Covers:     coverService,
		HTTPClient: httpClient,
	})

	artist := catalog.MusicArtist{ID: "artist-1", Name: "Kanye West", SortName: "Ye"}
	images, ok := service.ResolveMusicArtistCover(ctx, artist)
	if !ok || len(images) == 0 {
		t.Fatalf("expected deezer fallback image, ok=%v images=%#v", ok, images)
	}
}

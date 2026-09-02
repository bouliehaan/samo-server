package artistimages

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/lastfm"
)

func TestPickDeezerArtistPictureSkipsBlankSilhouette(t *testing.T) {
	blank := "https://cdn-images.dzcdn.net/images/artist/" + deezerBlankPictureHash + "/1000x1000.jpg"
	real := "https://cdn-images.dzcdn.net/images/artist/96b688020014a21cb80a0268b90287f5/1000x1000.jpg"

	picture := pickDeezerArtistPicture("Radiohead", []deezerArtist{
		{Name: "Radiohead", PictureXL: blank, NbFan: 12},
		{Name: "Radiohead", PictureXL: real, NbFan: 4081644},
	})
	if picture != real {
		t.Fatalf("picture = %q, want the real photo %q", picture, real)
	}
}

func TestPickDeezerArtistPictureIgnoresHitWithOnlyBlankImages(t *testing.T) {
	blank := "https://cdn-images.dzcdn.net/images/artist/" + deezerBlankPictureHash + "/1000x1000.jpg"
	if picture := pickDeezerArtistPicture("Radiohead", []deezerArtist{{Name: "Radiohead", PictureXL: blank}}); picture != "" {
		t.Fatalf("picture = %q, want none: the only hit has no photograph", picture)
	}
}

func TestPickDeezerArtistPicturePrefersExactNameOverPopularity(t *testing.T) {
	tribute := "https://cdn-images.dzcdn.net/images/artist/tribute/1000x1000.jpg"
	real := "https://cdn-images.dzcdn.net/images/artist/real/1000x1000.jpg"

	picture := pickDeezerArtistPicture("Yo La Tengo", []deezerArtist{
		{Name: "Yo La Tengo Tribute Band", PictureXL: tribute, NbFan: 900000},
		{Name: "yo la tengo", PictureXL: real, NbFan: 12},
	})
	if picture != real {
		t.Fatalf("picture = %q, want the exact name match %q", picture, real)
	}
}

func TestDeezerInBandQuotaErrorIsTransient(t *testing.T) {
	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Deezer reports quota exhaustion with HTTP 200 and no results.
		_, _ = w.Write([]byte(`{"error":{"type":"Exception","message":"Quota limit exceeded","code":4}}`))
	}))
	defer deezerServer.Close()

	_, err := deezerArtistPictureURL(context.Background(), deezerRedirectClient(deezerServer), "Kanye West")
	if err == nil {
		t.Fatal("expected an error for an exhausted quota")
	}
	if !isTransientLookupError(err) {
		t.Fatalf("err = %v, want it classified transient so it is not cached as a permanent absence", err)
	}
}

// TestFetchFallsBackToDeezerWhenLastFMImageWillNotDownload is the fallback that
// matters: Last.fm answers with a URL, so the old code never consulted Deezer,
// and then that URL refuses to download and the artist is left with nothing.
func TestFetchFallsBackToDeezerWhenLastFMImageWillNotDownload(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer refused.Close()

	served := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer served.Close()

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Kanye West","picture_xl":"` + served.URL + `/photo.jpg","nb_fan":10}]}`))
	}))
	defer deezerServer.Close()

	service := newFallbackTestService(t, db, lastfmServing(t, db, refused.URL+"/lastfm.jpg"), deezerServer)

	images, outcome := service.fetchAndPersist(ctx, testArtist("artist-1", "Kanye West"))
	if outcome != fetchFound || len(images) == 0 {
		t.Fatalf("outcome = %v images = %#v, want the deezer photo after last.fm's refused", outcome, images)
	}

	var source string
	if err := db.QueryRowContext(ctx, `SELECT source FROM music_artist_external_images WHERE artist_id = 'artist-1'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "deezer" {
		t.Fatalf("source = %q, want deezer", source)
	}
}

// TestBlockedDownloadIsNotCachedAsMissing guards the month of silence: a CDN
// that refuses every request must not leave a negative cache row behind, or the
// artist stays blank long after the block has lifted.
func TestBlockedDownloadIsNotCachedAsMissing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer refused.Close()

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Kanye West","picture_xl":"` + refused.URL + `/photo.jpg","nb_fan":10}]}`))
	}))
	defer deezerServer.Close()

	service := newFallbackTestService(t, db, lastfmServiceStub(), deezerServer)

	if _, outcome := service.fetchAndPersist(ctx, testArtist("artist-1", "Kanye West")); outcome != fetchBlocked {
		t.Fatalf("outcome = %v, want fetchBlocked", outcome)
	}

	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_artist_external_images WHERE artist_id = 'artist-1'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("cache rows = %d, want 0: a refusal is not evidence the artist has no photo", rows)
	}
}

// TestMissingArtistIsCachedAsMissing is the other half: when the sources are
// reachable and simply hold nothing, the negative cache must still engage.
func TestMissingArtistIsCachedAsMissing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Nobody At All")

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer deezerServer.Close()

	service := newFallbackTestService(t, db, lastfmServiceStub(), deezerServer)

	if _, outcome := service.fetchAndPersist(ctx, testArtist("artist-1", "Nobody At All")); outcome != fetchNoImage {
		t.Fatalf("outcome = %v, want fetchNoImage", outcome)
	}

	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_artist_external_images WHERE artist_id = 'artist-1'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("cache rows = %d, want 1", rows)
	}
}

func TestBackfillReportsBlockedSeparatelyFromFailed(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer refused.Close()

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Kanye West","picture_xl":"` + refused.URL + `/photo.jpg","nb_fan":10}]}`))
	}))
	defer deezerServer.Close()

	service := newFallbackTestService(t, db, lastfmServiceStub(), deezerServer)
	service.SetBackgroundContext(ctx)

	if _, err := service.StartBackfill(ctx, BackfillModeMissing); err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}
	waitForBackfillDone(t, service)

	job, _ := service.GetBackfillJob()
	if job.Blocked != 1 || job.Failed != 0 {
		t.Fatalf("job = %+v, want blocked=1 failed=0", job)
	}
}

// deezerRedirectClient points api.deezer.com at a local stub.
func deezerRedirectClient(stub *httptest.Server) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "api.deezer.com") {
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(strings.TrimPrefix(stub.URL, "https://"), "http://")
			req.URL.Path = "/search/artist"
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

// lastfmServing returns a Last.fm service whose artist.getInfo hands back the
// supplied image URL.
func lastfmServing(t *testing.T, db *sql.DB, imageURL string) *lastfm.Service {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artist":{"name":"Kanye West","image":[{"#text":"` + imageURL + `","size":"mega"}]}}`))
	}))
	t.Cleanup(server.Close)

	service := lastfm.NewService(lastfm.ServiceOptions{DB: db, HTTPClient: server.Client()})
	service.Configure("test-key", "test-secret")
	if client, ok := service.ActiveClient(); ok {
		client.SetAPIBaseURL(server.URL + "/")
	}
	return service
}

func newFallbackTestService(t *testing.T, db *sql.DB, lastfmService *lastfm.Service, deezerServer *httptest.Server) *Service {
	t.Helper()
	coverService, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	coverService.SetRemoteOptions(covers.RemoteOptions{
		HTTPClient:        &http.Client{Timeout: 5 * time.Second},
		AllowPrivateHosts: true,
	})
	return NewService(ServiceOptions{
		DB:         db,
		LastFM:     lastfmService,
		Covers:     coverService,
		HTTPClient: deezerRedirectClient(deezerServer),
	})
}

func testArtist(id, name string) catalog.MusicArtist {
	return catalog.MusicArtist{ID: id, Name: name}
}

// TestRefusedDownloadIsNotRetriedButStillCountsBlocked pins the distinction
// between the two failure questions. A 403 is transient — it will lift, and it
// is not a fact about the artist — but asking again a moment later cannot
// change it, and hammering an ACL is what turns a soft block into a hard one.
func TestRefusedDownloadIsNotRetriedButStillCountsBlocked(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	var attempts atomic.Int32
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer refused.Close()

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Kanye West","picture_xl":"` + refused.URL + `/photo.jpg","nb_fan":10}]}`))
	}))
	defer deezerServer.Close()

	service := newFallbackTestService(t, db, lastfmServiceStub(), deezerServer)

	if _, outcome := service.fetchAndPersist(ctx, testArtist("artist-1", "Kanye West")); outcome != fetchBlocked {
		t.Fatalf("outcome = %v, want fetchBlocked", outcome)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("download attempts = %d, want 1: a refusal must not be retried", got)
	}
}

// TestServerFaultIsRetried is the other side: a 5xx is worth asking again, and
// the retry has to actually be able to succeed.
func TestServerFaultIsRetried(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	var attempts atomic.Int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer flaky.Close()

	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Kanye West","picture_xl":"` + flaky.URL + `/photo.jpg","nb_fan":10}]}`))
	}))
	defer deezerServer.Close()

	service := newFallbackTestService(t, db, lastfmServiceStub(), deezerServer)

	images, outcome := service.fetchAndPersist(ctx, testArtist("artist-1", "Kanye West"))
	if outcome != fetchFound || len(images) == 0 {
		t.Fatalf("outcome = %v images = %#v, want the photo on the second attempt", outcome, images)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("download attempts = %d, want 2", got)
	}
}

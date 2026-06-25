package artistimages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/lastfm"
)

func TestStartBackfillFetchesMissingArtistImages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")

	coverService, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

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

	lastfmService := lastfm.NewService(lastfm.ServiceOptions{DB: db})
	lastfmService.Configure("test-key", "test-secret")
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
	service.SetBackgroundContext(ctx)

	if _, err := service.StartBackfill(ctx, BackfillModeMissing); err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := service.GetBackfillJob()
		if !ok {
			t.Fatal("expected backfill job")
		}
		if !isBackfillActive(job.Status) {
			if job.Status != BackfillStatusCompleted {
				t.Fatalf("job status = %q, want completed (error=%q)", job.Status, job.Error)
			}
			if job.Total != 1 || job.Found != 1 || job.Processed != 1 {
				t.Fatalf("job = %+v, want total=1 found=1 processed=1", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for backfill, last job=%+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var coverID string
	if err := db.QueryRowContext(ctx, `SELECT cover_id FROM music_artist_external_images WHERE artist_id = 'artist-1'`).Scan(&coverID); err != nil {
		t.Fatal(err)
	}
	if coverID == "" {
		t.Fatal("expected artist-1 cover to be cached")
	}
}

func TestStartBackfillRetriesNegativeCache(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedArtist(t, db, "artist-1", "Kanye West")
	if err := saveCacheRow(ctx, db, "artist-1", "", "lastfm"); err != nil {
		t.Fatal(err)
	}

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
		LastFM:     lastfmServiceStub(),
		Covers:     coverService,
		HTTPClient: httpClient,
	})
	service.SetBackgroundContext(ctx)

	if _, err := service.StartBackfill(ctx, BackfillModeMissing); err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}
	waitForBackfillDone(t, service)
}

func lastfmServiceStub() *lastfm.Service {
	svc := lastfm.NewService(lastfm.ServiceOptions{})
	svc.Configure("test-key", "test-secret")
	return svc
}

func waitForBackfillDone(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := service.GetBackfillJob()
		if !ok {
			t.Fatal("expected backfill job")
		}
		if !isBackfillActive(job.Status) {
			if job.Status != BackfillStatusCompleted {
				t.Fatalf("job status = %q, want completed (error=%q)", job.Status, job.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for backfill, last job=%+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

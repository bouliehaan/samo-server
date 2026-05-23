package covers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestDownloadFromURLStoresCover(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	coverDir := t.TempDir()
	service, err := New(db, Options{CoverDir: coverDir})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRemoteOptions(RemoteOptions{AllowPrivateHosts: true})

	payload := []byte("\xff\xd8\xff\xe0pretend-jpeg-bytes")
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	image, err := service.DownloadFromURL(ctx, server.URL+"/cover.jpg")
	if err != nil {
		t.Fatalf("DownloadFromURL: %v", err)
	}
	if image == nil {
		t.Fatal("image is nil")
	}
	if !strings.HasPrefix(image.ID, "cover_") {
		t.Errorf("ID = %q", image.ID)
	}
	if image.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q", image.MimeType)
	}
	if image.Path == "" {
		t.Fatalf("Path empty")
	}
	if _, err := os.Stat(image.Path); err != nil {
		t.Fatalf("expected cover file on disk: %v", err)
	}

	// Re-downloading the same URL should hit the cache and skip the upstream.
	again, err := service.DownloadFromURL(ctx, server.URL+"/cover.jpg")
	if err != nil {
		t.Fatalf("second DownloadFromURL: %v", err)
	}
	if again.ID != image.ID {
		t.Fatalf("second download produced new ID %q", again.ID)
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits)
	}
}

func TestDownloadFromURLRejectsNonImageContent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service, err := New(db, Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRemoteOptions(RemoteOptions{AllowPrivateHosts: true})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("not an image"))
	}))
	defer server.Close()

	if _, err := service.DownloadFromURL(ctx, server.URL+"/page.html"); err != ErrUnsupportedType {
		t.Fatalf("err = %v, want ErrUnsupportedType", err)
	}
}

func TestDownloadFromURLBlocksLoopbackByDefault(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service, err := New(db, Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Default options should refuse loopback URLs.
	service.SetRemoteOptions(RemoteOptions{})

	if _, err := service.DownloadFromURL(ctx, "http://127.0.0.1:9999/cover.jpg"); err != ErrForbiddenHost {
		t.Fatalf("err = %v, want ErrForbiddenHost", err)
	}
}

func TestDownloadFromURLRejectsOversizedBody(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service, err := New(db, Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRemoteOptions(RemoteOptions{AllowPrivateHosts: true, MaxBytes: 8})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("this body is definitely longer than eight bytes"))
	}))
	defer server.Close()

	if _, err := service.DownloadFromURL(ctx, server.URL+"/big.png"); err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

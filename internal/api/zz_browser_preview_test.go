package api

import (
	"context"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/search"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserPreview(t *testing.T) {
	if os.Getenv("SAMO_BROWSER_PREVIEW") != "1" {
		t.Skip("manual preview")
	}
	ctx := context.Background()
	db := storagetest.Open(t)
	userService, _, _ := testUserServiceWithTokens(t, ctx, db)
	libs := libraries.New(db, scanner.New(db))
	root := t.TempDir()
	lib, err := libs.Create(ctx, libraries.CreateLibraryInput{Name: "Demo library", Kind: "music", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = libs.ScanAll(ctx, libraries.TriggerStartup, ""); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile("/tmp/samo-browser-tone.wav")
	if err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(root, "test-tone.wav")
	if err := os.WriteFile(audioPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO media_files (id, library_id, path, file_name, mime_type, size_bytes, duration_seconds) VALUES ('demo-file', ?, ?, 'test-tone.wav', 'audio/wav', ?, 45)`, lib.ID, audioPath, len(payload))
	if err != nil {
		t.Fatal(err)
	}
	audio := catalog.AudioFile{ID: "demo-file", Path: audioPath, MimeType: "audio/wav", DurationSeconds: 45}
	second := audio
	second.ID = "demo-file-2"
	secondPath := filepath.Join(root, "second.wav")
	if err := os.WriteFile(secondPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	second.Path = secondPath
	second.StartOffsetSeconds = 45
	_, err = db.ExecContext(ctx, `INSERT INTO media_files (id, library_id, path, file_name, mime_type, size_bytes, duration_seconds) VALUES ('demo-file-2', ?, ?, 'test-tone.wav', 'audio/wav', ?, 45)`, lib.ID, secondPath, len(payload))
	if err != nil {
		t.Fatal(err)
	}
	ep := catalog.PodcastEpisode{ID: "demo-episode", PodcastID: "demo-podcast", Title: "Browser episode", DurationSeconds: 45, AudioFiles: []catalog.AudioFile{audio}, Progress: catalog.PlaybackState{ProgressSeconds: 12}}
	sourceService := sources.New(db)
	_, err = sourceService.AddInternetRadioStation(ctx, sources.AddInternetRadioStationInput{Name: "Browser radio", StreamURL: "http://127.0.0.1:18082/test-radio.wav"})
	if err != nil {
		t.Fatal(err)
	}
	seed := catalog.Seed{
		MusicAlbums:     []catalog.MusicAlbum{{ID: "demo-album", Title: "Elvis", ArtistIDs: []string{"demo-artist"}, ArtistNames: []string{"Samo"}, TrackCount: 1, DurationSeconds: 45}},
		MusicArtists:    []catalog.MusicArtist{{ID: "demo-artist", Name: "Elvis Presley", AlbumCount: 96}},
		Audiobooks:      []catalog.AudiobookItem{{ID: "demo-book", Book: &catalog.BookMetadata{Title: "Browser audiobook"}, DurationSeconds: 90, AudioFiles: []catalog.AudioFile{audio, second}}},
		Podcasts:        []catalog.PodcastItem{{ID: "demo-podcast", Podcast: &catalog.PodcastMetadata{Title: "Browser podcast", EpisodeCount: 1}, Episodes: []catalog.PodcastEpisode{ep}}},
		PodcastEpisodes: []catalog.PodcastEpisode{ep},
		MusicTracks:     []catalog.MusicTrack{{ID: "demo-track", Title: "Browser playback test", AlbumID: "demo-album", AlbumTitle: "Browser session", ArtistIDs: []string{"demo-artist"}, ArtistNames: []string{"Samo"}, DurationSeconds: 45, AudioFiles: []catalog.AudioFile{{ID: "demo-file", Path: audioPath, MimeType: "audio/wav", DurationSeconds: 45}}}},
	}
	cat := catalog.NewService(seed)
	searchService := search.New()
	searchService.Rebuild(seed)
	h := NewServer(ServerOptions{DB: db, Search: searchService, Users: userService, Libraries: libs, Catalog: cat, Files: files.New(db, root), Sources: sourceService, PodcastStream: podcaststream.New()})
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /__stop", func(w http.ResponseWriter, r *http.Request) { close(done) })
	mux.HandleFunc("GET /test-radio.wav", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, audioPath) })
	mux.Handle("/", WithSecurityHeaders(h))
	srv := &http.Server{Addr: "0.0.0.0:18082", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			t.Error(err)
		}
	}()
	t.Log("preview listening at http://127.0.0.1:18082")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
	}
	srv.Close()
}

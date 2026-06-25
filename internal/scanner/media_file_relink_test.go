package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestReconcileMediaFileTrackLinksRestoresAudioFiles(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	libraryID := "lib-music"
	trackPID := "pid-abc"
	wantTrack := stableID("track", libraryID, trackPID)
	staleTrack := "track-stale"
	path := "/music/Artist/Album/song.flac"

	scanner := New(db)
	if err := scanner.upsertLibrary(ctx, Library{ID: libraryID, Name: "Music", Kind: "music", Path: "/music"}); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertMusicAlbum(ctx, catalog.MusicAlbum{ID: "album-1", Title: "Album"}); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertMusicTrack(ctx, catalog.MusicTrack{ID: wantTrack, Title: "Song", AlbumID: "album-1"}); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertMusicTrack(ctx, catalog.MusicTrack{ID: staleTrack, Title: "Stale", AlbumID: "album-1"}); err != nil {
		t.Fatal(err)
	}
	file := catalog.AudioFile{ID: stableID("file", path), Path: path, FileName: "song.flac"}
	if err := scanner.upsertAudioFile(ctx, libraryID, audioFileOwner{TrackID: staleTrack}, file, trackPID, "hash"); err != nil {
		t.Fatal(err)
	}

	updated, err := scanner.reconcileMediaFileTrackLinks(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var track *catalog.MusicTrack
	for i := range seed.MusicTracks {
		if seed.MusicTracks[i].ID == wantTrack {
			track = &seed.MusicTracks[i]
			break
		}
	}
	if track == nil {
		t.Fatalf("track %q missing from catalog", wantTrack)
	}
	if len(track.AudioFiles) != 1 {
		t.Fatalf("audioFiles = %d, want 1", len(track.AudioFiles))
	}
	if track.AudioFiles[0].Path != path {
		t.Fatalf("path = %q, want %q", track.AudioFiles[0].Path, path)
	}
}

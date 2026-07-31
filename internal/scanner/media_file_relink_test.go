package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestReconcileMediaFileTrackLinksRestoresAudioFiles(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

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

	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
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

package radio

import (
	"context"
	"database/sql"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestImportConfigIfEmptySeedsStations(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Stations: []StationConfig{{
		ID:          "static-fm",
		Name:        "Static FM",
		Description: "Lo-fi night signals",
		Media: []MediaItemConfig{
			{Path: "/srv/radio/clip-a.mp3", DurationSeconds: 60, Title: "Clip A"},
			{Path: "/srv/radio/clip-b.mp3", DurationSeconds: 90, Title: "Clip B"},
		},
	}}}

	if err := ImportConfigIfEmpty(ctx, db, cfg); err != nil {
		t.Fatalf("ImportConfigIfEmpty: %v", err)
	}
	records, err := LoadStationsFromDB(ctx, db)
	if err != nil {
		t.Fatalf("LoadStationsFromDB: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	record := records[0]
	if record.Name != "Static FM" {
		t.Errorf("Name = %q", record.Name)
	}
	if len(record.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(record.Items))
	}
	if record.Items[0].SourceKind != ItemSourcePath {
		t.Errorf("SourceKind = %q", record.Items[0].SourceKind)
	}
	if record.Items[0].ResolvedPath != "/srv/radio/clip-a.mp3" {
		t.Errorf("ResolvedPath = %q", record.Items[0].ResolvedPath)
	}

	// A second import is a no-op once the DB has rows.
	if err := ImportConfigIfEmpty(ctx, db, Config{Stations: []StationConfig{{ID: "should-not-import", Name: "Nope"}}}); err != nil {
		t.Fatalf("second ImportConfigIfEmpty: %v", err)
	}
	records, _ = LoadStationsFromDB(ctx, db)
	if len(records) != 1 {
		t.Fatalf("len(records) after second import = %d, want 1", len(records))
	}
}

func TestCreateAndDeleteStation(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	record, err := CreateStation(ctx, db, CreateStationInput{
		Name:        "Memory Drift",
		Description: "Ambient station",
		Items: []CreateStationItemInput{{
			SourceKind:      ItemSourcePath,
			SourcePath:      "/srv/radio/drift.mp3",
			Title:           "Drift",
			DurationSeconds: 120,
		}},
	})
	if err != nil {
		t.Fatalf("CreateStation: %v", err)
	}
	if record.ID == "" {
		t.Fatal("station ID not generated")
	}
	if len(record.Items) != 1 {
		t.Fatalf("len(items) = %d", len(record.Items))
	}

	item, err := AddStationItem(ctx, db, record.ID, CreateStationItemInput{
		SourceKind:      ItemSourcePath,
		SourcePath:      "/srv/radio/drift-b.mp3",
		Title:           "Drift B",
		DurationSeconds: 180,
	})
	if err != nil {
		t.Fatalf("AddStationItem: %v", err)
	}
	if item.Position != 1 {
		t.Fatalf("item position = %d, want 1", item.Position)
	}

	if err := RemoveStationItem(ctx, db, item.ID); err != nil {
		t.Fatalf("RemoveStationItem: %v", err)
	}

	if err := DeleteStation(ctx, db, record.ID); err != nil {
		t.Fatalf("DeleteStation: %v", err)
	}
	if _, err := LoadStationByID(ctx, db, record.ID); err != ErrStationNotFound {
		t.Fatalf("LoadStationByID after delete = %v, want ErrStationNotFound", err)
	}
}

func TestResolveMusicTrackJoinsMediaFile(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	mustExec(t, db, `INSERT INTO libraries (id, name, kind, media_type, path) VALUES ('lib1', 'Music', 'music', '', '/srv/music')`)
	mustExec(t, db, `INSERT INTO music_artists (id, name) VALUES ('artist1', 'Test Artist')`)
	mustExec(t, db, `INSERT INTO music_albums (id, title) VALUES ('album1', 'Test Album')`)
	mustExec(t, db, `INSERT INTO music_tracks (id, album_id, title, display_artist, duration_seconds) VALUES ('track1', 'album1', 'Test Track', 'Test Artist', 240)`)
	mustExec(t, db, `INSERT INTO media_files (id, library_id, track_id, path, file_name, mime_type, duration_seconds) VALUES ('mf1', 'lib1', 'track1', '/srv/music/test.flac', 'test.flac', 'audio/flac', 240)`)

	station, err := CreateStation(ctx, db, CreateStationInput{
		Name: "Now Playing",
		Items: []CreateStationItemInput{{
			SourceKind: ItemSourceMusicTrack,
			SourceID:   "track1",
			Weight:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateStation: %v", err)
	}
	if len(station.Items) != 1 {
		t.Fatalf("len(items) = %d", len(station.Items))
	}
	item := station.Items[0]
	if item.ResolvedPath != "/srv/music/test.flac" {
		t.Errorf("ResolvedPath = %q", item.ResolvedPath)
	}
	if item.Title != "Test Track" {
		t.Errorf("Title = %q", item.Title)
	}
	if item.Artist != "Test Artist" {
		t.Errorf("Artist = %q", item.Artist)
	}
	if item.DurationSeconds != 240 {
		t.Errorf("DurationSeconds = %d, want 240", item.DurationSeconds)
	}
}

func TestServiceFromDBUsesResolvedItems(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateStation(ctx, db, CreateStationInput{
		Name: "Pulse",
		Items: []CreateStationItemInput{{
			SourceKind:      ItemSourcePath,
			SourcePath:      "/srv/radio/pulse.mp3",
			Title:           "Pulse Loop",
			DurationSeconds: 60,
		}},
	}); err != nil {
		t.Fatalf("CreateStation: %v", err)
	}

	service, err := NewServiceFromDB(ctx, db, Config{})
	if err != nil {
		t.Fatalf("NewServiceFromDB: %v", err)
	}
	if service.StationCount() != 1 {
		t.Fatalf("StationCount = %d", service.StationCount())
	}
	stations := service.ListStations()
	if stations[0].Name != "Pulse" {
		t.Fatalf("ListStations[0].Name = %q", stations[0].Name)
	}
	if stations[0].TotalDurationSeconds <= 0 {
		t.Fatalf("TotalDurationSeconds = %d", stations[0].TotalDurationSeconds)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

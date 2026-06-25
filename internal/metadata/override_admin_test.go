package metadata

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestMetadataOverrideAdminGetClearDelete(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	artistID := "artist-1"
	if _, err := db.ExecContext(ctx, `INSERT INTO music_artists (id, name) VALUES (?, 'Old')`, artistID); err != nil {
		t.Fatal(err)
	}

	service := NewMetadataApplyService(db)
	if _, err := service.Apply(ctx, MetadataApplyRequest{
		TargetKind: string(ApplyTargetMusicArtist),
		TargetID:   artistID,
		Fields:     []string{"name", "sortName"},
		Candidate: SearchResult{
			MediaType: "musicArtist",
			Title:     "Applied Name",
			SortTitle: "Applied Sort",
		},
	}); err != nil {
		t.Fatal(err)
	}

	view, err := service.GetOverride(ctx, string(ApplyTargetMusicArtist), artistID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Fields["name"] != "Applied Name" {
		t.Fatalf("override name = %#v", view.Fields["name"])
	}
	if len(view.AllowedFields) == 0 {
		t.Fatal("expected allowed fields")
	}

	if err := service.ClearOverrideFields(ctx, string(ApplyTargetMusicArtist), artistID, []string{"sortName"}); err != nil {
		t.Fatal(err)
	}
	view, err = service.GetOverride(ctx, string(ApplyTargetMusicArtist), artistID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.Fields["sortName"]; ok {
		t.Fatalf("sortName should be cleared: %#v", view.Fields)
	}
	if view.Fields["name"] != "Applied Name" {
		t.Fatalf("name should remain: %#v", view.Fields["name"])
	}

	if err := service.DeleteOverride(ctx, string(ApplyTargetMusicArtist), artistID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetOverride(ctx, string(ApplyTargetMusicArtist), artistID); err == nil {
		t.Fatal("expected override not found after delete")
	}
}

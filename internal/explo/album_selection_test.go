package explo

import (
	"context"
	"net/http"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
)

func TestSOSRejectsUnflaggedChartAlbum(t *testing.T) {
	compilation := acoustidReleaseGrp{ID: "chart", Title: "The Netherlands Top 100 Deezer", Type: "Album"}
	for _, groups := range [][]acoustidReleaseGrp{
		{compilation, {ID: "sos", Title: "SOS", Type: "Album"}},
		{{ID: "sos", Title: "SOS", Type: "Album"}, compilation},
	} {
		id, title := bestReleaseGroup(groups)
		if id != "sos" || title != "SOS" {
			t.Fatalf("picked %q / %q", id, title)
		}
	}
	if id, title := bestReleaseGroup([]acoustidReleaseGrp{compilation}); id != "" || title != "" {
		t.Fatalf("compilation-only response became %q / %q", id, title)
	}
}

func TestSOSRecoversAlbumFromCleanDuplicateWithoutChangingRecording(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[{"id":"chart-release","release-group":{"id":"chart","title":"Top 100 Netherlands Deezer","primary-type":"Album"}}]}`, 0)
	service := newExploServiceWithFakeProvider(&fakeMusicProvider{results: []metadata.SearchResult{
		{Title: "SOS", Authors: []catalog.ContributorRef{{Name: "SZA"}}, DurationSeconds: 117,
			ExternalIDs: catalog.ExternalIDs{MusicBrainzRecordingID: "clean-recording", MusicBrainzReleaseGroupID: "sos"},
			Raw:         map[string]any{"releaseTitle": "SOS"}},
	}})
	service.httpClient = srv.Client()
	got := service.resolveIdentifiedAlbum(context.Background(), identifiedTrack{
		Source: "acoustid", Title: "SOS", Artist: "SZA", MusicBrainzRecordingID: "fingerprint-recording",
		Album: "Top 100 Netherlands Deezer", MusicBrainzReleaseGroupID: "chart",
	}, 117)
	if got.Album != "SOS" || got.MusicBrainzReleaseGroupID != "sos" || got.MusicBrainzRecordingID != "fingerprint-recording" {
		t.Fatalf("resolved identity = %+v", got)
	}
}

func TestResolveAlbumDoesNotReintroduceRejectedGroup(t *testing.T) {
	srv := withStubReleaseGroup(t, `{"title":"Sampler","primary-type":"Album","secondary-types":["Compilation"]}`, 0)
	withStubMusicBrainz(t, `{"releases":[]}`, 0)
	service := &Service{httpClient: srv.Client(), logger: func(string, ...any) {}}
	got := service.resolveAlbumTitle(context.Background(), identifiedTrack{Title: "SOS", Artist: "SZA", MusicBrainzRecordingID: "rec", MusicBrainzReleaseGroupID: "comp"})
	if got.Album != "" || got.MusicBrainzReleaseGroupID != "" {
		t.Fatalf("reintroduced compilation: %+v", got)
	}
}

func TestSongCoverRequiresOriginalAlbum(t *testing.T) {
	withStubCoverSources(t,
		`{"results":[
 {"artistName":"SZA","trackName":"SOS","collectionName":"Now That's What I Call Music 44","artworkUrl100":"https://wrong/100x100.jpg"},
 {"artistName":"SZA","trackName":"SOS","collectionName":"Some Other Album","artworkUrl100":"https://other/100x100.jpg"},
 {"artistName":"SZA","trackName":"SOS","collectionName":"SOS","artworkUrl100":"https://sos/100x100.jpg"}]}`,
		`{"data":[
 {"artist":{"name":"SZA"},"title":"SOS","album":{"title":"Top 100 Netherlands Deezer","cover_xl":"https://wrong/art.jpg"}},
 {"artist":{"name":"SZA"},"title":"SOS","album":{"title":"Some Other Album","cover_xl":"https://other/art.jpg"}},
 {"artist":{"name":"SZA"},"title":"SOS","album":{"title":"SOS","cover_xl":"https://sos/art.jpg"}}]}`)
	itunes, err := lookupITunesTrackCover(context.Background(), http.DefaultClient, "SZA", "SOS", "SOS")
	if err != nil || itunes != "https://sos/600x600.jpg" {
		t.Fatalf("itunes = %q, %v", itunes, err)
	}
	deezer, err := lookupDeezerTrackCover(context.Background(), http.DefaultClient, "SZA", "SOS", "SOS")
	if err != nil || deezer != "https://sos/art.jpg" {
		t.Fatalf("deezer = %q, %v", deezer, err)
	}
	for _, lookup := range []func(context.Context, *http.Client, string, string, string) (string, error){lookupITunesTrackCover, lookupDeezerTrackCover} {
		if got, err := lookup(context.Background(), http.DefaultClient, "SZA", "SOS", ""); got != "" || err != nil {
			t.Fatalf("unresolved album got artwork %q: %v", got, err)
		}
	}
}

func TestChartAlbumIsRecheckedEvenWhenFileTagsAgree(t *testing.T) {
	row := ledgerIdentity{candidate: candidateTrack{title: "SOS", artist: "SZA", album: "Top 100 Netherlands Deezer"}, match: identifiedTrack{Title: "SOS", Artist: "SZA", Album: "Top 100 Netherlands Deezer"}}
	if !identityContradicted(row) {
		t.Fatal("existing chart album will never be repaired")
	}
}

func TestUnresolvedAlbumReplacesCompilationOverride(t *testing.T) {
	ctx := context.Background()
	db, dir := setupExploTestDB(t)
	service := NewService(ServiceOptions{DB: db, Dirs: []string{dir}, MetadataApply: metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{})})
	mustExec(t, db, `INSERT INTO metadata_overrides (target_kind,target_id,fields_json) VALUES ('music-album','album-explo','{"title":"Top 100 Netherlands Deezer"}')`)
	match := identifiedTrack{Source: "acoustid", Title: "SOS", Artist: "SZA"}
	if err := service.applyMatch(ctx, "track-matched", "album-explo", match); err != nil {
		t.Fatal(err)
	}
	if got := service.overriddenAlbumTitle(ctx, "album-explo"); got != "Unknown Album" {
		t.Fatalf("stale album = %q", got)
	}
	if err := service.recordProcessed(ctx, "track-matched", "matched", match, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.keepAlbum(ctx, "track-matched", catalog.MusicTrack{AlbumTitle: "Unknown Album"}); err == nil {
		t.Fatal("kept track without identifying its album")
	}
}

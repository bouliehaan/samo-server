package explo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withStubMusicBrainz(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	old := musicbrainzRecordingURL
	musicbrainzRecordingURL = srv.URL + "/"
	t.Cleanup(func() {
		musicbrainzRecordingURL = old
		srv.Close()
	})
	return srv
}

func TestFetchRecordingReleaseRefsPrefersAlbumType(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[
		{"id":"rel-1","release-group":{"id":"rg-single","primary-type":"Single"}},
		{"id":"rel-2","release-group":{"id":"rg-album","primary-type":"Album"}}
	]}`, 0)

	refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1")
	if err != nil || refs.ReleaseGroupID != "rg-album" {
		t.Fatalf("got (%q, %v), want (rg-album, nil)", refs.ReleaseGroupID, err)
	}
	// The chosen release group's own releases come first so the per-release
	// CAA rung tries the real record's pressings before other appearances.
	if len(refs.ReleaseIDs) != 2 || refs.ReleaseIDs[0] != "rel-2" || refs.ReleaseIDs[1] != "rel-1" {
		t.Fatalf("release ids = %v, want [rel-2 rel-1] (chosen-group first)", refs.ReleaseIDs)
	}
}

func TestFetchRecordingReleaseRefsFallsBackToFirst(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[{"id":"rel-9","release-group":{"id":"rg-comp","primary-type":"Compilation"}}]}`, 0)
	refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1")
	if err != nil || refs.ReleaseGroupID != "rg-comp" {
		t.Fatalf("got (%q, %v), want (rg-comp, nil)", refs.ReleaseGroupID, err)
	}
}

func TestFetchRecordingReleaseRefsEmptyAndError(t *testing.T) {
	// No id -> empty, no request.
	if refs, err := fetchRecordingReleaseRefs(context.Background(), http.DefaultClient, "  "); refs.ReleaseGroupID != "" || len(refs.ReleaseIDs) != 0 || err != nil {
		t.Fatalf("blank id got (%+v, %v)", refs, err)
	}
	// No release groups -> empty (definitive: nothing to find), no error.
	srv := withStubMusicBrainz(t, `{"releases":[]}`, 0)
	if refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1"); refs.ReleaseGroupID != "" || err != nil {
		t.Fatalf("no-rg got (%+v, %v), want empty/nil", refs, err)
	}
	// Non-200 -> error (transient; caller should retry, not mark resolved).
	srv2 := withStubMusicBrainz(t, `{}`, http.StatusServiceUnavailable)
	if _, err := fetchRecordingReleaseRefs(context.Background(), srv2.Client(), "rec-1"); err == nil {
		t.Fatal("expected error on 503")
	}
}

// withStubReleaseGroup points the release-group lookup at a stub, for the
// path where identification already chose a release group and only its NAME
// is missing.
func withStubReleaseGroup(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	old := musicbrainzReleaseGroupURL
	musicbrainzReleaseGroupURL = srv.URL + "/"
	t.Cleanup(func() {
		musicbrainzReleaseGroupURL = old
		srv.Close()
	})
	return srv
}

// The release group's TITLE is the album name a kept track is filed under, so
// the same lookup that ranks release groups has to carry it back.
func TestFetchRecordingReleaseRefsCarriesTheTitle(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[
		{"id":"rel-1","release-group":{"id":"rg-comp","title":"MNM Hits","primary-type":"Album","secondary-types":["Compilation"]}},
		{"id":"rel-2","release-group":{"id":"rg-album","title":"The Art of Loving","primary-type":"Album"}}
	]}`, 0)

	refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The compilation is listed first and is still rejected: that ordering is
	// exactly how "MNM Hits" became an album name in the library.
	if refs.ReleaseGroupTitle != "The Art of Loving" {
		t.Fatalf("release group title = %q, want %q", refs.ReleaseGroupTitle, "The Art of Loving")
	}
}

func TestFetchReleaseGroupTitle(t *testing.T) {
	srv := withStubReleaseGroup(t, `{"title":"Oracular Spectacular"}`, 0)
	got, err := fetchReleaseGroupTitle(context.Background(), srv.Client(), "rg-1")
	if err != nil || got != "Oracular Spectacular" {
		t.Fatalf("got (%q, %v), want (Oracular Spectacular, nil)", got, err)
	}
	// A blank id asks nothing and reports nothing — not an error.
	if got, err := fetchReleaseGroupTitle(context.Background(), http.DefaultClient, "  "); got != "" || err != nil {
		t.Fatalf("blank id got (%q, %v)", got, err)
	}
}

// A match that knows the recording knows the record. Leaving the album blank
// used to mean "keep whatever the scanner read off the file", which for a drop
// is the sharer's compilation tag or the drop folder's name.
func TestResolveAlbumTitleFillsABlankAlbum(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[
		{"id":"rel-1","release-group":{"id":"rg-album","title":"Oracular Spectacular","primary-type":"Album"}}
	]}`, 0)
	service := &Service{httpClient: srv.Client(), logger: func(string, ...any) {}}

	got := service.resolveAlbumTitle(context.Background(), identifiedTrack{
		Title:                  "Kids",
		Artist:                 "MGMT",
		MusicBrainzRecordingID: "rec-1",
	})
	if got.Album != "Oracular Spectacular" {
		t.Fatalf("album = %q, want %q", got.Album, "Oracular Spectacular")
	}
	if got.MusicBrainzReleaseGroupID != "rg-album" {
		t.Fatalf("release group = %q, want rg-album", got.MusicBrainzReleaseGroupID)
	}
}

// An album the identifier DID name is authoritative — no lookup, no overwrite.
func TestResolveAlbumTitleLeavesAKnownAlbumAlone(t *testing.T) {
	service := &Service{httpClient: http.DefaultClient, logger: func(string, ...any) {}}
	got := service.resolveAlbumTitle(context.Background(), identifiedTrack{
		Album:                  "The Art of Loving",
		MusicBrainzRecordingID: "rec-1",
	})
	if got.Album != "The Art of Loving" {
		t.Fatalf("album = %q, want it untouched", got.Album)
	}
}

// A MusicBrainz hiccup costs an album name; it must never blank a good one or
// fail the identification outright.
func TestResolveAlbumTitleSurvivesLookupFailure(t *testing.T) {
	srv := withStubMusicBrainz(t, `{}`, http.StatusServiceUnavailable)
	service := &Service{httpClient: srv.Client(), logger: func(string, ...any) {}}
	got := service.resolveAlbumTitle(context.Background(), identifiedTrack{
		Title:                  "Kids",
		MusicBrainzRecordingID: "rec-1",
	})
	if got.Album != "" || got.Title != "Kids" {
		t.Fatalf("failed lookup should leave the match as-is, got %+v", got)
	}
}

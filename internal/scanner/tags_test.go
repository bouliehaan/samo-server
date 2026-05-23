package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestFirstTagUsesAliases(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"TPE2": "Album Artist Name",
		"UPC":  "012345678901",
	})
	if got := firstTag(tags, "album_artist"); got != "Album Artist Name" {
		t.Fatalf("album artist = %q, want Album Artist Name", got)
	}
	if got := barcodeFromTags(tags); got != "012345678901" {
		t.Fatalf("barcode = %q, want UPC value", got)
	}
}

func TestExplicitTagHonorsITunesAdvisory(t *testing.T) {
	clean := normalizeTags(map[string]string{"itunesadvisory": "2"})
	if explicitTag(clean) {
		t.Fatal("advisory 2 should not be explicit")
	}
	explicit := normalizeTags(map[string]string{"itunesadvisory": "1"})
	if !explicitTag(explicit) {
		t.Fatal("advisory 1 should be explicit")
	}
}

func TestMergeProbeTagsFillsMissingFields(t *testing.T) {
	merged := mergeProbeTags([]probedFile{
		{Tags: catalog.Tags{"title": []string{"Part One"}}},
		{Tags: catalog.Tags{"author": []string{"Ada Archive"}}},
	})
	if firstTag(merged, "title") != "Part One" {
		t.Fatalf("title = %q", firstTag(merged, "title"))
	}
	if firstTag(merged, "author") != "Ada Archive" {
		t.Fatalf("author = %q", firstTag(merged, "author"))
	}
}

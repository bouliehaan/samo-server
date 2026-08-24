package explo

import (
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The kept copy has to land where the rest of the library already lives —
// <root>/<album artist>/<album>/<NN> - <title>.<ext> — or it is a stray file
// in a folder nobody browses.
func TestKeepDestinationMatchesLibraryLayout(t *testing.T) {
	track := catalog.MusicTrack{
		Title:            "Crystalised",
		DisplayArtist:    "The xx",
		AlbumArtistNames: []string{"The xx"},
		AlbumTitle:       "xx",
		TrackNumber:      3,
	}
	got := keepDestination("/mnt/music", track, ".flac")
	want := filepath.Join("/mnt/music", "The xx", "xx", "03 - Crystalised.flac")
	if got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

// Album artist wins over the display artist, so every track on a compilation
// files under one folder instead of scattering by featured performer.
func TestKeepDestinationPrefersAlbumArtist(t *testing.T) {
	track := catalog.MusicTrack{
		Title:            "Sunflower",
		DisplayArtist:    "Post Malone & Swae Lee",
		AlbumArtistNames: []string{"Various Artists"},
		AlbumTitle:       "Spider-Man: Into the Spider-Verse",
		TrackNumber:      1,
	}
	if dir := filepath.Base(filepath.Dir(filepath.Dir(keepDestination("/m", track, ".mp3")))); dir != "Various Artists" {
		t.Fatalf("album artist folder = %q, want %q", dir, "Various Artists")
	}
}

// A track with no number should not be prefixed with "00 - ".
func TestKeepDestinationOmitsMissingTrackNumber(t *testing.T) {
	track := catalog.MusicTrack{Title: "Untitled Demo", DisplayArtist: "Someone", AlbumTitle: "Demos"}
	if base := filepath.Base(keepDestination("/m", track, ".flac")); base != "Untitled Demo.flac" {
		t.Fatalf("basename = %q, want %q", base, "Untitled Demo.flac")
	}
}

// Metadata is attacker-adjacent: it comes from strangers' file tags by way of
// MusicBrainz. A title containing a separator must never escape its directory.
func TestSafeComponentCannotTraverse(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "a/b", `a\b`, "..", "."} {
		got := safeComponent(in, "fallback")
		if got == "" {
			t.Fatalf("safeComponent(%q) returned empty", in)
		}
		for _, bad := range []string{"/", `\`} {
			if containsRune(got, bad) {
				t.Fatalf("safeComponent(%q) = %q, still contains %q", in, got, bad)
			}
		}
		if got == ".." || got == "." {
			t.Fatalf("safeComponent(%q) = %q, which traverses", in, got)
		}
	}
}

func TestSafeComponentFallsBackWhenEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "...", "\x00"} {
		if got := safeComponent(in, "Unknown Artist"); got != "Unknown Artist" {
			t.Fatalf("safeComponent(%q) = %q, want the fallback", in, got)
		}
	}
}

// Keep must refuse anything outside the drop folder, or the endpoint becomes a
// way to duplicate arbitrary library files to a second path.
func TestUnderAnyDirGuardsTheDropFolder(t *testing.T) {
	dirs := []string{"/mnt/data2tb/Music/explo/Weekly-Exploration"}

	if !underAnyDir("/mnt/data2tb/Music/explo/Weekly-Exploration/a.flac", dirs) {
		t.Fatal("a file inside the drop folder must be keepable")
	}
	for _, outside := range []string{
		"/mnt/data2tb/Music/Adele/25/01 - Hello.flac",
		"/mnt/data2tb/Music/explo/Weekly-Exploration-Other/a.flac", // prefix, not a child
		"/etc/passwd",
		"/mnt/data2tb/Music/explo/Weekly-Exploration", // the folder itself
	} {
		if underAnyDir(outside, dirs) {
			t.Fatalf("%q must not be keepable", outside)
		}
	}
}

func containsRune(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

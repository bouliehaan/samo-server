package explo

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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
	got := keepDestination("/mnt/music", track, "xx", ".flac")
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
	if dir := filepath.Base(filepath.Dir(filepath.Dir(keepDestination("/m", track, "Spider-Man: Into the Spider-Verse", ".mp3")))); dir != "Various Artists" {
		t.Fatalf("album artist folder = %q, want %q", dir, "Various Artists")
	}
}

// A track with no number should not be prefixed with "00 - ".
func TestKeepDestinationOmitsMissingTrackNumber(t *testing.T) {
	track := catalog.MusicTrack{Title: "Untitled Demo", DisplayArtist: "Someone", AlbumTitle: "Demos"}
	if base := filepath.Base(keepDestination("/m", track, "Demos", ".flac")); base != "Untitled Demo.flac" {
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
	dirs := []string{"/mnt/media/Music/explo/Weekly-Exploration"}

	if !underAnyDir("/mnt/media/Music/explo/Weekly-Exploration/a.flac", dirs) {
		t.Fatal("a file inside the drop folder must be keepable")
	}
	for _, outside := range []string{
		"/mnt/media/Music/Adele/25/01 - Hello.flac",
		"/mnt/media/Music/explo/Weekly-Exploration-Other/a.flac", // prefix, not a child
		"/etc/passwd",
		"/mnt/media/Music/explo/Weekly-Exploration", // the folder itself
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

// The kept copy has to carry samo's cover. An explo drop is an untagged rip
// with no embedded picture, and the art the app shows comes from samo's own
// identification — so a copy that only writes text tags lands in the library
// with no artwork at all.
func TestRemuxArgsEmbedsTheCover(t *testing.T) {
	args := remuxArgs("/drop/x.flac", "/lib/x.flac", "/covers/c.jpg", "Outlandos d\u2019Amour", catalog.MusicTrack{Title: "Roxanne"})
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-i /drop/x.flac -i /covers/c.jpg") {
		t.Fatalf("cover is not a second input: %s", joined)
	}
	// Audio from the source, picture from the cover: mapping all of input 0
	// alongside input 1 would leave two pictures on a file that had its own.
	if !strings.Contains(joined, "-map 0:a -map 1:v") {
		t.Fatalf("streams are not mapped audio-then-cover: %s", joined)
	}
	// Without this the picture is a plain video stream, which FLAC refuses.
	if !strings.Contains(joined, "-disposition:v:0 attached_pic") {
		t.Fatalf("cover is not marked as attached art: %s", joined)
	}
	if args[len(args)-1] != "/lib/x.flac" {
		t.Fatalf("destination must come last, got %q", args[len(args)-1])
	}
}

// With no cover to add, the source's own streams must still come across whole
// — a file that DID carry embedded art must not lose it.
func TestRemuxArgsWithoutCoverKeepsSourceStreams(t *testing.T) {
	joined := strings.Join(remuxArgs("/drop/x.flac", "/lib/x.flac", "", "Outlandos d\u2019Amour", catalog.MusicTrack{Title: "Roxanne"}), " ")
	if !strings.Contains(joined, "-map 0 -c copy") {
		t.Fatalf("source streams are not mapped whole: %s", joined)
	}
	if strings.Contains(joined, "attached_pic") {
		t.Fatalf("no cover was given, so nothing should be attached: %s", joined)
	}
}

// The tags samo holds as overrides are the point of remuxing rather than
// copying, so they have to reach the file alongside the cover.
func TestRemuxArgsWritesEffectiveTags(t *testing.T) {
	joined := strings.Join(remuxArgs("/a.flac", "/b.flac", "/c.jpg", "Outlandos D'Amour", catalog.MusicTrack{
		Title:            "Roxanne",
		DisplayArtist:    "The Police",
		AlbumTitle:       "Outlandos D'Amour",
		AlbumArtistNames: []string{"The Police"},
		TrackNumber:      3,
		ReleaseYear:      1978,
	}), " ")
	for _, want := range []string{
		"-metadata title=Roxanne",
		"-metadata artist=The Police",
		"-metadata album=Outlandos D'Amour",
		"-metadata album_artist=The Police",
		"-metadata track=3",
		"-metadata date=1978",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in: %s", want, joined)
		}
	}
}

// Keepable is what decides whether a surface offers "Keep in Library" at all,
// so a wrong answer is either a menu entry that always fails or a drop nobody
// is told they can save.
//
// db is a bare pointer: Keepable only checks it for nil, because a service
// without storage is a disabled one. Giving it a real database would test the
// harness rather than the rule.
func newKeepableService(dirs []string, track catalog.MusicTrack, found bool) *Service {
	return &Service{
		db:         new(sql.DB),
		dirs:       dirs,
		ffmpegPath: "/usr/bin/ffmpeg",
		trackByID: func(string) (catalog.MusicTrack, error) {
			if !found {
				return catalog.MusicTrack{}, errors.New("no such track")
			}
			return track, nil
		},
	}
}

func TestKeepableAcceptsDropFolderTrack(t *testing.T) {
	track := catalog.MusicTrack{AudioFiles: []catalog.AudioFile{{Path: "/drops/explo/Artist - Song.flac"}}}
	if !newKeepableService([]string{"/drops/explo"}, track, true).Keepable("t1") {
		t.Fatal("a track inside the explo folder should be keepable")
	}
}

// The case this whole feature turns on: a station whose music is the ordinary
// library — Christmas rotation in December, say — must not offer to keep
// anything, because there is nothing to save it from.
func TestKeepableRejectsOrdinaryLibraryTrack(t *testing.T) {
	track := catalog.MusicTrack{AudioFiles: []catalog.AudioFile{{Path: "/mnt/music/Wham!/Last Christmas.flac"}}}
	if newKeepableService([]string{"/drops/explo"}, track, true).Keepable("t1") {
		t.Fatal("a library track is already kept; it must not be offered")
	}
}

// A folder that merely shares a prefix is a different folder. Without this,
// "/drops/exploration" would be read as explo content.
func TestKeepableRejectsSiblingPrefixFolder(t *testing.T) {
	track := catalog.MusicTrack{AudioFiles: []catalog.AudioFile{{Path: "/drops/exploration/Song.flac"}}}
	if newKeepableService([]string{"/drops/explo"}, track, true).Keepable("t1") {
		t.Fatal("/drops/exploration is not under /drops/explo")
	}
}

// Every "no" answers false rather than erroring: the caller's only question is
// whether to draw a menu entry.
func TestKeepableRejectsWhenNothingCanKeep(t *testing.T) {
	inDrop := catalog.MusicTrack{AudioFiles: []catalog.AudioFile{{Path: "/drops/explo/Song.flac"}}}
	cases := []struct {
		name    string
		service *Service
		trackID string
	}{
		{"nil service", nil, "t1"},
		{"no explo folder configured", newKeepableService(nil, inDrop, true), "t1"},
		{"unknown track", newKeepableService([]string{"/drops/explo"}, inDrop, false), "t1"},
		{"empty id", newKeepableService([]string{"/drops/explo"}, inDrop, true), ""},
		{
			"nothing to copy",
			newKeepableService([]string{"/drops/explo"}, catalog.MusicTrack{}, true),
			"t1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.service.Keepable(tc.trackID) {
				t.Fatal("expected false")
			}
		})
	}
}

// Keep fails the whole batch without ffmpeg — it remuxes samo's tags and cover
// into the copy — so a server missing it must not offer the action either.
func TestKeepableRejectsWithoutFFmpeg(t *testing.T) {
	track := catalog.MusicTrack{AudioFiles: []catalog.AudioFile{{Path: "/drops/explo/Song.flac"}}}
	service := newKeepableService([]string{"/drops/explo"}, track, true)
	service.ffmpegPath = ""
	if service.Keepable("t1") {
		t.Fatal("without ffmpeg the keep would fail; do not offer it")
	}
}

// The duplicate this dedupe exists to stop, byte for byte as it happened:
// the library already held "Outlandos d’Amour/3 - Roxanne.flac" and Keep wrote
// "Outlandos D'Amour/03 - Roxanne.flac" beside it. Two apostrophes and a
// capital letter are not two recordings.
func TestNormalizeKeepIdentityFoldsApostropheAndCase(t *testing.T) {
	if got, want := normalizeKeepIdentity("Outlandos d’Amour"), normalizeKeepIdentity("Outlandos D'Amour"); got != want {
		t.Fatalf("apostrophe style still distinguishes albums: %q vs %q", got, want)
	}
	if got, want := normalizeKeepIdentity("So Easy (To Fall in Love)"), normalizeKeepIdentity("So Easy (To Fall In Love)"); got != want {
		t.Fatalf("title case still distinguishes tracks: %q vs %q", got, want)
	}
	// It must still tell genuinely different recordings apart — folding
	// punctuation is not licence to collapse a remix into its original.
	if normalizeKeepIdentity("Kids") == normalizeKeepIdentity("Kids (Soulwax remix)") {
		t.Fatal("a remix must not normalize onto the original")
	}
}

// The same release is credited "Artist" in one place and "Artist, Guest" in
// another; requiring equality would let that alone mint a duplicate.
func TestKeepArtistsMatchAllowsFeaturedCredits(t *testing.T) {
	postMalone := normalizeKeepIdentity("Post Malone")
	withGuest := normalizeKeepIdentity("Post Malone & Swae Lee")
	if !keepArtistsMatch(postMalone, withGuest) {
		t.Fatal("a featured credit must still match the primary artist")
	}
	if keepArtistsMatch(postMalone, normalizeKeepIdentity("Olivia Dean")) {
		t.Fatal("unrelated artists must not match")
	}
	if keepArtistsMatch("", postMalone) {
		t.Fatal("an empty artist must never match")
	}
}

// The album a kept copy is filed under is the RESOLVED one, never the
// catalog's — which for a drop is the sharer's compilation tag or the drop
// folder's own name.
func TestKeepDestinationUsesResolvedAlbumNotCatalogTitle(t *testing.T) {
	track := catalog.MusicTrack{
		Title:            "Kids",
		DisplayArtist:    "MGMT",
		AlbumArtistNames: []string{"MGMT"},
		AlbumTitle:       "Weekly-Exploration", // what the scanner read off the folder
		TrackNumber:      3,
	}
	got := keepDestination("/mnt/music", track, "Oracular Spectacular", ".flac")
	want := filepath.Join("/mnt/music", "MGMT", "Oracular Spectacular", "03 - Kids.flac")
	if got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

// A drop folder name is never an album. Keeping such a track would put
// "Weekly-Exploration" in the library permanently, so it fails with a reason
// instead.
func TestKeepAlbumTitleRefusesTheDropFolderName(t *testing.T) {
	service := &Service{
		dirs:   []string{"/mnt/media/Music/explo/Weekly-Exploration"},
		logger: func(string, ...any) {},
	}
	_, err := service.keepAlbumTitle(context.Background(), "track-1", catalog.MusicTrack{
		Title:      "Kids",
		AlbumTitle: "Weekly-Exploration",
	})
	if err == nil {
		t.Fatal("keeping a track whose album is the drop folder must fail")
	}
	if !strings.Contains(err.Error(), "Weekly-Exploration") {
		t.Fatalf("error should name what it refused to write, got %v", err)
	}
	// A real album tag is still usable when MusicBrainz has nothing to say.
	got, err := service.keepAlbumTitle(context.Background(), "track-2", catalog.MusicTrack{
		Title:      "Roxanne",
		AlbumTitle: "Outlandos d'Amour",
	})
	if err != nil || got != "Outlandos d'Amour" {
		t.Fatalf("got (%q, %v), want (Outlandos d'Amour, nil)", got, err)
	}
}

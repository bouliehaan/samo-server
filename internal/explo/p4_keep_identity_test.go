package explo

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// One rule for the artist, shared with the now-playing card: display artist,
// then the credited artists, then the album artist. The folder Keep files
// under and the album_artist tag come from the same function.
func TestKeptArtistFollowsTheCardsRule(t *testing.T) {
	cases := []struct {
		name             string
		track            catalog.MusicTrack
		wantTrack, wantA string
	}{
		{"display artist shown", catalog.MusicTrack{DisplayArtist: "Radiohead", ArtistNames: []string{"Radiohead"}}, "Radiohead", "Radiohead"},
		{"credited artists when no display artist", catalog.MusicTrack{ArtistNames: []string{"Ye", "Pusha T"}}, "Ye, Pusha T", "Ye, Pusha T"},
		{"album artist only as a last resort", catalog.MusicTrack{AlbumArtistNames: []string{"Various Artists"}}, "Various Artists", "Various Artists"},
		{"album artist files the compilation", catalog.MusicTrack{DisplayArtist: "Post Malone & Swae Lee", AlbumArtistNames: []string{"Various Artists"}}, "Post Malone & Swae Lee", "Various Artists"},
	}
	for _, tc := range cases {
		if got := keptTrackArtist(tc.track); got != tc.wantTrack {
			t.Errorf("%s: keptTrackArtist = %q, want %q", tc.name, got, tc.wantTrack)
		}
		if got := keptAlbumArtist(tc.track); got != tc.wantA {
			t.Errorf("%s: keptAlbumArtist = %q, want %q", tc.name, got, tc.wantA)
		}
		dest := keepDestination("/m", tc.track, "Album", ".flac")
		if folder := filepath.Base(filepath.Dir(filepath.Dir(dest))); folder != safeComponent(tc.wantA, "Unknown Artist") {
			t.Errorf("%s: filed under %q, want the album artist %q", tc.name, folder, tc.wantA)
		}
	}
}

// The identity written into the copy is the one the listener saw. With no
// album artist in the catalog — the shape every explo drop has — the
// album_artist tag is the displayed artist, written explicitly: left blank it
// was whatever the sharer's file said, and the library filed "Creep" by
// Klangsberg under an album credited to Radiohead.
func TestRemuxArgsWritesTheDisplayedArtistAsAlbumArtist(t *testing.T) {
	joined := strings.Join(remuxArgs("/drop/x.flac", "/lib/x.flac.samo-keep-tmp", "flac", "", keptAlbum{Title: "Pablo Honey"}, catalog.MusicTrack{
		Title:         "Creep",
		DisplayArtist: "Radiohead",
	}), " ")
	for _, want := range []string{"-metadata artist=Radiohead", "-metadata album_artist=Radiohead", "-metadata album=Pablo Honey"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in: %s", want, joined)
		}
	}
}

// For an identified drop every MusicBrainz identity tag the sharer's file
// carried is deleted (an empty -metadata value), in each spelling ffmpeg
// surfaces, and samo's release group is written after the deletions so it
// survives them. The recording id samo knows is written as before.
func TestRemuxArgsDropsTheSourcesIdentityForAnIdentifiedDrop(t *testing.T) {
	args := remuxArgs("/drop/x.flac", "/lib/x.flac.samo-keep-tmp", "flac", "", keptAlbum{Title: "Pablo Honey", ReleaseGroupID: "rg-pablo-honey", Identified: true}, catalog.MusicTrack{
		Title:         "Creep",
		DisplayArtist: "Radiohead",
		ExternalIDs:   catalog.ExternalIDs{MusicBrainzRecordingID: "rec-creep"},
	})
	index := func(value string) int {
		for i, arg := range args {
			if arg == value {
				return i
			}
		}
		return -1
	}
	for _, key := range []string{
		"musicbrainz_albumid", "musicbrainz_releaseid", "MusicBrainz Album Id",
		"musicbrainz_artistid", "MusicBrainz Artist Id",
		"musicbrainz_albumartistid", "MusicBrainz Album Artist Id",
		"musicbrainz_releasegroupid", "MusicBrainz Release Group Id",
		"musicbrainz_releasetrackid", "musicbrainz_workid",
	} {
		if i := index(key + "="); i < 1 || args[i-1] != "-metadata" {
			t.Fatalf("%q is not deleted: %v", key, args)
		}
	}
	set := index("musicbrainz_releasegroupid=rg-pablo-honey")
	if set < 0 || args[set-1] != "-metadata" {
		t.Fatalf("samo's release group is not written: %v", args)
	}
	if cleared := index("musicbrainz_releasegroupid="); cleared > set {
		t.Fatal("the release group is deleted after it is written, so the copy ends up without one")
	}
	if i := index("musicbrainz_trackid=rec-creep"); i < 0 {
		t.Fatalf("the recording id is not written: %v", args)
	}
}

// A drop that was never identified has nothing of samo's to write over its
// tags: they are all it has, and they stay.
func TestRemuxArgsLeavesAnUnidentifiedDropsTagsAlone(t *testing.T) {
	joined := strings.Join(remuxArgs("/drop/x.flac", "/lib/x.flac.samo-keep-tmp", "flac", "", keptAlbum{Title: "Outlandos d'Amour"}, catalog.MusicTrack{
		Title:         "Roxanne",
		DisplayArtist: "The Police",
	}), " ")
	for _, banned := range []string{"musicbrainz_albumid=", "musicbrainz_artistid=", "musicbrainz_releasegroupid", "MusicBrainz"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("an unidentified drop's identity tags were touched (%q): %s", banned, joined)
		}
	}
}

// The real thing: a source tagged with one identity, remuxed as another, read
// back with ffprobe. The sharer's MusicBrainz ids are gone in the spelling
// each container uses, samo's ids and names are there, and a tag that is not
// an identity survives the copy. This is what the scanner will read, so it is
// the album and artist the library will show.
func TestKeepRemuxReplacesTheSourcesIdentityOnDisk(t *testing.T) {
	ffmpeg := ffmpegOrSkip(t)
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	cases := []struct {
		ext, codec, format string
		// how this container spells the sharer's ids
		albumID, artistID string
	}{
		{".flac", "flac", "flac", "MUSICBRAINZ_ALBUMID", "MUSICBRAINZ_ARTISTID"},
		{".mp3", "libmp3lame", "mp3", "MusicBrainz Album Id", "MusicBrainz Artist Id"},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			f := newKeepFixture(t, tc.ext)
			synthesize(t, ffmpeg, "-f", "lavfi", "-i", "sine=f=440:d=0.2", "-c:a", tc.codec,
				"-metadata", "artist=Radiohead",
				"-metadata", "album_artist=Radiohead",
				"-metadata", "album=Pablo Honey",
				"-metadata", tc.albumID+"=5df336df-cb28-4147-a126-ba414820f02f",
				"-metadata", tc.artistID+"=a74b1b7f-71a5-4011-9441-d0b5e4122711",
				"-metadata", "composer=Thom Yorke",
				f.source)

			track := f.track
			track.Title = "Radiohead - Creep"
			track.DisplayArtist = "Klangsberg"
			track.ExternalIDs = catalog.ExternalIDs{MusicBrainzRecordingID: "rec-klangsberg"}
			album := keptAlbum{Title: "Album", ReleaseGroupID: "rg-klangsberg", Identified: true}
			output := filepath.Join(f.albumDir, "copy"+tc.ext)
			if err := f.service(ffmpeg).remuxWithTags(context.Background(), f.source, output, tc.format, track, album, ""); err != nil {
				t.Fatal(err)
			}

			out, err := exec.Command(ffprobe, "-v", "error", "-show_format", "-print_format", "json", output).Output()
			if err != nil {
				t.Fatal(err)
			}
			var probe struct {
				Format struct {
					Tags map[string]string `json:"tags"`
				} `json:"format"`
			}
			if err := json.Unmarshal(out, &probe); err != nil {
				t.Fatal(err)
			}
			tags := map[string]string{}
			for key, value := range probe.Format.Tags {
				tags[strings.ToLower(strings.ReplaceAll(key, " ", "_"))] = value
			}
			for key, want := range map[string]string{
				"artist":                     "Klangsberg",
				"album_artist":               "Klangsberg",
				"album":                      "Album",
				"musicbrainz_releasegroupid": "rg-klangsberg",
				"musicbrainz_trackid":        "rec-klangsberg",
				"composer":                   "Thom Yorke",
			} {
				if got := tags[key]; got != want {
					t.Errorf("%s = %q, want %q (tags: %v)", key, got, want, tags)
				}
			}
			for _, gone := range []string{"musicbrainz_albumid", "musicbrainz_album_id", "musicbrainz_artistid", "musicbrainz_artist_id"} {
				if value, ok := tags[gone]; ok && value != "" {
					t.Errorf("the sharer's %s survived the copy: %q", gone, value)
				}
			}
		})
	}
}

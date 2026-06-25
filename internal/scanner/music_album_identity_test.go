package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestResolveMusicAlbumArtistNamesPrefersTagOverTrackArtist(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"artist":       "Young Thug",
		"album_artist": "Kanye West",
	})
	names := resolveMusicAlbumArtistNames(tags, musicAlbumSidecar{})
	if len(names) != 1 || names[0] != "Kanye West" {
		t.Fatalf("names = %#v, want Kanye West", names)
	}
}

func TestResolveMusicAlbumArtistNamesUsesSidecarBeforeTrackArtist(t *testing.T) {
	tags := normalizeTags(map[string]string{"artist": "Young Thug"})
	names := resolveMusicAlbumArtistNames(tags, musicAlbumSidecar{AlbumArtist: "Kanye West"})
	if len(names) != 1 || names[0] != "Kanye West" {
		t.Fatalf("names = %#v, want sidecar album artist", names)
	}
}

func TestResolveMusicAlbumArtistNamesDoesNotUseTrackArtist(t *testing.T) {
	tags := normalizeTags(map[string]string{"artist": "Young Thug"})
	names := resolveMusicAlbumArtistNames(tags, musicAlbumSidecar{})
	if names != nil {
		t.Fatalf("names = %#v, want nil so metadata/folder identity is used", names)
	}
}

func TestResolveMusicAlbumArtistNamesUsesCompilationFallback(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"artist":      "Young Thug",
		"compilation": "1",
	})
	names := resolveMusicAlbumArtistNames(tags, musicAlbumSidecar{})
	if len(names) != 1 || names[0] != "Various Artists" {
		t.Fatalf("names = %#v, want Various Artists", names)
	}
}

func TestResolveMusicAlbumArtistNamesSplitsMultiValueSidecar(t *testing.T) {
	tags := normalizeTags(map[string]string{"artist": "Young Thug"})
	names := resolveMusicAlbumArtistNames(tags, musicAlbumSidecar{AlbumArtist: "Ye; Young Thug"})
	if len(names) != 2 || names[0] != "Ye" || names[1] != "Young Thug" {
		t.Fatalf("names = %#v, want Ye and Young Thug", names)
	}
}

func TestResolveMusicAlbumIDUsesMusicBrainzReleaseGroup(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"musicbrainz_releasegroupid": "group-integral",
	})
	id := resolveMusicAlbumID(tags, "Integral", "Miles Davis/Integral", []string{"Miles Davis"})
	want := stableID("album", "mbgroup", "group-integral")
	if id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
}

func TestResolveMusicAlbumIDGroupsTracksWithSameReleaseGroup(t *testing.T) {
	albumTitle := "Integral"
	relDir := "Miles Davis/Integral"
	artists := []string{"Miles Davis"}

	trackA := resolveMusicAlbumID(
		normalizeTags(map[string]string{
			"musicbrainz_releasegroupid": "group-integral",
			"title":                      "Track A",
		}),
		albumTitle,
		relDir,
		artists,
	)
	trackB := resolveMusicAlbumID(
		normalizeTags(map[string]string{
			"musicbrainz_releasegroupid": "group-integral",
			"title":                      "Track B",
		}),
		albumTitle,
		relDir,
		artists,
	)
	if trackA != trackB {
		t.Fatalf("expected one album id for matching release group, got %q and %q", trackA, trackB)
	}
}

func TestResolveMusicAlbumIDSplitsDifferentReleaseGroups(t *testing.T) {
	albumTitle := "Integral"
	relDir := "Miles Davis/Integral"
	artists := []string{"Miles Davis"}

	a := resolveMusicAlbumID(
		normalizeTags(map[string]string{"musicbrainz_releasegroupid": "group-a"}),
		albumTitle,
		relDir,
		artists,
	)
	b := resolveMusicAlbumID(
		normalizeTags(map[string]string{"musicbrainz_releasegroupid": "group-b"}),
		albumTitle,
		relDir,
		artists,
	)
	if a == b {
		t.Fatalf("different release groups should not share album id: %q", a)
	}
}

func TestResolveMusicAlbumIDUsesMusicBrainzReleaseID(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"musicbrainz_albumid": "release-integral-2011",
	})
	id := resolveMusicAlbumID(tags, "Integral", "Miles Davis/Integral", []string{"Miles Davis"})
	want := stableID("album", "mbrelease", "release-integral-2011")
	if id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
}

func TestResolveMusicAlbumIDGroupsTracksWithSameReleaseID(t *testing.T) {
	albumTitle := "Quiet Nights"
	relDir := "Miles Davis/Quiet Nights"
	artists := []string{"Miles Davis"}

	a := resolveMusicAlbumID(
		normalizeTags(map[string]string{
			"musicbrainz_albumid": "release-quiet-nights",
			"title":               "1. Track",
		}),
		albumTitle,
		relDir,
		artists,
	)
	b := resolveMusicAlbumID(
		normalizeTags(map[string]string{
			"musicbrainz_albumid": "release-quiet-nights",
			"title":               "2. Track",
		}),
		albumTitle,
		relDir,
		artists,
	)
	if a != b {
		t.Fatalf("expected one album id for matching release id, got %q and %q", a, b)
	}
}

func TestResolveMusicAlbumIDSplitsDifferentReleaseIDs(t *testing.T) {
	albumTitle := "Integral"
	relDir := "Miles Davis/Integral"
	artists := []string{"Miles Davis"}

	a := resolveMusicAlbumID(
		normalizeTags(map[string]string{"musicbrainz_albumid": "release-a"}),
		albumTitle,
		relDir,
		artists,
	)
	b := resolveMusicAlbumID(
		normalizeTags(map[string]string{"musicbrainz_albumid": "release-b"}),
		albumTitle,
		relDir,
		artists,
	)
	if a == b {
		t.Fatal("different release ids should not share album id")
	}
}

func TestResolveMusicAlbumIDUsesAlbumArtistAndTitleWithoutMusicBrainz(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"album":        "Quiet Nights",
		"album_artist": "Miles Davis",
	})
	id := resolveMusicAlbumID(tags, "Quiet Nights", "some/other/dir", []string{"Miles Davis"})
	want := stableID("album", "meta", "miles davis", "quiet nights")
	if id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
}

func TestResolveMusicAlbumIDMetaGroupsSameYearDifferentFormats(t *testing.T) {
	artists := []string{"Miles Davis"}
	title := "Quiet Nights"
	a := resolveMusicAlbumID(
		normalizeTags(map[string]string{"album_artist": "Miles Davis", "date": "1964"}),
		title, "Miles Davis/Quiet Nights", artists,
	)
	b := resolveMusicAlbumID(
		normalizeTags(map[string]string{"album_artist": "Miles Davis", "date": "1964-08-04"}),
		title, "Miles Davis/Quiet Nights", artists,
	)
	if a != b {
		t.Fatalf("expected one meta album id for same year, got %q and %q", a, b)
	}
}

func TestResolveMusicAlbumIDUsesFolderArtistWhenUntagged(t *testing.T) {
	albumTitle := "Bootleg"
	relDir := "Miles Davis/Bootleg"
	id := resolveMusicAlbumID(catalog.Tags{}, albumTitle, relDir, nil)
	want := stableID("album", "meta", "miles davis", "bootleg")
	if id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
}

func TestResolveMusicAlbumIDCollapsesDiscSubfolders(t *testing.T) {
	albumTitle := "INTEGRAL MILES DAVIS 1951-1956"
	tags := catalog.Tags{}

	disc1 := resolveMusicAlbumID(tags, albumTitle, "Miles Davis/Integral Miles Davis/Disc 1", nil)
	disc3 := resolveMusicAlbumID(tags, albumTitle, "Miles Davis/Integral Miles Davis/Disc 3", nil)
	if disc1 != disc3 {
		t.Fatalf("expected one album id for multi-disc folder layout, got %q and %q", disc1, disc3)
	}
	want := stableID("album", "meta", "miles davis", "integral miles davis 1951-1956")
	if disc1 != want {
		t.Fatalf("id = %q, want %q", disc1, want)
	}
}

func TestResolveMusicAlbumIDNavidromeMetaFallbackIncludesVersionAndDate(t *testing.T) {
	tagsA := normalizeTags(map[string]string{
		"album_artist": "Miles Davis",
		"albumversion": "Original",
		"date":         "1964",
	})
	tagsB := normalizeTags(map[string]string{
		"album_artist": "Miles Davis",
		"albumversion": "Remaster",
		"date":         "1964",
	})
	artists := []string{"Miles Davis"}
	title := "Quiet Nights"

	a := resolveMusicAlbumID(tagsA, title, "Miles Davis/Quiet Nights", artists)
	b := resolveMusicAlbumID(tagsB, title, "Miles Davis/Quiet Nights", artists)
	if a == b {
		t.Fatal("Navidrome-style meta ids should split when albumversion differs")
	}
}

func TestResolveMusicAlbumIDGroupsIntegralStyleTaggedBoxSet(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"album":        "INTEGRAL MILES DAVIS 1951-1956",
		"album_artist": "Miles Davis",
		"date":         "2024-04-19",
	})
	artists := []string{"Miles Davis"}
	title := "INTEGRAL MILES DAVIS 1951-1956"

	disc1 := resolveMusicAlbumID(tags, title, "Miles Davis/Integral Miles Davis/Disc 1", artists)
	disc10 := resolveMusicAlbumID(tags, title, "Miles Davis/Integral Miles Davis/Disc 10", artists)
	if disc1 != disc10 {
		t.Fatalf("expected one album id for tagged integral box set, got %q and %q", disc1, disc10)
	}
}

func TestAlbumIdentityDirCollapsesCDFolders(t *testing.T) {
	if got := albumIdentityDir("Artist/Album/CD 2"); got != "Artist/Album" {
		t.Fatalf("got %q, want Artist/Album", got)
	}
	if got := albumIdentityDir("Artist/Album"); got != "Artist/Album" {
		t.Fatalf("got %q, want Artist/Album", got)
	}
}

func TestResolveMusicAlbumIDUsesFolderArtistForUntaggedTracksRegardlessOfTrackArtist(t *testing.T) {
	albumTitle := "The Life of Pablo"
	relDir := "Kanye West/The Life of Pablo"

	kanyeTagged := resolveMusicAlbumID(
		normalizeTags(map[string]string{"artist": "Kanye West"}),
		albumTitle,
		relDir,
		nil,
	)
	thugTagged := resolveMusicAlbumID(
		normalizeTags(map[string]string{"artist": "Young Thug"}),
		albumTitle,
		relDir,
		nil,
	)
	want := stableID("album", "meta", "kanye west", "the life of pablo")
	if kanyeTagged != want || thugTagged != want {
		t.Fatalf("expected one album id via folder artist, got kanye=%q thug=%q want=%q", kanyeTagged, thugTagged, want)
	}
}

func TestResolveMusicAlbumDisplayArtistUsesJoinedAlbumArtists(t *testing.T) {
	display := resolveMusicAlbumDisplayArtist(
		normalizeTags(map[string]string{
			"albumartists": "Ye;Young Thug",
		}),
		musicAlbumSidecar{},
		[]string{"Ye", "Young Thug"},
	)
	if display != "Ye, Young Thug" {
		t.Fatalf("display = %q, want Ye, Young Thug", display)
	}
}

func TestResolveMusicAlbumDisplayArtistDoesNotUseTrackArtist(t *testing.T) {
	display := resolveMusicAlbumDisplayArtist(
		catalog.Tags{},
		musicAlbumSidecar{},
		nil,
	)
	if display != "" {
		t.Fatalf("display = %q, want empty without album artist metadata", display)
	}
}

func TestShouldScanAudioFileSkipsAppleDoubleSidecars(t *testing.T) {
	if shouldScanAudioFile("/music/album/._01 - Song.flac") {
		t.Fatal("expected AppleDouble sidecar to be skipped")
	}
	if !shouldScanAudioFile("/music/album/01 - Song.flac") {
		t.Fatal("expected normal audio file to be scanned")
	}
}

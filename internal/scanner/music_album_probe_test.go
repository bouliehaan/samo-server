package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProbeMusicDirectoryAlbumGrouping walks SAMO_TEST_MUSIC_DIR and reports how
// files group by resolveMusicAlbumID. Set the env var to a folder (e.g. an album
// directory) to inspect real MusicBrainz tags:
//
//	SAMO_TEST_MUSIC_DIR="/path/to/Miles Davis/Integral" go test ./internal/scanner -run TestProbeMusicDirectoryAlbumGrouping -v
func TestProbeMusicDirectoryAlbumGrouping(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("SAMO_TEST_MUSIC_DIR"))
	if root == "" {
		t.Skip("set SAMO_TEST_MUSIC_DIR to an album folder to probe real tags")
	}

	scanner := &Scanner{}
	groups := map[string][]string{}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !shouldScanAudioFile(path) {
			return nil
		}

		probe, err := scanner.probeMusic(context.Background(), path)
		if err != nil {
			t.Logf("skip %s: probe: %v", path, err)
			return nil
		}

		relAlbumDir, _ := filepath.Rel(root, filepath.Dir(path))
		if relAlbumDir == "." {
			relAlbumDir = ""
		}
		tags := probe.Tags
		albumTitle := firstNonEmpty(firstTag(tags, "album"), filepath.Base(filepath.Dir(path)))
		albumArtistNames := resolveMusicAlbumArtistNames(tags, readMusicAlbumSidecar(filepath.Dir(path)))
		albumID := resolveMusicAlbumID(tags, albumTitle, relAlbumDir, albumArtistNames)

		mbGroup := firstTag(tags, "musicbrainz_releasegroupid", "musicbrainz_albumgroupid")
		mbRelease := firstTag(tags, "musicbrainz_albumid", "musicbrainz_releaseid")
		trackTitle := firstTag(tags, "title")

		groups[albumID] = append(groups[albumID], filepath.Base(path))
		t.Logf(
			"file=%s title=%q album=%q album_artist=%q mb_group=%q mb_release=%q -> album_id=%s",
			filepath.Base(path),
			trackTitle,
			albumTitle,
			strings.Join(albumArtistNames, "; "),
			mbGroup,
			mbRelease,
			albumID,
		)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	t.Logf("distinct album ids under %s: %d", root, len(groups))
	for albumID, files := range groups {
		t.Logf("  album_id=%s tracks=%d", albumID, len(files))
	}
	if len(groups) > 1 {
		t.Logf("multiple album ids in one folder — check MusicBrainz tags (release group / release id should match across tracks)")
	}
}

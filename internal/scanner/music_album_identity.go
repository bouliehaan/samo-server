package scanner

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// discSubdirPattern matches common multi-disc folder names (Disc 1, CD2, etc.).
// Navidrome's PID.Album="folder" requires a flat album folder; we instead collapse
// these subdirs when falling back to path-based ids so box sets stay grouped.
var discSubdirPattern = regexp.MustCompile(`(?i)^(cd|disc|disk)\s*\d+$`)

// resolveMusicAlbumArtistNames picks the album-artist identity used for
// grouping. Track artist (TPE1) is intentionally not used as a fallback here:
// when album_artist is missing, per-track performers would otherwise mint a
// different album id for every featured artist on the same release.
func resolveMusicAlbumArtistNames(
	tags catalog.Tags,
	sidecar musicAlbumSidecar,
) []string {
	if names := splitTag(tags, "album_artist", "albumartist"); len(names) > 0 {
		return names
	}
	if sidecar.AlbumArtist != "" {
		return splitPeopleString(sidecar.AlbumArtist)
	}
	if sidecar.Artist != "" {
		return splitPeopleString(sidecar.Artist)
	}
	if boolTag(tags, "compilation", "itunescompilation", "tcmp") {
		return []string{"Various Artists"}
	}
	return nil
}

// resolveMusicAlbumID picks a stable album key from embedded metadata first,
// aligned with Navidrome's default PID.Album:
//
//	musicbrainz_albumid | albumartistid,album,albumversion,releasedate
//
// musicbrainz_releasegroupid is checked when no release id is present so tagged
// box sets with per-disc release ids still merge. Folder path is only a last
// resort for untagged files, with Disc/CD subfolders collapsed to the parent.
func resolveMusicAlbumID(
	tags catalog.Tags,
	albumTitle string,
	relAlbumDir string,
	albumArtistNames []string,
) string {
	if mbRelease := firstTag(tags, "musicbrainz_albumid", "musicbrainz_releaseid"); mbRelease != "" {
		return stableID("album", "mbrelease", mbRelease)
	}
	if mbGroup := firstTag(
		tags,
		"musicbrainz_releasegroupid",
		"musicbrainz_albumgroupid",
	); mbGroup != "" {
		return stableID("album", "mbgroup", mbGroup)
	}

	title := normalizeAlbumIdentityText(albumTitle)
	if title == "" {
		title = normalizeAlbumIdentityText(filepath.Base(relAlbumDir))
	}
	if artistKey := albumIdentityArtistKey(tags, albumArtistNames, relAlbumDir); artistKey != "" && title != "" {
		return albumMetaID(artistKey, title, tags)
	}

	relDir := albumIdentityDir(relAlbumDir)
	return stableID("album", "dir", relDir, title)
}

// albumMetaID mirrors Navidrome's metadata fallback: album artist + album +
// version + release date must all match for tracks to share an album.
func albumMetaID(artistKey, title string, tags catalog.Tags) string {
	parts := []string{artistKey, title}
	// Navidrome maps albumversion only — not per-track VERSION (see navidrome#4029).
	if version := normalizeAlbumIdentityText(firstTag(tags, "albumversion")); version != "" {
		parts = append(parts, version)
	}
	if date := albumIdentityReleaseDateKey(tags); date != "" {
		parts = append(parts, date)
	}
	all := append([]string{"album", "meta"}, parts...)
	return stableID(all[0], all[1:]...)
}

func albumIdentityArtistKey(tags catalog.Tags, albumArtistNames []string, relAlbumDir string) string {
	if len(albumArtistNames) > 0 {
		return normalizeAlbumIdentityText(strings.Join(albumArtistNames, "; "))
	}
	if mb := firstTag(tags, "musicbrainz_albumartistid", "musicbrainz_albumartist_id"); mb != "" {
		return normalizeAlbumIdentityText(mb)
	}
	if tag := firstTag(tags, "album_artist", "albumartist"); tag != "" {
		return normalizeAlbumIdentityText(tag)
	}
	return albumIdentityFolderArtist(relAlbumDir)
}

// albumIdentityReleaseDateKey normalizes release date for grouping. Navidrome's
// PID uses releasedate only; normalizing to a four-digit year keeps "1964" and
// "1964-01-01" on the same album instead of minting duplicates per track.
func albumIdentityReleaseDateKey(tags catalog.Tags) string {
	raw := firstTag(tags, "releasedate", "release_date", "originaldate", "originalyear", "date", "year")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) >= 4 && raw[0] >= '0' && raw[0] <= '9' {
		year := raw[:4]
		if year[0] >= '0' && year[0] <= '9' && year[1] >= '0' && year[1] <= '9' &&
			year[2] >= '0' && year[2] <= '9' && year[3] >= '0' && year[3] <= '9' {
			return year
		}
	}
	return normalizeAlbumIdentityText(raw)
}

// albumIdentityFolderArtist uses the top-level library-relative folder as a
// stand-in album artist when tags omit album_artist (Artist/Album/Track layout).
func albumIdentityFolderArtist(relAlbumDir string) string {
	rel := strings.TrimSpace(filepath.ToSlash(relAlbumDir))
	if rel == "" || rel == "." {
		return ""
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return ""
	}
	return normalizeAlbumIdentityText(parts[0])
}

// albumIdentityDir returns the library-relative directory used for path-based
// album grouping. Disc/CD subfolders are collapsed to their parent so
// …/Integral Miles Davis/Disc 3 does not become a separate album from Disc 1.
func albumIdentityDir(relAlbumDir string) string {
	relAlbumDir = strings.TrimSpace(filepath.ToSlash(relAlbumDir))
	if relAlbumDir == "" || relAlbumDir == "." {
		return relAlbumDir
	}
	base := filepath.Base(relAlbumDir)
	if discSubdirPattern.MatchString(base) {
		parent := filepath.Dir(relAlbumDir)
		if parent != "." && parent != "" {
			return parent
		}
	}
	return relAlbumDir
}

func normalizeAlbumIdentityText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

func resolveMusicAlbumDisplayArtist(
	tags catalog.Tags,
	sidecar musicAlbumSidecar,
	albumArtistNames []string,
) string {
	if joined := strings.Join(albumArtistNames, ", "); joined != "" {
		return joined
	}
	if names := splitTag(tags, "albumartists", "album_artist_display"); len(names) > 0 {
		return strings.Join(names, ", ")
	}
	return firstNonEmpty(
		sidecar.AlbumArtist,
		sidecar.Artist,
	)
}

func albumArtistsExplicitFromTags(tags catalog.Tags, sidecar musicAlbumSidecar) bool {
	if len(splitTag(tags, "album_artist", "albumartist")) > 0 {
		return true
	}
	return sidecar.AlbumArtist != ""
}

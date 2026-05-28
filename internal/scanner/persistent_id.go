package scanner

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Navidrome default PID specs (consts.DefaultAlbumPID / DefaultTrackPID).
const (
	defaultAlbumPID = "musicbrainz_albumid|albumartistid,album,albumversion,releasedate"
	defaultTrackPID = "musicbrainz_trackid|albumid,discnumber,tracknumber,title"
)

// computeTrackPID mirrors Navidrome's metadata.trackPID: try each pipe-separated
// field group; use the first group with any non-empty attribute.
func computeTrackPID(libraryID string, relPath string, tags catalog.Tags, albumID string) string {
	spec := defaultTrackPID
	pid := ""
	for field := range strings.SplitSeq(spec, "|") {
		attrs := strings.Split(field, ",")
		values := make([]string, len(attrs))
		hasValue := false
		for i, attr := range attrs {
			v := trackPIDAttr(libraryID, relPath, tags, albumID, attr)
			if v != "" {
				hasValue = true
			}
			values[i] = v
		}
		if hasValue {
			pid = strings.Join(values, "\\")
			break
		}
	}
	if pid == "" {
		pid = relPath
	}
	return stableID("pid", libraryID, pid)
}

func trackPIDAttr(libraryID, relPath string, tags catalog.Tags, albumID, attr string) string {
	switch strings.TrimSpace(strings.ToLower(attr)) {
	case "albumid":
		if albumID != "" {
			return albumID
		}
		return ""
	case "discnumber":
		disc, _ := parseNumberPair(firstTag(tags, "disc", "discnumber"))
		if disc > 0 {
			return strconv.Itoa(disc)
		}
		return ""
	case "tracknumber":
		track, _ := parseNumberPair(firstTag(tags, "track", "tracknumber"))
		if track > 0 {
			return strconv.Itoa(track)
		}
		return ""
	case "title":
		title := firstTag(tags, "title")
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
		}
		return normalizeAlbumIdentityText(title)
	default:
		return firstTag(tags, attr)
	}
}

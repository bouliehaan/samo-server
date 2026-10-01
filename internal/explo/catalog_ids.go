package explo

import (
	"strconv"
	"strings"
)

// Search for new finds music in two catalogs. MusicBrainz ids are what
// identification checks a download against; Deezer ids, "deezer-<n>", name
// music MusicBrainz does not list at all. A Deezer id is never written where a
// MusicBrainz id belongs — not as identification evidence, not in the ledger,
// not in a kept file's tags — because it would name a recording that does not
// exist there.
//
// A YouTube Music playlist imported through Explo brings a third kind,
// "youtube-<video>": a track of that playlist, downloaded from the playlist's
// own video. It names a song only, never an album, and like a Deezer id it is
// never written where a MusicBrainz id belongs.

const (
	deezerIDPrefix  = "deezer-"
	youtubeIDPrefix = "youtube-"
)

// MusicBrainzID reports whether id is shaped like a MusicBrainz id.
func MusicBrainzID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// DeezerID is the Deezer number in a deezer-<n> id, or "".
func DeezerID(id string) string {
	n, ok := strings.CutPrefix(id, deezerIDPrefix)
	if !ok || n == "" || len(n) > 20 {
		return ""
	}
	if _, err := strconv.ParseUint(n, 10, 64); err != nil {
		return ""
	}
	return n
}

// YouTubeID is the video in a youtube-<video> id, or "".
func YouTubeID(id string) string {
	video, ok := strings.CutPrefix(id, youtubeIDPrefix)
	if !ok || len(video) != 11 {
		return ""
	}
	for _, c := range video {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return video
}

// ValidCatalogID reports whether id names a song or album Search for new can
// ask for: a MusicBrainz id or a Deezer one.
func ValidCatalogID(id string) bool {
	return MusicBrainzID(id) || DeezerID(id) != ""
}

// ValidSongID reports whether id names a song samo may have asked Explo for:
// a catalog id, or a track of an imported YouTube Music playlist.
func ValidSongID(id string) bool {
	return ValidCatalogID(id) || YouTubeID(id) != ""
}

// listingSource names the catalog whose listing may identify a requested
// download that samo cannot recognise, or "" when only identification may.
// A Deezer track always may: MusicBrainz does not list it, so nothing else
// ever will. A YouTube Music track may only when YouTube Music lists it as a
// song, which Explo marks by giving it an album; a music video or anyone's
// upload is named by whoever uploaded it.
func listingSource(recordingID, album string) (source, name string) {
	switch {
	case DeezerID(recordingID) != "":
		return "deezer", "Deezer"
	case YouTubeID(recordingID) != "" && strings.TrimSpace(album) != "":
		return "youtube-music", "YouTube Music"
	}
	return "", ""
}

// musicBrainzOnly is id when it is a MusicBrainz id, or "".
func musicBrainzOnly(id string) string {
	if MusicBrainzID(id) {
		return id
	}
	return ""
}

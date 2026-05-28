package subsonic

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func toArtist(item catalog.MusicArtist) artist {
	return artist{
		ID:         item.ID,
		Name:       item.Name,
		AlbumCount: item.AlbumCount,
		CoverArt:   item.ID,
	}
}

func toArtistAlbumChild(album catalog.MusicAlbum) child {
	return child{
		ID:       album.ID,
		Parent:   firstID(album.AlbumArtistIDs),
		Title:    album.Title,
		IsDir:    true,
		Album:    album.Title,
		Artist:   displayArtist(album.DisplayArtist, album.AlbumArtistNames),
		ArtistID: firstID(album.AlbumArtistIDs),
		Year:     album.ReleaseYear,
		Genre:    firstGenre(album.Genres),
		CoverArt: album.ID,
		Duration: album.DurationSeconds,
	}
}

func toAlbumChild(album catalog.MusicAlbum) child {
	return toArtistAlbumChild(album)
}

func toSongChild(track catalog.MusicTrack) child {
	audio := firstAudioFile(track.AudioFiles)
	return child{
		ID:          track.ID,
		Parent:      track.AlbumID,
		Title:       track.Title,
		IsDir:       false,
		Album:       track.AlbumTitle,
		Artist:      displayArtist(track.DisplayArtist, track.AlbumArtistNames, track.ArtistNames),
		ArtistID:    firstID(track.AlbumArtistIDs, track.ArtistIDs),
		Track:       track.TrackNumber,
		Year:        track.ReleaseYear,
		Genre:       firstGenre(track.Genres),
		CoverArt:    track.AlbumID,
		Duration:    track.DurationSeconds,
		BitRate:     subsonicBitRate(audio.Bitrate),
		ContentType: audio.MimeType,
		Path:        audioPath(audio),
		Size:        audio.SizeBytes,
		Suffix:      audioSuffix(audio),
	}
}

func toSong(track catalog.MusicTrack) song {
	child := toSongChild(track)
	return song{
		ID:          child.ID,
		Parent:      child.Parent,
		Title:       child.Title,
		Album:       child.Album,
		Artist:      child.Artist,
		ArtistID:    child.ArtistID,
		Track:       child.Track,
		Year:        child.Year,
		Genre:       child.Genre,
		CoverArt:    child.CoverArt,
		Duration:    child.Duration,
		BitRate:     child.BitRate,
		ContentType: child.ContentType,
		Path:        child.Path,
		Size:        child.Size,
		Suffix:      child.Suffix,
	}
}

func buildArtistIndex(artists []catalog.MusicArtist) artistsIndex {
	groups := map[string][]artist{}
	order := make([]string, 0)
	for _, item := range artists {
		key := indexKey(item.SortName, item.Name)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], toArtist(item))
	}

	index := make([]indexGroup, 0, len(order))
	for _, key := range order {
		index = append(index, indexGroup{Name: key, Artist: groups[key]})
	}
	return artistsIndex{Index: index}
}

func indexKey(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		runes := []rune(strings.ToUpper(value))
		for _, r := range runes {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return string(r)
			}
		}
	}
	return "#"
}

func displayArtist(display string, names ...[]string) string {
	if display = strings.TrimSpace(display); display != "" {
		return display
	}
	for _, group := range names {
		if name := strings.Join(nonEmptyStrings(group), ", "); name != "" {
			return name
		}
	}
	return ""
}

func firstID(groups ...[]string) string {
	for _, group := range groups {
		for _, id := range group {
			if id = strings.TrimSpace(id); id != "" {
				return id
			}
		}
	}
	return ""
}

func firstGenre(genres []string) string {
	for _, genre := range genres {
		if genre = strings.TrimSpace(genre); genre != "" {
			return genre
		}
	}
	return ""
}

func firstAudioFile(files []catalog.AudioFile) catalog.AudioFile {
	if len(files) == 0 {
		return catalog.AudioFile{}
	}
	return files[0]
}

func audioPath(file catalog.AudioFile) string {
	if path := strings.TrimSpace(file.RelativePath); path != "" {
		return path
	}
	return strings.TrimSpace(file.Path)
}

func audioSuffix(file catalog.AudioFile) string {
	if format := catalog.DisplayFormat(file); format != "" {
		return strings.ToLower(format)
	}
	if path := audioPath(file); path != "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if ext != "" {
			return ext
		}
	}
	return ""
}

func subsonicBitRate(bitrate int) int {
	if bitrate <= 0 {
		return 0
	}
	if bitrate > 4000 {
		return bitrate / 1000
	}
	return bitrate
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstImagePath(images []catalog.Image) string {
	for _, image := range images {
		if path := strings.TrimSpace(image.Path); path != "" {
			return path
		}
	}
	return ""
}

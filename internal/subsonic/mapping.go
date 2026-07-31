package subsonic

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Projection helpers: catalog types -> Subsonic elements. Kept in one file so
// the field-by-field mapping is reviewable in a single place, since a wrong
// key here is invisible until a client misbehaves.

func (s *Server) albumItem(album catalog.MusicAlbum) albumItem {
	tracks := s.catalog.MusicTracksForAlbum(album.ID)
	duration := 0
	for _, track := range tracks {
		duration += track.DurationSeconds
	}
	artistID := ""
	if len(album.ArtistIDs) > 0 {
		artistID = album.ArtistIDs[0]
	}
	return albumItem{
		ID:        album.ID,
		Name:      album.Title,
		Artist:    album.DisplayArtist,
		ArtistID:  artistID,
		CoverArt:  album.ID,
		SongCount: len(tracks),
		Duration:  duration,
		Created:   album.AddedAt,
		Year:      album.ReleaseYear,
		Genre:     firstOrEmpty(album.Genres),
	}
}

func (s *Server) child(track catalog.MusicTrack) child {
	artistID := ""
	if len(track.ArtistIDs) > 0 {
		artistID = track.ArtistIDs[0]
	}

	item := child{
		ID:         track.ID,
		Parent:     track.AlbumID,
		IsDir:      false,
		Title:      track.Title,
		Album:      track.AlbumTitle,
		Artist:     track.DisplayArtist,
		Track:      track.TrackNumber,
		Year:       track.ReleaseYear,
		Genre:      firstOrEmpty(track.Genres),
		CoverArt:   coverArtID(track),
		Duration:   track.DurationSeconds,
		DiscNumber: track.DiscNumber,
		Created:    track.AddedAt,
		AlbumID:    track.AlbumID,
		ArtistID:   artistID,
		Type:       "music",
	}

	// Size, container and bitrate come from the underlying file. Clients use
	// suffix and contentType to decide whether they can play a track without
	// transcoding, so an empty value here makes some of them skip it.
	if len(track.AudioFiles) > 0 {
		file := track.AudioFiles[0]
		item.Size = file.SizeBytes
		item.ContentType = file.MimeType
		item.Suffix = strings.TrimPrefix(strings.ToLower(filepath.Ext(file.FileName)), ".")
		if item.Suffix == "" {
			item.Suffix = strings.ToLower(file.Container)
		}
		item.Path = file.RelativePath
		if track.DurationSeconds > 0 && file.SizeBytes > 0 {
			// kbps, which is the unit the protocol expects.
			item.BitRate = int(file.SizeBytes * 8 / int64(track.DurationSeconds) / 1000)
		}
	}
	return item
}

// coverArtID prefers the track's own art and falls back to the album's, which
// is what makes compilations show per-track covers where they exist.
func coverArtID(track catalog.MusicTrack) string {
	for _, image := range track.Images {
		if strings.TrimSpace(image.ID) != "" {
			return image.ID
		}
	}
	return track.AlbumID
}

func (s *Server) playlistItem(pl catalog.MusicPlaylist, owner string) playlistItem {
	tracks := s.catalog.MusicTracksForPlaylist(pl.ID)
	duration := 0
	for _, track := range tracks {
		duration += track.DurationSeconds
	}
	return playlistItem{
		ID:        pl.ID,
		Name:      pl.Name,
		Comment:   pl.Description,
		Owner:     owner,
		Public:    pl.Public,
		SongCount: len(tracks),
		Duration:  duration,
		Created:   pl.CreatedAt,
		CoverArt:  pl.ID,
	}
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// pageSlice applies an offset and count to any slice, clamping rather than
// panicking on out-of-range values a client may send.
func pageSlice[T any](items []T, offset, count int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return nil
	}
	items = items[offset:]
	if count > 0 && count < len(items) {
		items = items[:count]
	}
	return items
}

func afterTime(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return false
	case a == nil:
		return false
	case b == nil:
		return true
	default:
		return a.After(*b)
	}
}

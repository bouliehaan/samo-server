package catalog

import "time"

// EnrichAlbumAddedAtFromFiles sets each album's AddedAt to the latest media file
// modified_at among its tracks so "recently added" reflects when files landed on
// disk, not when Samo first scanned them.
func EnrichAlbumAddedAtFromFiles(albums []MusicAlbum, tracks []MusicTrack) {
	latestByAlbum := make(map[string]time.Time)
	for _, track := range tracks {
		albumID := track.AlbumID
		if albumID == "" {
			continue
		}
		for _, file := range track.AudioFiles {
			if file.ModifiedAt == nil {
				continue
			}
			if existing, ok := latestByAlbum[albumID]; !ok || file.ModifiedAt.After(existing) {
				latestByAlbum[albumID] = *file.ModifiedAt
			}
		}
	}
	for i := range albums {
		if latest, ok := latestByAlbum[albums[i].ID]; ok {
			albums[i].AddedAt = timePtr(latest)
		}
	}
}

func timePtr(value time.Time) *time.Time {
	v := value
	return &v
}

func (s *Service) enrichAlbumAddedAtFromFiles() {
	EnrichAlbumAddedAtFromFiles(s.musicAlbums, s.musicTracks)
	for _, album := range s.musicAlbums {
		s.musicAlbumByID[album.ID] = album
	}
}

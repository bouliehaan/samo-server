package catalog

import "strings"

func (s *Service) MusicAlbumsForArtist(artistID string) []MusicAlbum {
	artistID = strings.TrimSpace(artistID)
	if artistID == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]MusicAlbum, 0)
	for _, album := range s.musicAlbums {
		if album.TrackCount <= 0 {
			continue
		}
		if musicAlbumMatchesArtist(album, artistID) {
			items = append(items, album)
		}
	}
	return items
}

func (s *Service) MusicTracksForArtist(artistID string) []MusicTrack {
	artistID = strings.TrimSpace(artistID)
	if artistID == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]MusicTrack, 0)
	for _, track := range s.musicTracks {
		if musicTrackMatchesArtistLocked(s, track, artistID) {
			items = append(items, track)
		}
	}
	return items
}

func musicTrackMatchesArtistLocked(s *Service, track MusicTrack, artistID string) bool {
	for _, id := range track.ArtistIDs {
		if id == artistID {
			return true
		}
	}
	for _, id := range track.AlbumArtistIDs {
		if id == artistID {
			return true
		}
	}
	if track.AlbumID == "" {
		return false
	}
	album, ok := s.musicAlbumByID[track.AlbumID]
	if !ok {
		return false
	}
	return musicAlbumMatchesArtist(album, artistID)
}

func (s *Service) MusicTracksForAlbum(albumID string) []MusicTrack {
	albumID = strings.TrimSpace(albumID)
	if albumID == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]MusicTrack, 0)
	for _, track := range s.musicTracks {
		if track.AlbumID == albumID {
			items = append(items, track)
		}
	}
	return items
}

func (s *Service) MusicTracksForPlaylist(playlistID string) []MusicTrack {
	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	playlist, ok := s.playlistByID[playlistID]
	if !ok {
		return nil
	}

	items := make([]MusicTrack, 0, len(playlist.TrackIDs))
	for _, trackID := range playlist.TrackIDs {
		if track, ok := s.musicTrackByID[trackID]; ok {
			items = append(items, track)
		}
	}
	return items
}

func (s *Service) ResolveMusicCoverArtID(id string) (string, []Image) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if track, ok := s.musicTrackByID[id]; ok {
		if images := nonEmptyImages(track.Images); len(images) > 0 {
			return id, images
		}
		return track.AlbumID, s.musicAlbumCoverImagesLocked(track.AlbumID)
	}
	if _, ok := s.musicAlbumByID[id]; ok {
		return id, s.musicAlbumCoverImagesLocked(id)
	}
	if artist, ok := s.musicArtistByID[id]; ok {
		if images := nonEmptyImages(artist.Images); len(images) > 0 {
			return id, images
		}
		return id, nil
	}
	return "", nil
}

func musicAlbumMatchesArtist(album MusicAlbum, artistID string) bool {
	for _, id := range album.AlbumArtistIDs {
		if id == artistID {
			return true
		}
	}
	return false
}

func nonEmptyImages(images []Image) []Image {
	if len(images) == 0 {
		return nil
	}
	filtered := make([]Image, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.Path) != "" ||
			strings.TrimSpace(image.URL) != "" ||
			strings.TrimSpace(image.ID) != "" {
			filtered = append(filtered, image)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

package catalog

import (
	"os"
	"path/filepath"
	"strings"
)

func registerCatalogImages(index map[string]Image, images []Image) {
	for _, image := range images {
		id := strings.TrimSpace(image.ID)
		if id == "" {
			continue
		}
		if _, exists := index[id]; exists {
			continue
		}
		index[id] = image
	}
}

func (s *Service) ImageByID(id string) (Image, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return Image{}, ErrNotFound
	}

	image, ok := s.imageByID[id]
	if !ok {
		return Image{}, ErrNotFound
	}
	return image, nil
}

func (s *Service) registerExtractedCoverCatalog() {
	seen := map[string]struct{}{}
	for _, image := range s.extractedCoversBySource {
		id := strings.TrimSpace(image.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		registerCatalogImages(s.imageByID, []Image{image})
	}
}

func (s *Service) backfillMusicImagesFromExtractedCovers() {
	if len(s.extractedCoversBySource) == 0 {
		return
	}
	for index, track := range s.musicTracks {
		if len(nonEmptyImages(track.Images)) > 0 {
			continue
		}
		image, ok := s.lookupTrackExtractedCover(track)
		if !ok {
			continue
		}
		s.musicTracks[index].Images = []Image{image}
		s.musicTrackByID[track.ID] = s.musicTracks[index]
		registerCatalogImages(s.imageByID, []Image{image})
	}
}

func (s *Service) lookupTrackExtractedCover(track MusicTrack) (Image, bool) {
	for _, candidate := range trackAudioPathCandidates(track) {
		if image, ok := lookupExtractedCover(s.extractedCoversBySource, candidate); ok {
			return image, true
		}
	}
	return Image{}, false
}

func trackAudioPathCandidates(track MusicTrack) []string {
	candidates := make([]string, 0, len(track.AudioFiles)*3)
	seen := map[string]struct{}{}
	var add func(string)
	add = func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, dup := seen[value]; dup {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
		if clean := filepath.Clean(value); clean != value {
			add(clean)
		}
		if absolute, err := filepath.Abs(value); err == nil {
			add(absolute)
		}
	}
	for _, file := range track.AudioFiles {
		add(file.Path)
		add(file.RelativePath)
	}
	return candidates
}

func trackPrimaryAudioPath(track MusicTrack) string {
	candidates := trackAudioPathCandidates(track)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func lookupExtractedCover(covers map[string]Image, audioPath string) (Image, bool) {
	candidates := []string{
		strings.TrimSpace(audioPath),
		filepath.Clean(strings.TrimSpace(audioPath)),
	}
	if absolute, err := filepath.Abs(strings.TrimSpace(audioPath)); err == nil {
		candidates = append(candidates, absolute, filepath.Clean(absolute))
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		if image, ok := covers[candidate]; ok {
			return image, true
		}
	}
	return Image{}, false
}

func (s *Service) enrichAlbumImagesFromTracks() {
	for index, album := range s.musicAlbums {
		if len(nonEmptyImages(album.Images)) > 0 {
			continue
		}
		for _, track := range s.musicTracks {
			if track.AlbumID != album.ID {
				continue
			}
			images := nonEmptyImages(track.Images)
			if len(images) == 0 {
				continue
			}
			s.musicAlbums[index].Images = images
			s.musicAlbumByID[album.ID] = s.musicAlbums[index]
			registerCatalogImages(s.imageByID, images)
			break
		}
	}
}

func (s *Service) enrichAlbumImagesFromExtractedCovers() {
	if len(s.extractedCoversBySource) == 0 {
		return
	}
	for index, album := range s.musicAlbums {
		if len(nonEmptyImages(album.Images)) > 0 {
			continue
		}
		for _, track := range s.musicTracks {
			if track.AlbumID != album.ID {
				continue
			}
			image, ok := s.lookupTrackExtractedCover(track)
			if !ok {
				continue
			}
			images := []Image{image}
			s.musicAlbums[index].Images = images
			s.musicAlbumByID[album.ID] = s.musicAlbums[index]
			registerCatalogImages(s.imageByID, images)
			break
		}
	}
}

func (s *Service) repairBrokenMusicImageReferences() {
	for index, album := range s.musicAlbums {
		if repaired, ok := s.repairImageReferences(album.Images); ok {
			s.musicAlbums[index].Images = repaired
			s.musicAlbumByID[album.ID] = s.musicAlbums[index]
		}
	}
	for index, track := range s.musicTracks {
		if repaired, ok := s.repairImageReferences(track.Images); ok {
			s.musicTracks[index].Images = repaired
			s.musicTrackByID[track.ID] = s.musicTracks[index]
		}
	}
}

func (s *Service) repairImageReferences(images []Image) ([]Image, bool) {
	filtered := nonEmptyImages(images)
	if len(filtered) == 0 {
		return nil, false
	}
	repaired := make([]Image, 0, len(filtered))
	changed := false
	for _, image := range filtered {
		if image.Path != "" || image.URL != "" {
			repaired = append(repaired, image)
			continue
		}
		id := strings.TrimSpace(image.ID)
		if id == "" {
			changed = true
			continue
		}
		if resolved, ok := s.imageByID[id]; ok && (resolved.Path != "" || resolved.URL != "" || resolved.ID != "") {
			repaired = append(repaired, resolved)
			if resolved.Path != image.Path || resolved.URL != image.URL {
				changed = true
			}
			continue
		}
		if image, ok := lookupExtractedCoverByID(s.extractedCoversBySource, id); ok {
			repaired = append(repaired, image)
			changed = true
			continue
		}
		changed = true
	}
	if len(repaired) == 0 {
		return nil, changed
	}
	if !changed {
		return filtered, false
	}
	return repaired, true
}

func lookupExtractedCoverByID(covers map[string]Image, id string) (Image, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Image{}, false
	}
	for _, image := range covers {
		if strings.TrimSpace(image.ID) == id {
			return image, true
		}
	}
	return Image{}, false
}

// MusicAlbumCoverImages returns the best cover metadata for an album, falling
// back to track art and extracted-cover lookup when album.images is empty.
func (s *Service) MusicAlbumCoverImages(albumID string) []Image {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.musicAlbumCoverImagesLocked(albumID)
}

func (s *Service) musicAlbumCoverImagesLocked(albumID string) []Image {
	albumID = strings.TrimSpace(albumID)
	if albumID == "" {
		return nil
	}
	if album, ok := s.musicAlbumByID[albumID]; ok {
		if images := nonEmptyImages(album.Images); len(images) > 0 {
			return images
		}
	}
	for _, track := range s.musicTracks {
		if track.AlbumID != albumID {
			continue
		}
		if images := nonEmptyImages(track.Images); len(images) > 0 {
			return images
		}
	}
	for _, track := range s.musicTracks {
		if track.AlbumID != albumID {
			continue
		}
		if image, ok := s.lookupTrackExtractedCover(track); ok {
			return []Image{image}
		}
	}
	return nil
}

// NonEmptyImages returns image metadata rows that still resolve to something
// servable (local path, remote URL, or cached cover id).
func NonEmptyImages(images []Image) []Image {
	return nonEmptyImages(images)
}

// resolvedImagesLocked returns images that can still be served (repairing cover
// ids and skipping local paths that no longer exist).
func (s *Service) resolvedImagesLocked(images []Image) []Image {
	repaired, _ := s.repairImageReferences(images)
	if len(repaired) == 0 {
		return nil
	}
	out := make([]Image, 0, len(repaired))
	for _, image := range repaired {
		if strings.TrimSpace(image.URL) != "" {
			out = append(out, image)
			continue
		}
		if path := strings.TrimSpace(image.Path); path != "" {
			if _, err := os.Stat(path); err == nil {
				out = append(out, image)
			}
			continue
		}
		if strings.TrimSpace(image.ID) != "" {
			out = append(out, image)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Service) musicPlaylistAutoCoverImagesLocked(playlist MusicPlaylist) []Image {
	var results []Image
	seen := make(map[string]bool)

	addUnique := func(images []Image) bool {
		for _, img := range images {
			key := img.ID
			if key == "" {
				key = img.Path
			}
			if key == "" {
				key = img.URL
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, img)
			if len(results) == 4 {
				return true
			}
		}
		return false
	}

	for _, trackID := range playlist.TrackIDs {
		track, ok := s.musicTrackByID[trackID]
		if !ok {
			continue
		}
		if images := s.resolvedImagesLocked(track.Images); len(images) > 0 {
			if addUnique(images) {
				break
			}
			continue
		}
		if images := s.musicAlbumCoverImagesLocked(track.AlbumID); len(images) > 0 {
			if addUnique(images) {
				break
			}
			continue
		}
		if image, ok := s.lookupTrackExtractedCover(track); ok {
			if addUnique([]Image{image}) {
				break
			}
		}
	}

	if len(results) > 1 && len(results) < 4 {
		orig := len(results)
		for i := orig; i < 4; i++ {
			results = append(results, results[i%orig])
		}
	}

	if len(results) == 4 {
		return results
	}
	if len(results) > 0 {
		return results[:1]
	}
	return nil
}

// MusicPlaylistCoverImages returns user-uploaded playlist art when present,
// otherwise the first servable cover from a playlist track (track art, album
// art, or extracted embedded cover).
func (s *Service) MusicPlaylistCoverImages(playlistID string) []Image {
	s.mu.RLock()
	defer s.mu.RUnlock()

	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return nil
	}
	playlist, ok := s.playlistByID[playlistID]
	if !ok {
		return nil
	}
	if images := s.resolvedImagesLocked(playlist.Images); len(images) > 0 {
		return images
	}
	return s.musicPlaylistAutoCoverImagesLocked(playlist)
}

func (s *Service) enrichPlaylistImagesFromTracks() {
	for index, playlist := range s.musicPlaylists {
		if len(s.resolvedImagesLocked(playlist.Images)) > 0 {
			continue
		}
		auto := s.musicPlaylistAutoCoverImagesLocked(playlist)
		if len(auto) == 0 {
			continue
		}
		s.musicPlaylists[index].Images = auto
		s.playlistByID[playlist.ID] = s.musicPlaylists[index]
		registerCatalogImages(s.imageByID, auto)
	}
}

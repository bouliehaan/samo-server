package catalog

// EnrichAlbumAudioQuality aggregates track technical metadata onto each album
// so list/search/home responses can render quality badges without per-track
// fetches. Mutates the albums slice in place.
func EnrichAlbumAudioQuality(albums []MusicAlbum, tracks []MusicTrack) {
	tracksByAlbum := map[string][]MusicTrack{}
	for _, track := range tracks {
		albumID := track.AlbumID
		if albumID == "" {
			continue
		}
		tracksByAlbum[albumID] = append(tracksByAlbum[albumID], track)
	}

	for index, album := range albums {
		depth, rate, quality, hiRes := summarizeAlbumAudioQuality(tracksByAlbum[album.ID])
		album.MaxBitDepth = depth
		album.MaxSampleRate = rate
		album.AudioQuality = quality
		album.HiRes = hiRes
		albums[index] = album
	}
}

// enrichAlbumAudioQuality aggregates track technical metadata onto each album so
// clients can render hi-res badges from album list/search/home responses.
func (s *Service) enrichAlbumAudioQuality() {
	EnrichAlbumAudioQuality(s.musicAlbums, s.musicTracks)
	for _, album := range s.musicAlbums {
		s.musicAlbumByID[album.ID] = album
	}
}

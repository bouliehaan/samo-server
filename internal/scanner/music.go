package scanner

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/media"
)

func (s *Scanner) scanMusicFile(ctx context.Context, library Library, root string, path string) error {
	if !shouldScanAudioFile(path) {
		return nil
	}
	if s.skipUnchangedFile(path) {
		return nil
	}

	log.Printf("scanner: indexing music file %q", path)
	if s.onFileActive != nil {
		s.onFileActive(path)
	}
	// Bump files_seen before ffprobe/DB work so the dashboard does not sit at
	// 0/13637 while the first (slow) file is processed.
	if s.activeScan != nil {
		s.activeScan.seeFile(path)
	}

	var probe probeInfo
	var err error
	if s.scanMode == ScanModeRepair {
		probe, err = s.loadCachedMusicProbe(ctx, library.ID, path)
		if err != nil {
			probe, err = s.probeMusic(ctx, path)
		}
	} else {
		probe, err = s.probeMusic(ctx, path)
	}
	if err != nil {
		// One unreadable file (corrupt, unsupported codec, ffprobe
		// hiccup) used to abort the entire scan. Log + skip so the
		// other 9,999 files still get indexed.
		log.Printf("scanner: skipping %q (probe failed: %v)", path, err)
		return nil
	}

	relPath, _ := filepath.Rel(root, path)
	tags := probe.Tags
	albumDir := filepath.Dir(path)
	relAlbumDir, _ := filepath.Rel(root, albumDir)
	albumSidecar := readMusicAlbumSidecar(albumDir)
	title := firstTag(tags, "title")
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	artistNames := splitTag(tags, "artist", "artists")
	if len(artistNames) == 0 {
		artistNames = []string{"Unknown Artist"}
	}
	artistSortNames := splitTag(tags, "artistsort", "artist_sort", "sortname")
	albumArtistSortNames := splitTag(tags, "albumartistsort", "album_artist_sort", "albumsort")
	artists := musicArtistsFromNames(artistNames, splitTag(tags, "musicbrainz_artistid", "musicbrainz_artist_id"), artistSortNames)

	albumTitle := firstNonEmpty(firstTag(tags, "album"), albumSidecar.Title)
	if albumTitle == "" {
		albumTitle = filepath.Base(albumDir)
	}
	releaseDate := firstTag(tags, "date", "year", "originaldate", "originalyear")
	albumArtistNames := resolveMusicAlbumArtistNames(tags, albumSidecar)
	albumID := resolveMusicAlbumID(tags, albumTitle, relAlbumDir, albumArtistNames)
	trackPID := computeTrackPID(library.ID, relPath, tags, albumID)
	contentHash := contentHashFromProbe(library.ID, relPath, tags, albumID, probe)
	if len(albumArtistNames) == 0 {
		albumArtistNames = s.loadAlbumArtistNamesForAlbum(ctx, albumID)
	}
	albumArtists := musicArtistsFromNames(albumArtistNames, splitTag(tags, "musicbrainz_albumartistid", "musicbrainz_albumartist_id"), albumArtistSortNames)
	if artistImage := findArtistImage(filepath.Dir(albumDir)); artistImage != nil {
		for index := range albumArtists {
			if len(albumArtists[index].Images) == 0 {
				albumArtists[index].Images = []catalog.Image{*artistImage}
			}
		}
	}
	for _, artist := range append(artists, albumArtists...) {
		if err := s.upsertMusicArtist(ctx, artist); err != nil {
			return err
		}
	}

	releaseYear := yearFromDate(releaseDate)
	genres := splitGenreTag(tags, "genre")
	for _, genre := range genres {
		if err := s.upsertGenre(ctx, string(media.KindMusic), genre); err != nil {
			return err
		}
	}

	embeddedKnown := probe.HasEmbeddedCover
	albumCover := s.resolveCover(ctx, filepath.Dir(path), []string{path}, []string{probe.AudioFile.Checksum}, &embeddedKnown)
	album := catalog.MusicAlbum{
		ID:                  albumID,
		Title:               albumTitle,
		SortTitle:           firstTag(tags, "albumsort", "album_sort", "sortalbum"),
		Version:             firstTag(tags, "albumversion", "album_version"),
		DisplayArtist:       resolveMusicAlbumDisplayArtist(tags, albumSidecar, albumArtistNames),
		AlbumArtistIDs:      artistIDs(albumArtists),
		AlbumArtistNames:    albumArtistNames,
		ReleaseDate:         releaseDate,
		OriginalReleaseDate: firstTag(tags, "originaldate", "originalyear", "original_release_date"),
		ReleaseYear:         releaseYear,
		ReleaseType:         firstTag(tags, "releasetype", "musicbrainz_albumtype"),
		ReleaseStatus:       firstTag(tags, "releasestatus"),
		Compilation:         boolTag(tags, "compilation", "itunescompilation", "tcmp"),
		RecordLabel:         firstNonEmpty(firstTag(tags, "label", "organization", "publisher"), albumSidecar.RecordLabel),
		CatalogNumber:       firstTag(tags, "catalognumber", "catalog_number"),
		Barcode:             firstNonEmpty(barcodeFromTags(tags), albumSidecar.Barcode),
		Genres:              genres,
		Styles:              splitGenreTag(tags, "style", "styles"),
		Moods:               splitGenreTag(tags, "mood", "moods"),
		Tags:                splitGenreTag(tags, "tag", "tags"),
		ExternalIDs: catalog.ExternalIDs{
			MusicBrainzReleaseGroupID: firstTag(tags, "musicbrainz_releasegroupid", "musicbrainz_albumgroupid"),
			MusicBrainzReleaseID:      firstTag(tags, "musicbrainz_albumid", "musicbrainz_releaseid"),
			DiscogsID:                 firstTag(tags, "discogs_release_id", "discogs_id"),
			SpotifyID:                 firstTag(tags, "spotify_album_id"),
			AppleMusicID:              firstTag(tags, "apple_music_album_id", "applemusic_album_id"),
		},
	}
	if len(album.Genres) == 0 && len(albumSidecar.Genres) > 0 {
		album.Genres = albumSidecar.Genres
	}
	albumSidecar.mergeIntoAlbum(&album)
	if albumCover != nil {
		album.Images = []catalog.Image{*albumCover}
	}
	if err := s.upsertMusicAlbum(ctx, album); err != nil {
		return err
	}
	if s.activeScan != nil {
		s.activeScan.seeAlbum(album.ID)
	}
	if err := s.setAlbumArtists(ctx, album.ID, albumArtists, albumArtistsExplicitFromTags(tags, albumSidecar)); err != nil {
		return err
	}

	discNumber, totalDiscs := parseNumberPair(firstTag(tags, "disc", "discnumber"))
	trackNumber, totalTracks := parseNumberPair(firstTag(tags, "track", "tracknumber"))
	// Navidrome-style persistent id: stable across path changes; phase 2 reconciles moves.
	trackID := stableID("track", library.ID, trackPID)
	track := catalog.MusicTrack{
		ID:               trackID,
		Title:            title,
		SortTitle:        firstTag(tags, "titlesort", "title_sort", "sorttitle"),
		Subtitle:         firstTag(tags, "subtitle"),
		DisplayArtist:    firstNonEmpty(firstTag(tags, "artists", "artist_display"), strings.Join(artistNames, ", ")),
		ArtistIDs:        artistIDs(artists),
		ArtistNames:      artistNames,
		AlbumID:          album.ID,
		AlbumTitle:       album.Title,
		AlbumArtistIDs:   album.AlbumArtistIDs,
		AlbumArtistNames: album.AlbumArtistNames,
		DiscNumber:       discNumber,
		TrackNumber:      trackNumber,
		TotalDiscs:       totalDiscs,
		TotalTracks:      totalTracks,
		ReleaseDate:      releaseDate,
		ReleaseYear:      releaseYear,
		Genres:           genres,
		Moods:            splitGenreTag(tags, "mood", "moods"),
		Tags:             album.Tags,
		DurationSeconds:  probe.AudioFile.DurationSeconds,
		Explicit:         explicitTag(tags),
		BPM:              int(parseInt64(firstTag(tags, "bpm"))),
		Key:              firstTag(tags, "initialkey", "key"),
		Comment:          firstTag(tags, "comment", "description"),
		ExternalIDs: catalog.ExternalIDs{
			MusicBrainzRecordingID: firstTag(tags, "musicbrainz_trackid", "musicbrainz_recordingid"),
			MusicBrainzTrackID:     firstTag(tags, "musicbrainz_releasetrackid"),
			MusicBrainzWorkID:      firstTag(tags, "musicbrainz_workid"),
			ISRC:                   firstTag(tags, "isrc"),
			SpotifyID:              firstTag(tags, "spotify_track_id"),
			AppleMusicID:           firstTag(tags, "apple_music_track_id", "applemusic_track_id"),
		},
	}
	if albumCover != nil {
		track.Images = []catalog.Image{*albumCover}
	}
	if lyrics := firstTag(tags, "lyrics", "unsyncedlyrics"); lyrics != "" {
		track.Lyrics = []catalog.Lyric{{Text: lyrics, Synced: false}}
	}
	if err := s.upsertMusicTrack(ctx, track); err != nil {
		return err
	}
	if err := s.setTrackArtists(ctx, track.ID, artists); err != nil {
		return err
	}

	file := probe.AudioFile
	// media_files.path is globally unique — id must be stable per path, not per
	// trackPID (which changes when tags/album grouping change on rescan).
	file.ID = stableID("file", file.Path)
	file.RelativePath = relPath
	if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{TrackID: track.ID}, file, trackPID, contentHash); err != nil {
		return err
	}

	return nil
}

func musicArtistsFromNames(names []string, musicBrainzIDs []string, sortNames []string) []catalog.MusicArtist {
	artists := make([]catalog.MusicArtist, 0, len(names))
	for index, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		artist := catalog.MusicArtist{
			ID:   stableID("artist", name),
			Name: name,
		}
		if index < len(sortNames) {
			artist.SortName = strings.TrimSpace(sortNames[index])
		}
		if index < len(musicBrainzIDs) {
			artist.ExternalIDs.MusicBrainzArtistID = musicBrainzIDs[index]
		}
		artists = append(artists, artist)
	}
	return artists
}

func artistIDs(artists []catalog.MusicArtist) []string {
	ids := make([]string, 0, len(artists))
	for _, artist := range artists {
		ids = append(ids, artist.ID)
	}
	return ids
}

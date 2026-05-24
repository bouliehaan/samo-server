package catalog

import "strings"

// ProjectMetadataOverrides overlays user override patches onto a hydrated
// catalog seed. Each domain has its own target_kind:
//
//	music-artist / music-album / music-track / music-playlist
//	audiobook
//	podcast              (the show)
//	podcast-episode      (one episode)
//	podcast-feed         (RSS row in podcast_feeds; overlays its podcast show)
//
// The old shelf-* dispatch values are now dead — migration 016 rewrites
// any existing rows.
func ProjectMetadataOverrides(seed *Seed, overrides map[MetadataOverrideKey]MetadataOverridePatch, feedPodcastIDs map[string]string) {
	if seed == nil || len(overrides) == 0 {
		return
	}

	podcastFeedByPodcastID := map[string]MetadataOverridePatch{}
	for key, patch := range overrides {
		if key.TargetKind != "podcast-feed" {
			continue
		}
		if podcastID := feedPodcastIDs[key.TargetID]; podcastID != "" {
			podcastFeedByPodcastID[podcastID] = patch
		}
	}

	for index, artist := range seed.MusicArtists {
		key := MetadataOverrideKey{TargetKind: "music-artist", TargetID: artist.ID}
		if patch, ok := overrides[key]; ok {
			seed.MusicArtists[index] = overlayMusicArtist(artist, patch)
		}
	}
	for index, album := range seed.MusicAlbums {
		key := MetadataOverrideKey{TargetKind: "music-album", TargetID: album.ID}
		if patch, ok := overrides[key]; ok {
			seed.MusicAlbums[index] = overlayMusicAlbum(album, patch)
		}
	}
	for index, track := range seed.MusicTracks {
		key := MetadataOverrideKey{TargetKind: "music-track", TargetID: track.ID}
		if patch, ok := overrides[key]; ok {
			seed.MusicTracks[index] = overlayMusicTrack(track, patch)
		}
	}
	for index, item := range seed.Audiobooks {
		key := MetadataOverrideKey{TargetKind: "audiobook", TargetID: item.ID}
		if patch, ok := overrides[key]; ok {
			item = overlayAudiobook(item, patch)
		}
		seed.Audiobooks[index] = item
	}
	for index, item := range seed.Podcasts {
		key := MetadataOverrideKey{TargetKind: "podcast", TargetID: item.ID}
		if patch, ok := overrides[key]; ok {
			item = overlayPodcast(item, patch)
		}
		if patch, ok := podcastFeedByPodcastID[item.ID]; ok {
			item = overlayPodcastFeedOnPodcast(item, patch)
		}
		seed.Podcasts[index] = item
	}
	for index, episode := range seed.PodcastEpisodes {
		key := MetadataOverrideKey{TargetKind: "podcast-episode", TargetID: episode.ID}
		if patch, ok := overrides[key]; ok {
			seed.PodcastEpisodes[index] = overlayPodcastEpisode(episode, patch)
		}
	}
}

func overlayMusicArtist(artist MusicArtist, patch MetadataOverridePatch) MusicArtist {
	if value, ok := decodePatchString(patch, "name"); ok {
		artist.Name = value
	}
	if value, ok := decodePatchString(patch, "sortName"); ok {
		artist.SortName = value
	}
	if value, ok := decodePatchString(patch, "description"); ok {
		artist.Disambiguation = value
	}
	if value, ok := decodePatchStringSlice(patch, "genres"); ok {
		artist.Genres = value
	}
	if value, ok := decodePatchStringSlice(patch, "tags"); ok {
		artist.Moods = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		artist.ExternalIDs = value
	}
	return artist
}

func overlayMusicAlbum(album MusicAlbum, patch MetadataOverridePatch) MusicAlbum {
	if value, ok := decodePatchString(patch, "title"); ok {
		album.Title = value
	}
	if value, ok := decodePatchString(patch, "sortTitle"); ok {
		album.SortTitle = value
	}
	if value, ok := decodePatchString(patch, "version"); ok {
		album.Version = value
	}
	if value, ok := decodePatchString(patch, "displayArtist"); ok {
		album.DisplayArtist = value
	}
	if value, ok := decodePatchString(patch, "releaseDate"); ok {
		album.ReleaseDate = value
	}
	if value, ok := decodePatchString(patch, "originalReleaseDate"); ok {
		album.OriginalReleaseDate = value
	}
	if value, ok := decodePatchInt(patch, "releaseYear"); ok {
		album.ReleaseYear = value
	}
	if value, ok := decodePatchString(patch, "releaseType"); ok {
		album.ReleaseType = value
	}
	if value, ok := decodePatchString(patch, "recordLabel"); ok {
		album.RecordLabel = value
	}
	if value, ok := decodePatchString(patch, "catalogNumber"); ok {
		album.CatalogNumber = value
	}
	if value, ok := decodePatchString(patch, "barcode"); ok {
		album.Barcode = value
	}
	if value, ok := decodePatchStringSlice(patch, "genres"); ok {
		album.Genres = value
	}
	if value, ok := decodePatchStringSlice(patch, "styles"); ok {
		album.Styles = value
	}
	if value, ok := decodePatchStringSlice(patch, "moods"); ok {
		album.Moods = value
	}
	if value, ok := decodePatchStringSlice(patch, "tags"); ok {
		album.Tags = value
	}
	if value, ok := decodePatchImages(patch, "cover"); ok {
		album.Images = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		album.ExternalIDs = value
	}
	if value, ok := decodePatchContributors(patch, "artists"); ok {
		album.ArtistNames = contributorRefNames(value)
		if album.DisplayArtist == "" {
			album.DisplayArtist = joinContributorRefNames(value)
		}
	}
	return album
}

func overlayMusicTrack(track MusicTrack, patch MetadataOverridePatch) MusicTrack {
	if value, ok := decodePatchString(patch, "title"); ok {
		track.Title = value
	}
	if value, ok := decodePatchString(patch, "sortTitle"); ok {
		track.SortTitle = value
	}
	if value, ok := decodePatchString(patch, "subtitle"); ok {
		track.Subtitle = value
	}
	if value, ok := decodePatchString(patch, "displayArtist"); ok {
		track.DisplayArtist = value
	}
	if value, ok := decodePatchString(patch, "releaseDate"); ok {
		track.ReleaseDate = value
	}
	if value, ok := decodePatchInt(patch, "releaseYear"); ok {
		track.ReleaseYear = value
	}
	if value, ok := decodePatchStringSlice(patch, "genres"); ok {
		track.Genres = value
	}
	if value, ok := decodePatchStringSlice(patch, "moods"); ok {
		track.Moods = value
	}
	if value, ok := decodePatchStringSlice(patch, "tags"); ok {
		track.Tags = value
	}
	if value, ok := decodePatchBool(patch, "explicit"); ok {
		track.Explicit = value
	}
	if value, ok := decodePatchImages(patch, "cover"); ok {
		track.Images = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		track.ExternalIDs = value
	}
	if value, ok := decodePatchContributors(patch, "artists"); ok {
		track.ArtistNames = contributorRefNames(value)
		if track.DisplayArtist == "" {
			track.DisplayArtist = joinContributorRefNames(value)
		}
	}
	return track
}

func contributorRefNames(refs []ContributorRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		if name := strings.TrimSpace(ref.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func joinContributorRefNames(refs []ContributorRef) string {
	return strings.Join(contributorRefNames(refs), ", ")
}

func overlayAudiobook(item AudiobookItem, patch MetadataOverridePatch) AudiobookItem {
	book := item.Book
	if book == nil {
		book = &BookMetadata{}
	}
	if value, ok := decodePatchString(patch, "title"); ok {
		book.Title = value
	}
	if value, ok := decodePatchString(patch, "subtitle"); ok {
		book.Subtitle = value
	}
	if value, ok := decodePatchString(patch, "sortTitle"); ok {
		book.SortTitle = value
	}
	if value, ok := decodePatchString(patch, "description"); ok {
		book.Description = value
	}
	if value, ok := decodePatchString(patch, "publisher"); ok {
		book.Publisher = value
	}
	if value, ok := decodePatchString(patch, "publishedDate"); ok {
		book.PublishedDate = value
	}
	if value, ok := decodePatchString(patch, "publishedYear"); ok {
		book.PublishedYear = value
	}
	if value, ok := decodePatchString(patch, "language"); ok {
		book.Language = value
	}
	if value, ok := decodePatchStringSlice(patch, "genres"); ok {
		book.Genres = value
		item.Genres = value
	}
	if value, ok := decodePatchStringSlice(patch, "tags"); ok {
		book.Tags = value
		item.Tags = value
	}
	if value, ok := decodePatchBool(patch, "explicit"); ok {
		book.Explicit = value
	}
	if value, ok := decodePatchBool(patch, "abridged"); ok {
		book.Abridged = value
	}
	if value, ok := decodePatchContributors(patch, "authors"); ok {
		book.Authors = value
	}
	if value, ok := decodePatchContributors(patch, "narrators"); ok {
		book.Narrators = value
	}
	if value, ok := decodePatchSeries(patch, "series"); ok {
		book.Series = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		book.ExternalIDs = value
	}
	if cover, ok := decodePatchImage(patch, "cover"); ok {
		item.Cover = cover
	}
	item.Book = book
	return item
}

func overlayPodcast(item PodcastItem, patch MetadataOverridePatch) PodcastItem {
	podcast := item.Podcast
	if podcast == nil {
		podcast = &PodcastMetadata{}
	}
	if value, ok := decodePatchString(patch, "title"); ok {
		podcast.Title = value
	}
	if value, ok := decodePatchString(patch, "description"); ok {
		podcast.Description = value
	}
	if value, ok := decodePatchString(patch, "author"); ok {
		podcast.Author = value
	}
	if value, ok := decodePatchString(patch, "siteUrl"); ok {
		podcast.SiteURL = value
	}
	if value, ok := decodePatchString(patch, "language"); ok {
		podcast.Language = value
	}
	if value, ok := decodePatchStringSlice(patch, "genres"); ok {
		item.Genres = value
	}
	if value, ok := decodePatchStringSlice(patch, "categories"); ok {
		podcast.Categories = value
		item.Genres = value
	}
	if value, ok := decodePatchBool(patch, "explicit"); ok {
		podcast.Explicit = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		podcast.ExternalIDs = value
	}
	if cover, ok := decodePatchImage(patch, "cover"); ok {
		item.Cover = cover
	}
	item.Podcast = podcast
	return item
}

// overlayPodcastFeedOnPodcast applies a podcast-feed override to its
// associated podcast show. Both target_kind = 'podcast' and target_kind =
// 'podcast-feed' can adjust the same fields; we apply the podcast-feed
// patch second so it wins on conflict.
func overlayPodcastFeedOnPodcast(item PodcastItem, patch MetadataOverridePatch) PodcastItem {
	podcast := item.Podcast
	if podcast == nil {
		podcast = &PodcastMetadata{}
	}
	if value, ok := decodePatchString(patch, "title"); ok {
		podcast.Title = value
	}
	if value, ok := decodePatchString(patch, "description"); ok {
		podcast.Description = value
	}
	if value, ok := decodePatchString(patch, "author"); ok {
		podcast.Author = value
	}
	if value, ok := decodePatchString(patch, "siteUrl"); ok {
		podcast.SiteURL = value
	}
	if value, ok := decodePatchString(patch, "language"); ok {
		podcast.Language = value
	}
	if value, ok := decodePatchStringSlice(patch, "categories"); ok {
		podcast.Categories = value
		item.Genres = value
	}
	if value, ok := decodePatchBool(patch, "explicit"); ok {
		podcast.Explicit = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		podcast.ExternalIDs = value
	}
	if cover, ok := decodePatchImage(patch, "cover"); ok {
		item.Cover = cover
	} else if value, ok := decodePatchString(patch, "imageUrl"); ok && strings.TrimSpace(value) != "" {
		item.Cover = &Image{URL: value}
	}
	item.Podcast = podcast
	return item
}

func overlayPodcastEpisode(episode PodcastEpisode, patch MetadataOverridePatch) PodcastEpisode {
	if value, ok := decodePatchString(patch, "title"); ok {
		episode.Title = value
	}
	if value, ok := decodePatchString(patch, "subtitle"); ok {
		episode.Subtitle = value
	}
	if value, ok := decodePatchString(patch, "description"); ok {
		episode.Description = value
	}
	if value, ok := decodePatchTime(patch, "publishedAt"); ok {
		episode.PublishedAt = value
	}
	if value, ok := decodePatchBool(patch, "explicit"); ok {
		episode.Explicit = value
	}
	if value, ok := decodePatchExternalIDs(patch, "externalIds"); ok {
		episode.ExternalIDs = value
	}
	return episode
}

// PodcastFeedFields is the metadata subset exposed by podcast feed source APIs.
type PodcastFeedFields struct {
	Title       string
	Description string
	Author      string
	SiteURL     string
	ImageURL    string
	Language    string
	Explicit    bool
	Categories  []string
}

func ProjectPodcastFeedFields(fields PodcastFeedFields, patch MetadataOverridePatch) PodcastFeedFields {
	if len(patch) == 0 {
		return fields
	}
	out := fields
	if value, ok := decodePatchString(patch, "title"); ok {
		out.Title = value
	}
	if value, ok := decodePatchString(patch, "description"); ok {
		out.Description = value
	}
	if value, ok := decodePatchString(patch, "author"); ok {
		out.Author = value
	}
	if value, ok := decodePatchString(patch, "siteUrl"); ok {
		out.SiteURL = value
	}
	if value, ok := decodePatchString(patch, "imageUrl"); ok {
		out.ImageURL = value
	}
	if value, ok := decodePatchString(patch, "language"); ok {
		out.Language = value
	}
	if value, ok := decodePatchBool(patch, "explicit"); ok {
		out.Explicit = value
	}
	if value, ok := decodePatchStringSlice(patch, "categories"); ok {
		out.Categories = value
	}
	return out
}

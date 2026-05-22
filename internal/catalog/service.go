package catalog

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("catalog item not found")

type Seed struct {
	MusicArtists    []MusicArtist
	MusicAlbums     []MusicAlbum
	MusicTracks     []MusicTrack
	MusicPlaylists  []MusicPlaylist
	Genres          []GenreSummary
	ShelfLibraries  []ShelfLibrary
	ShelfItems      []ShelfItem
	ShelfAuthors    []ShelfAuthor
	ShelfSeries     []ShelfSeries
	PodcastEpisodes []PodcastEpisode
}

type Service struct {
	mu sync.RWMutex

	musicArtists   []MusicArtist
	musicAlbums    []MusicAlbum
	musicTracks    []MusicTrack
	musicPlaylists []MusicPlaylist
	genres         []GenreSummary

	shelfLibraries  []ShelfLibrary
	shelfItems      []ShelfItem
	shelfAuthors    []ShelfAuthor
	shelfSeries     []ShelfSeries
	podcastEpisodes []PodcastEpisode

	musicArtistByID  map[string]MusicArtist
	musicAlbumByID   map[string]MusicAlbum
	musicTrackByID   map[string]MusicTrack
	playlistByID     map[string]MusicPlaylist
	shelfLibraryByID map[string]ShelfLibrary
	shelfItemByID    map[string]ShelfItem
	shelfAuthorByID  map[string]ShelfAuthor
	shelfSeriesByID  map[string]ShelfSeries
	episodeByID      map[string]PodcastEpisode
}

func NewService(seed Seed) *Service {
	service := &Service{}
	service.applySeed(seed)
	return service
}

func (s *Service) Replace(seed Seed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applySeed(seed)
}

func (s *Service) Overview() Overview {
	s.mu.RLock()
	defer s.mu.RUnlock()

	musicDuration := 0
	for _, track := range s.musicTracks {
		musicDuration += track.DurationSeconds
	}

	audiobooks := 0
	podcasts := 0
	shelfDuration := 0
	for _, item := range s.shelfItems {
		switch item.MediaType {
		case ShelfMediaTypeBook:
			audiobooks++
		case ShelfMediaTypePodcast:
			podcasts++
		}
		shelfDuration += item.DurationSeconds
	}
	for _, episode := range s.podcastEpisodes {
		shelfDuration += episode.DurationSeconds
	}

	return Overview{
		Music: MusicOverview{
			ArtistCount:     len(s.musicArtists),
			AlbumCount:      len(s.musicAlbums),
			TrackCount:      len(s.musicTracks),
			PlaylistCount:   len(s.musicPlaylists),
			GenreCount:      len(s.genres),
			DurationSeconds: musicDuration,
		},
		Shelf: ShelfOverview{
			LibraryCount:    len(s.shelfLibraries),
			ItemCount:       len(s.shelfItems),
			AudiobookCount:  audiobooks,
			PodcastCount:    podcasts,
			EpisodeCount:    len(s.podcastEpisodes),
			AuthorCount:     len(s.shelfAuthors),
			SeriesCount:     len(s.shelfSeries),
			DurationSeconds: shelfDuration,
		},
	}
}

func (s *Service) ListMusicArtists(page PageRequest) Page[MusicArtist] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.musicArtists, page)
}

func (s *Service) MusicArtist(id string) (MusicArtist, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.musicArtistByID[id]
	if !ok {
		return MusicArtist{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListMusicAlbums(page PageRequest) Page[MusicAlbum] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.musicAlbums, page)
}

func (s *Service) MusicAlbum(id string) (MusicAlbum, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.musicAlbumByID[id]
	if !ok {
		return MusicAlbum{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListMusicTracks(page PageRequest) Page[MusicTrack] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.musicTracks, page)
}

func (s *Service) MusicTrack(id string) (MusicTrack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.musicTrackByID[id]
	if !ok {
		return MusicTrack{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListMusicPlaylists(page PageRequest) Page[MusicPlaylist] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.musicPlaylists, page)
}

func (s *Service) MusicPlaylist(id string) (MusicPlaylist, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.playlistByID[id]
	if !ok {
		return MusicPlaylist{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListGenres(page PageRequest) Page[GenreSummary] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.genres, page)
}

func (s *Service) SearchMusic(query string, page PageRequest) MusicSearchResults {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	results := MusicSearchResults{
		Limit:  normalizePage(page).Limit,
		Offset: normalizePage(page).Offset,
	}
	if query == "" {
		return results
	}

	for _, item := range s.musicArtists {
		if containsAny(query, item.Name, item.SortName) {
			results.Artists = append(results.Artists, item)
		}
	}
	for _, item := range s.musicAlbums {
		if containsAny(query, item.Title, item.SortTitle, strings.Join(item.ArtistNames, " ")) {
			results.Albums = append(results.Albums, item)
		}
	}
	for _, item := range s.musicTracks {
		if containsAny(query, item.Title, item.SortTitle, item.AlbumTitle, strings.Join(item.ArtistNames, " ")) {
			results.Tracks = append(results.Tracks, item)
		}
	}
	for _, item := range s.musicPlaylists {
		if containsAny(query, item.Name, item.Description) {
			results.Playlists = append(results.Playlists, item)
		}
	}

	results.Total = len(results.Artists) + len(results.Albums) + len(results.Tracks) + len(results.Playlists)
	return trimMusicSearch(results, page)
}

func (s *Service) ListShelfLibraries(page PageRequest) Page[ShelfLibrary] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.shelfLibraries, page)
}

func (s *Service) ShelfLibrary(id string) (ShelfLibrary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.shelfLibraryByID[id]
	if !ok {
		return ShelfLibrary{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListShelfItems(page PageRequest) Page[ShelfItem] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.shelfItems, page)
}

func (s *Service) ListAudiobooks(page PageRequest) Page[ShelfItem] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ShelfItem, 0)
	for _, item := range s.shelfItems {
		if item.MediaType == ShelfMediaTypeBook {
			items = append(items, item)
		}
	}
	return paginate(items, page)
}

func (s *Service) ShelfItem(id string) (ShelfItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.shelfItemByID[id]
	if !ok {
		return ShelfItem{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListShelfAuthors(page PageRequest) Page[ShelfAuthor] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.shelfAuthors, page)
}

func (s *Service) ShelfAuthor(id string) (ShelfAuthor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.shelfAuthorByID[id]
	if !ok {
		return ShelfAuthor{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListShelfSeries(page PageRequest) Page[ShelfSeries] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.shelfSeries, page)
}

func (s *Service) ShelfSeries(id string) (ShelfSeries, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.shelfSeriesByID[id]
	if !ok {
		return ShelfSeries{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListPodcasts(page PageRequest) Page[ShelfItem] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ShelfItem, 0)
	for _, item := range s.shelfItems {
		if item.MediaType == ShelfMediaTypePodcast {
			items = append(items, item)
		}
	}
	return paginate(items, page)
}

func (s *Service) ListPodcastEpisodes(page PageRequest) Page[PodcastEpisode] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.podcastEpisodes, page)
}

func (s *Service) PodcastEpisode(id string) (PodcastEpisode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.episodeByID[id]
	if !ok {
		return PodcastEpisode{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) SearchShelf(query string, page PageRequest) ShelfSearchResults {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	results := ShelfSearchResults{
		Limit:  normalizePage(page).Limit,
		Offset: normalizePage(page).Offset,
	}
	if query == "" {
		return results
	}

	for _, item := range s.shelfItems {
		if shelfItemMatches(item, query) {
			results.Items = append(results.Items, item)
		}
	}
	for _, item := range s.shelfAuthors {
		if containsAny(query, item.Name, item.SortName) {
			results.Authors = append(results.Authors, item)
		}
	}
	for _, item := range s.shelfSeries {
		if containsAny(query, item.Name, item.Description) {
			results.Series = append(results.Series, item)
		}
	}
	for _, item := range s.podcastEpisodes {
		if containsAny(query, item.Title, item.Subtitle, item.Description) {
			results.Episodes = append(results.Episodes, item)
		}
	}

	results.Total = len(results.Items) + len(results.Authors) + len(results.Series) + len(results.Episodes)
	return trimShelfSearch(results, page)
}

func (s *Service) reindex() {
	s.musicArtistByID = map[string]MusicArtist{}
	s.musicAlbumByID = map[string]MusicAlbum{}
	s.musicTrackByID = map[string]MusicTrack{}
	s.playlistByID = map[string]MusicPlaylist{}
	s.shelfLibraryByID = map[string]ShelfLibrary{}
	s.shelfItemByID = map[string]ShelfItem{}
	s.shelfAuthorByID = map[string]ShelfAuthor{}
	s.shelfSeriesByID = map[string]ShelfSeries{}
	s.episodeByID = map[string]PodcastEpisode{}

	for _, item := range s.musicArtists {
		s.musicArtistByID[item.ID] = item
	}
	for _, item := range s.musicAlbums {
		s.musicAlbumByID[item.ID] = item
	}
	for _, item := range s.musicTracks {
		s.musicTrackByID[item.ID] = item
	}
	for _, item := range s.musicPlaylists {
		s.playlistByID[item.ID] = item
	}
	for _, item := range s.shelfLibraries {
		s.shelfLibraryByID[item.ID] = item
	}
	for _, item := range s.shelfItems {
		s.shelfItemByID[item.ID] = item
	}
	for _, item := range s.shelfAuthors {
		s.shelfAuthorByID[item.ID] = item
	}
	for _, item := range s.shelfSeries {
		s.shelfSeriesByID[item.ID] = item
	}
	for _, item := range s.podcastEpisodes {
		s.episodeByID[item.ID] = item
	}

	sort.Slice(s.musicArtists, func(i, j int) bool { return s.musicArtists[i].Name < s.musicArtists[j].Name })
	sort.Slice(s.musicAlbums, func(i, j int) bool { return s.musicAlbums[i].Title < s.musicAlbums[j].Title })
	sort.Slice(s.musicTracks, func(i, j int) bool { return s.musicTracks[i].Title < s.musicTracks[j].Title })
	sort.Slice(s.musicPlaylists, func(i, j int) bool { return s.musicPlaylists[i].Name < s.musicPlaylists[j].Name })
	sort.Slice(s.shelfLibraries, func(i, j int) bool { return s.shelfLibraries[i].Name < s.shelfLibraries[j].Name })
	sort.Slice(s.shelfItems, func(i, j int) bool { return shelfItemTitle(s.shelfItems[i]) < shelfItemTitle(s.shelfItems[j]) })
	sort.Slice(s.shelfAuthors, func(i, j int) bool { return s.shelfAuthors[i].Name < s.shelfAuthors[j].Name })
	sort.Slice(s.shelfSeries, func(i, j int) bool { return s.shelfSeries[i].Name < s.shelfSeries[j].Name })
	sort.Slice(s.podcastEpisodes, func(i, j int) bool { return s.podcastEpisodes[i].Title < s.podcastEpisodes[j].Title })
	sort.Slice(s.genres, func(i, j int) bool { return s.genres[i].Name < s.genres[j].Name })
}

func (s *Service) applySeed(seed Seed) {
	s.musicArtists = slices.Clone(seed.MusicArtists)
	s.musicAlbums = slices.Clone(seed.MusicAlbums)
	s.musicTracks = slices.Clone(seed.MusicTracks)
	s.musicPlaylists = slices.Clone(seed.MusicPlaylists)
	s.genres = slices.Clone(seed.Genres)
	s.shelfLibraries = slices.Clone(seed.ShelfLibraries)
	s.shelfItems = slices.Clone(seed.ShelfItems)
	s.shelfAuthors = slices.Clone(seed.ShelfAuthors)
	s.shelfSeries = slices.Clone(seed.ShelfSeries)
	s.podcastEpisodes = slices.Clone(seed.PodcastEpisodes)
	s.reindex()
}

func paginate[T any](items []T, page PageRequest) Page[T] {
	page = normalizePage(page)
	total := len(items)
	if page.Offset > total {
		return Page[T]{Items: []T{}, Total: total, Limit: page.Limit, Offset: page.Offset}
	}

	end := page.Offset + page.Limit
	if end > total {
		end = total
	}

	return Page[T]{
		Items:  slices.Clone(items[page.Offset:end]),
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
}

func normalizePage(page PageRequest) PageRequest {
	if page.Limit <= 0 {
		page.Limit = 50
	}
	if page.Limit > 500 {
		page.Limit = 500
	}
	if page.Offset < 0 {
		page.Offset = 0
	}
	return page
}

func containsAny(query string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func trimMusicSearch(results MusicSearchResults, page PageRequest) MusicSearchResults {
	page = normalizePage(page)
	results.Limit = page.Limit
	results.Offset = page.Offset
	results.Artists = paginate(results.Artists, page).Items
	results.Albums = paginate(results.Albums, page).Items
	results.Tracks = paginate(results.Tracks, page).Items
	results.Playlists = paginate(results.Playlists, page).Items
	return results
}

func trimShelfSearch(results ShelfSearchResults, page PageRequest) ShelfSearchResults {
	page = normalizePage(page)
	results.Limit = page.Limit
	results.Offset = page.Offset
	results.Items = paginate(results.Items, page).Items
	results.Authors = paginate(results.Authors, page).Items
	results.Series = paginate(results.Series, page).Items
	results.Episodes = paginate(results.Episodes, page).Items
	return results
}

func shelfItemMatches(item ShelfItem, query string) bool {
	if containsAny(query, item.ID, item.Path, strings.Join(item.Tags, " "), strings.Join(item.Genres, " ")) {
		return true
	}
	if item.Book != nil {
		values := []string{
			item.Book.Title,
			item.Book.Subtitle,
			item.Book.SortTitle,
			item.Book.Description,
			item.Book.Publisher,
			strings.Join(item.Book.Tags, " "),
			strings.Join(item.Book.Genres, " "),
		}
		for _, author := range item.Book.Authors {
			values = append(values, author.Name, author.SortName)
		}
		for _, narrator := range item.Book.Narrators {
			values = append(values, narrator.Name, narrator.SortName)
		}
		for _, series := range item.Book.Series {
			values = append(values, series.Name)
		}
		return containsAny(query, values...)
	}
	if item.Podcast != nil {
		return containsAny(query, item.Podcast.Title, item.Podcast.Author, item.Podcast.Description, item.Podcast.FeedURL, strings.Join(item.Podcast.Categories, " "))
	}
	return false
}

func shelfItemTitle(item ShelfItem) string {
	if item.Book != nil {
		return item.Book.Title
	}
	if item.Podcast != nil {
		return item.Podcast.Title
	}
	return item.ID
}

package catalog

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("catalog item not found")

// Seed is the input to catalog.Service — the projection assembled from
// SQLite at startup / after a scan. Audiobooks and podcasts are kept in
// separate slices because they are independent product domains in Samo;
// there is no shared "library item" umbrella type.
type Seed struct {
	MusicArtists    []MusicArtist
	MusicAlbums     []MusicAlbum
	MusicTracks     []MusicTrack
	MusicPlaylists  []MusicPlaylist
	Genres          []GenreSummary
	Audiobooks      []AudiobookItem
	Podcasts        []PodcastItem
	Contributors    []Contributor
	Series          []Series
	PodcastEpisodes []PodcastEpisode
	// Maps audio file paths from extracted_covers.source_path to cached cover
	// metadata. Used to backfill empty track images_json at catalog load.
	ExtractedCoversBySource map[string]Image
}

type Service struct {
	mu sync.RWMutex

	musicArtists   []MusicArtist
	musicAlbums    []MusicAlbum
	musicTracks    []MusicTrack
	musicPlaylists []MusicPlaylist
	genres         []GenreSummary

	audiobooks      []AudiobookItem
	podcasts        []PodcastItem
	contributors    []Contributor
	series          []Series
	podcastEpisodes []PodcastEpisode

	musicArtistByID         map[string]MusicArtist
	musicAlbumByID          map[string]MusicAlbum
	musicTrackByID          map[string]MusicTrack
	playlistByID            map[string]MusicPlaylist
	audiobookByID           map[string]AudiobookItem
	podcastByID             map[string]PodcastItem
	contributorByID         map[string]Contributor
	seriesByID              map[string]Series
	episodeByID             map[string]PodcastEpisode
	audiobookLibIDs         map[string]struct{}
	podcastLibIDs           map[string]struct{}
	imageByID               map[string]Image
	extractedCoversBySource map[string]Image
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

	audiobookDuration := 0
	for _, item := range s.audiobooks {
		audiobookDuration += item.DurationSeconds
	}

	podcastDuration := 0
	for _, item := range s.podcasts {
		podcastDuration += item.DurationSeconds
	}
	for _, episode := range s.podcastEpisodes {
		podcastDuration += episode.DurationSeconds
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
		Audiobook: AudiobookOverview{
			LibraryCount:     len(s.audiobookLibIDs),
			AudiobookCount:   len(s.audiobooks),
			ContributorCount: len(s.contributors),
			SeriesCount:      len(s.series),
			DurationSeconds:  audiobookDuration,
		},
		Podcast: PodcastOverview{
			LibraryCount:    len(s.podcastLibIDs),
			PodcastCount:    len(s.podcasts),
			EpisodeCount:    len(s.podcastEpisodes),
			DurationSeconds: podcastDuration,
		},
	}
}

// SyncManifestIDs is the full set of current entity IDs per type. Clients use
// it to reconcile deletions during an incremental sync: any locally-mirrored
// row whose ID is absent here was removed server-side and should be dropped.
// Playlist IDs are scoped to the requesting user's visibility.
type SyncManifestIDs struct {
	Artists    []string `json:"artists"`
	Albums     []string `json:"albums"`
	Tracks     []string `json:"tracks"`
	Playlists  []string `json:"playlists"`
	Audiobooks []string `json:"audiobooks"`
	Podcasts   []string `json:"podcasts"`
	Episodes   []string `json:"episodes"`
}

// SyncManifest is the deletion-reconciliation payload for incremental syncs.
// ServerTime is truncated to whole seconds to match SQLite's second-precision
// updated_at: a client that stores it and replays it as updatedSince gets a
// <=1s boundary overlap (a few rows harmlessly re-sent) rather than risking a
// change dropped exactly on the second boundary.
type SyncManifest struct {
	ServerTime time.Time       `json:"serverTime"`
	Counts     map[string]int  `json:"counts"`
	IDs        SyncManifestIDs `json:"ids"`
}

// SyncManifest returns every current entity ID (playlists scoped to userID)
// plus the server clock, for delta-sync deletion reconciliation.
func (s *Service) SyncManifest(userID string) SyncManifest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	artists := idsOf(s.musicArtists, func(a MusicArtist) string { return a.ID })
	albums := idsOf(s.musicAlbums, func(a MusicAlbum) string { return a.ID })
	tracks := idsOf(s.musicTracks, func(t MusicTrack) string { return t.ID })
	audiobooks := idsOf(s.audiobooks, func(a AudiobookItem) string { return a.ID })
	podcasts := idsOf(s.podcasts, func(p PodcastItem) string { return p.ID })
	episodes := idsOf(s.podcastEpisodes, func(e PodcastEpisode) string { return e.ID })

	playlists := make([]string, 0, len(s.musicPlaylists))
	for _, item := range s.musicPlaylists {
		if PlaylistVisibleToUser(item, userID) {
			playlists = append(playlists, item.ID)
		}
	}

	return SyncManifest{
		ServerTime: time.Now().UTC().Truncate(time.Second),
		Counts: map[string]int{
			"artists":    len(artists),
			"albums":     len(albums),
			"tracks":     len(tracks),
			"playlists":  len(playlists),
			"audiobooks": len(audiobooks),
			"podcasts":   len(podcasts),
			"episodes":   len(episodes),
		},
		IDs: SyncManifestIDs{
			Artists:    artists,
			Albums:     albums,
			Tracks:     tracks,
			Playlists:  playlists,
			Audiobooks: audiobooks,
			Podcasts:   podcasts,
			Episodes:   episodes,
		},
	}
}

// -- Music ------------------------------------------------------------------

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

func (s *Service) SetMusicArtistImages(artistID string, images []Image) {
	artistID = strings.TrimSpace(artistID)
	if artistID == "" {
		return
	}
	filtered := nonEmptyImages(images)
	if len(filtered) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	artist, ok := s.musicArtistByID[artistID]
	if !ok {
		return
	}
	artist.Images = filtered
	s.musicArtistByID[artistID] = artist
	for index, item := range s.musicArtists {
		if item.ID == artistID {
			s.musicArtists[index].Images = filtered
			break
		}
	}
	registerCatalogImages(s.imageByID, filtered)
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

func (s *Service) ListMusicPlaylistsForUser(userID string, page PageRequest) Page[MusicPlaylist] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]MusicPlaylist, 0, len(s.musicPlaylists))
	for _, item := range s.musicPlaylists {
		if PlaylistVisibleToUser(item, userID) {
			items = append(items, item)
		}
	}
	items = filterUpdatedSince(items, page.UpdatedSince, func(p MusicPlaylist) *time.Time { return p.UpdatedAt })
	return paginate(items, page)
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

func (s *Service) MusicPlaylistForUser(userID, id string) (MusicPlaylist, error) {
	item, err := s.MusicPlaylist(id)
	if err != nil {
		return MusicPlaylist{}, err
	}
	if !PlaylistVisibleToUser(item, userID) {
		return MusicPlaylist{}, ErrNotFound
	}
	return item, nil
}

func PlaylistVisibleToUser(item MusicPlaylist, userID string) bool {
	if item.Public || strings.TrimSpace(item.OwnerID) == "" {
		return true
	}
	return strings.TrimSpace(userID) != "" && item.OwnerID == strings.TrimSpace(userID)
}

func (s *Service) ListGenres(page PageRequest) Page[GenreSummary] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.genres, page)
}

// -- Audiobooks -------------------------------------------------------------

func (s *Service) ListAudiobooks(page PageRequest) Page[AudiobookItem] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := filterUpdatedSince(s.audiobooks, page.UpdatedSince, func(a AudiobookItem) *time.Time { return a.UpdatedAt })
	return paginate(items, page)
}

func (s *Service) Audiobook(id string) (AudiobookItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.audiobookByID[id]
	if !ok {
		return AudiobookItem{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListContributors(page PageRequest) Page[Contributor] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.contributors, page)
}

func (s *Service) Contributor(id string) (Contributor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.contributorByID[id]
	if !ok {
		return Contributor{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListSeries(page PageRequest) Page[Series] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return paginate(s.series, page)
}

func (s *Service) Series(id string) (Series, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.seriesByID[id]
	if !ok {
		return Series{}, ErrNotFound
	}
	return item, nil
}

// ContributorDetail returns the contributor plus the audiobooks they
// contributed to. The audiobooks are filtered by whether their
// BookMetadata.Authors or BookMetadata.Narrators inline list contains the
// contributor's ID. We don't consult the junction table here because the
// catalog already projects junction rows into the inline lists at hydration
// time — see catalog/sqlite.go.
func (s *Service) ContributorDetail(id string, page PageRequest) (ContributorDetail, error) {
	contributor, err := s.Contributor(id)
	if err != nil {
		return ContributorDetail{}, err
	}
	audiobooks, err := s.AudiobooksForContributor(id, page)
	if err != nil {
		return ContributorDetail{}, err
	}
	return ContributorDetail{Contributor: contributor, Audiobooks: audiobooks}, nil
}

func (s *Service) AudiobooksForContributor(contributorID string, page PageRequest) (Page[AudiobookItem], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	contributorID = strings.TrimSpace(contributorID)
	if _, ok := s.contributorByID[contributorID]; !ok {
		return Page[AudiobookItem]{}, ErrNotFound
	}
	matches := make([]AudiobookItem, 0)
	for _, item := range s.audiobooks {
		if audiobookMatchesContributor(item, contributorID) {
			matches = append(matches, item)
		}
	}
	return paginate(matches, page), nil
}

func (s *Service) SeriesDetail(id string, page PageRequest) (SeriesDetail, error) {
	series, err := s.Series(id)
	if err != nil {
		return SeriesDetail{}, err
	}
	audiobooks, err := s.AudiobooksForSeries(id, page)
	if err != nil {
		return SeriesDetail{}, err
	}
	return SeriesDetail{Series: series, Audiobooks: audiobooks}, nil
}

func (s *Service) AudiobooksForSeries(seriesID string, page PageRequest) (Page[AudiobookItem], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	series, ok := s.seriesByID[seriesID]
	if !ok {
		return Page[AudiobookItem]{}, ErrNotFound
	}
	matches := make([]AudiobookItem, 0, len(series.AudiobookIDs))
	for _, audiobookID := range series.AudiobookIDs {
		item, ok := s.audiobookByID[audiobookID]
		if !ok {
			continue
		}
		matches = append(matches, item)
	}
	return paginate(matches, page), nil
}

func audiobookMatchesContributor(item AudiobookItem, contributorID string) bool {
	if item.Book == nil {
		return false
	}
	for _, ref := range item.Book.Authors {
		if ref.ID == contributorID {
			return true
		}
	}
	for _, ref := range item.Book.Narrators {
		if ref.ID == contributorID {
			return true
		}
	}
	return false
}

// -- Podcasts ---------------------------------------------------------------

func (s *Service) ListPodcasts(page PageRequest) Page[PodcastItem] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := filterUpdatedSince(s.podcasts, page.UpdatedSince, func(p PodcastItem) *time.Time { return p.UpdatedAt })
	return paginate(items, page)
}

func (s *Service) Podcast(id string) (PodcastItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.podcastByID[id]
	if !ok {
		return PodcastItem{}, ErrNotFound
	}
	return item, nil
}

func (s *Service) ListPodcastEpisodes(page PageRequest) Page[PodcastEpisode] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	episodes := filterUpdatedSince(s.podcastEpisodes, page.UpdatedSince, func(e PodcastEpisode) *time.Time { return e.UpdatedAt })
	return paginate(s.withEpisodePodcastTitles(episodes), page)
}

func (s *Service) PodcastEpisode(id string) (PodcastEpisode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.episodeByID[id]
	if !ok {
		return PodcastEpisode{}, ErrNotFound
	}
	return s.enrichEpisodePodcastTitle(item), nil
}

// EpisodesForPodcast returns every episode whose PodcastID matches. Episodes
// sit in newest-first projection order so feed refreshes surface fresh drops
// without the UI needing to know every sorting rule.
func (s *Service) EpisodesForPodcast(podcastID string, page PageRequest) (Page[PodcastEpisode], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	podcastID = strings.TrimSpace(podcastID)
	if _, ok := s.podcastByID[podcastID]; !ok {
		return Page[PodcastEpisode]{}, ErrNotFound
	}
	matches := make([]PodcastEpisode, 0)
	for _, episode := range s.podcastEpisodes {
		if episode.PodcastID == podcastID {
			matches = append(matches, episode)
		}
	}
	return paginate(s.withEpisodePodcastTitles(matches), page), nil
}

func (s *Service) enrichEpisodePodcastTitle(episode PodcastEpisode) PodcastEpisode {
	if episode.PodcastID == "" {
		return episode
	}
	if show, ok := s.podcastByID[episode.PodcastID]; ok {
		episode.PodcastTitle = podcastTitle(show)
	}
	return episode
}

func (s *Service) withEpisodePodcastTitles(episodes []PodcastEpisode) []PodcastEpisode {
	if len(episodes) == 0 {
		return episodes
	}
	out := make([]PodcastEpisode, len(episodes))
	for i, episode := range episodes {
		out[i] = s.enrichEpisodePodcastTitle(episode)
	}
	return out
}

// -- Internal ---------------------------------------------------------------

func (s *Service) reindex() {
	s.musicArtistByID = map[string]MusicArtist{}
	s.musicAlbumByID = map[string]MusicAlbum{}
	s.musicTrackByID = map[string]MusicTrack{}
	s.playlistByID = map[string]MusicPlaylist{}
	s.audiobookByID = map[string]AudiobookItem{}
	s.podcastByID = map[string]PodcastItem{}
	s.contributorByID = map[string]Contributor{}
	s.seriesByID = map[string]Series{}
	s.episodeByID = map[string]PodcastEpisode{}
	s.audiobookLibIDs = map[string]struct{}{}
	s.podcastLibIDs = map[string]struct{}{}
	s.imageByID = map[string]Image{}

	for _, item := range s.musicArtists {
		s.musicArtistByID[item.ID] = item
		registerCatalogImages(s.imageByID, item.Images)
	}
	for _, item := range s.musicAlbums {
		s.musicAlbumByID[item.ID] = item
		registerCatalogImages(s.imageByID, item.Images)
	}
	for _, item := range s.musicTracks {
		s.musicTrackByID[item.ID] = item
		registerCatalogImages(s.imageByID, item.Images)
	}
	for _, item := range s.musicPlaylists {
		s.playlistByID[item.ID] = item
		registerCatalogImages(s.imageByID, item.Images)
	}
	for _, item := range s.audiobooks {
		s.audiobookByID[item.ID] = item
		if item.LibraryID != "" {
			s.audiobookLibIDs[item.LibraryID] = struct{}{}
		}
		if item.Cover != nil {
			registerCatalogImages(s.imageByID, []Image{*item.Cover})
		}
	}
	for _, item := range s.podcasts {
		s.podcastByID[item.ID] = item
		if item.LibraryID != "" {
			s.podcastLibIDs[item.LibraryID] = struct{}{}
		}
		if item.Cover != nil {
			registerCatalogImages(s.imageByID, []Image{*item.Cover})
		}
	}
	for _, item := range s.contributors {
		s.contributorByID[item.ID] = item
		registerCatalogImages(s.imageByID, item.Images)
	}
	for _, item := range s.series {
		s.seriesByID[item.ID] = item
	}
	for _, item := range s.podcastEpisodes {
		s.episodeByID[item.ID] = item
	}

	// Re-link covers from extracted_covers when images_json was wiped, repair
	// stale id-only rows, copy track art onto empty albums, then artists inherit.
	s.registerExtractedCoverCatalog()
	s.backfillMusicImagesFromExtractedCovers()
	s.repairBrokenMusicImageReferences()
	s.enrichAlbumImagesFromTracks()
	s.enrichAlbumImagesFromExtractedCovers()
	s.enrichAlbumAudioQuality()
	s.enrichAlbumAddedAtFromFiles()
	EnrichAudiobookAddedAtFromFiles(s.audiobooks)
	EnrichPodcastAddedAtFromFiles(s.podcasts)
	s.enrichPlaylistImagesFromTracks()

	sort.Slice(s.musicArtists, func(i, j int) bool { return s.musicArtists[i].Name < s.musicArtists[j].Name })
	sort.Slice(s.musicAlbums, func(i, j int) bool { return s.musicAlbums[i].Title < s.musicAlbums[j].Title })
	sort.Slice(s.musicTracks, func(i, j int) bool { return s.musicTracks[i].Title < s.musicTracks[j].Title })
	sort.Slice(s.musicPlaylists, func(i, j int) bool { return s.musicPlaylists[i].Name < s.musicPlaylists[j].Name })
	sort.Slice(s.audiobooks, func(i, j int) bool { return audiobookTitle(s.audiobooks[i]) < audiobookTitle(s.audiobooks[j]) })
	sort.Slice(s.podcasts, func(i, j int) bool { return podcastTitle(s.podcasts[i]) < podcastTitle(s.podcasts[j]) })
	sort.Slice(s.contributors, func(i, j int) bool { return s.contributors[i].Name < s.contributors[j].Name })
	sort.Slice(s.series, func(i, j int) bool { return s.series[i].Name < s.series[j].Name })
	sort.Slice(s.podcastEpisodes, func(i, j int) bool {
		left := podcastEpisodeSortStamp(s.podcastEpisodes[i])
		right := podcastEpisodeSortStamp(s.podcastEpisodes[j])
		if left != right {
			return left > right
		}
		if s.podcastEpisodes[i].Title != s.podcastEpisodes[j].Title {
			return s.podcastEpisodes[i].Title < s.podcastEpisodes[j].Title
		}
		return s.podcastEpisodes[i].ID < s.podcastEpisodes[j].ID
	})
	sort.Slice(s.genres, func(i, j int) bool { return s.genres[i].Name < s.genres[j].Name })
}

func (s *Service) applySeed(seed Seed) {
	s.extractedCoversBySource = seed.ExtractedCoversBySource
	if s.extractedCoversBySource == nil {
		s.extractedCoversBySource = map[string]Image{}
	}
	s.musicArtists = slices.Clone(seed.MusicArtists)
	s.musicAlbums = slices.Clone(seed.MusicAlbums)
	s.musicTracks = slices.Clone(seed.MusicTracks)
	s.musicPlaylists = slices.Clone(seed.MusicPlaylists)
	s.genres = slices.Clone(seed.Genres)
	s.audiobooks = slices.Clone(seed.Audiobooks)
	s.podcasts = slices.Clone(seed.Podcasts)
	s.contributors = slices.Clone(seed.Contributors)
	s.series = slices.Clone(seed.Series)
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

func NormalizePage(page PageRequest) PageRequest {
	return normalizePage(page)
}

func Paginate[T any](items []T, page PageRequest) Page[T] {
	return paginate(items, page)
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

// filterUpdatedSince returns the entries whose UpdatedAt is at or after since.
// A zero since returns items unchanged (no copy) so the non-delta list path is
// untouched. A nil UpdatedAt is treated as "always changed" so a row predating
// updated_at tracking is never hidden from a delta consumer.
func filterUpdatedSince[T any](items []T, since time.Time, updatedAt func(T) *time.Time) []T {
	if since.IsZero() {
		return items
	}
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		ts := updatedAt(item)
		if ts == nil || !ts.Before(since) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func idsOf[T any](items []T, id func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, id(item))
	}
	return out
}

func audiobookTitle(item AudiobookItem) string {
	if item.Book != nil {
		return item.Book.Title
	}
	return item.ID
}

func podcastTitle(item PodcastItem) string {
	if item.Podcast != nil {
		return item.Podcast.Title
	}
	return item.ID
}

func podcastEpisodeSortStamp(item PodcastEpisode) int64 {
	if item.PublishedAt != nil {
		return item.PublishedAt.UnixNano()
	}
	if item.AddedAt != nil {
		return item.AddedAt.UnixNano()
	}
	if item.UpdatedAt != nil {
		return item.UpdatedAt.UnixNano()
	}
	return 0
}

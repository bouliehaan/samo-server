package scanner

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/media"
)

type groupedAudio struct {
	Root  string
	Files []string
}

func (s *Scanner) scanAudiobookLibrary(ctx context.Context, library Library, root string, files []string) error {
	for _, group := range groupAudiobooks(root, files) {
		if err := s.scanAudiobook(ctx, library, root, group); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	return err
}

func (s *Scanner) scanAudiobook(ctx context.Context, library Library, root string, group groupedAudio) error {
	probes, err := s.probeGroup(ctx, root, group.Files)
	if err != nil {
		return err
	}
	if len(probes) == 0 {
		return nil
	}

	commonTags := probes[0].Tags
	sidecar := readBookSidecar(group.Root)
	title := firstNonEmpty(firstTag(commonTags, "album", "title"), sidecar.Title)
	if title == "" {
		title = filepath.Base(group.Root)
	}

	authorNames := splitTag(commonTags, "author", "authors", "album_artist", "artist")
	if len(authorNames) == 0 {
		authorNames = sidecar.Authors
	}
	if len(authorNames) == 0 {
		if parent := filepath.Base(filepath.Dir(group.Root)); parent != "." && parent != string(filepath.Separator) {
			authorNames = authorNamesFromFolder(parent)
		}
	}
	if len(authorNames) == 0 {
		authorNames = []string{"Unknown Author"}
	}

	narratorNames := splitTag(commonTags, "narrator", "narrators", "read_by", "performer", "composer")
	if len(narratorNames) == 0 {
		narratorNames = sidecar.Narrators
	}
	genres := firstNonEmptySlice(splitGenreTag(commonTags, "genre"), sidecar.Genres)
	for _, genre := range genres {
		if err := s.upsertGenre(ctx, string(media.KindAudiobook), genre); err != nil {
			return err
		}
	}

	itemID := stableID("item", library.ID, group.Root)
	duration, sizeBytes := groupDurationAndSize(probes)
	authors := contributorsFromNames(authorNames, "author")
	narrators := contributorsFromNames(narratorNames, "narrator")
	for _, author := range authors {
		if err := s.upsertShelfAuthor(ctx, catalog.ShelfAuthor{ID: author.ID, Name: author.Name, SortName: author.SortName}); err != nil {
			return err
		}
	}

	seriesRefs := seriesRefsFromTags(commonTags)
	if len(seriesRefs) == 0 {
		seriesRefs = sidecar.Series
	}
	for _, ref := range seriesRefs {
		if err := s.upsertShelfSeries(ctx, catalog.ShelfSeries{ID: ref.ID, Name: ref.Name, ItemIDs: []string{itemID}}); err != nil {
			return err
		}
	}

	audioPaths := make([]string, 0, len(probes))
	checksums := make([]string, 0, len(probes))
	for _, probed := range probes {
		audioPaths = append(audioPaths, probed.AudioFile.Path)
		checksums = append(checksums, probed.AudioFile.Checksum)
	}
	cover := s.resolveCover(ctx, group.Root, audioPaths, checksums)
	item := catalog.ShelfItem{
		ID:              itemID,
		LibraryID:       library.ID,
		MediaType:       catalog.ShelfMediaTypeBook,
		MediaKind:       string(media.KindAudiobook),
		Path:            group.Root,
		FolderID:        stableID("folder", group.Root),
		Inode:           fileInode(group.Root),
		SizeBytes:       sizeBytes,
		Genres:          genres,
		DurationSeconds: duration,
		Cover:           cover,
		Book: &catalog.BookMetadata{
			Title:           title,
			Subtitle:        firstTag(commonTags, "subtitle"),
			SortTitle:       firstTag(commonTags, "sorttitle", "titlesort"),
			Authors:         authors,
			Narrators:       narrators,
			Series:          seriesRefs,
			Publisher:       firstNonEmpty(firstTag(commonTags, "publisher", "organization"), sidecar.Publisher),
			PublishedDate:   firstNonEmpty(firstTag(commonTags, "date", "year", "releasedate"), sidecar.PublishedDate),
			PublishedYear:   yearString(firstNonEmpty(firstTag(commonTags, "date", "year", "releasedate"), sidecar.PublishedDate)),
			Description:     firstNonEmpty(firstTag(commonTags, "description", "comment", "summary"), sidecar.Description),
			Language:        firstNonEmpty(firstTag(commonTags, "language", "lang"), sidecar.Language),
			Genres:          genres,
			Tags:            splitGenreTag(commonTags, "tag", "tags"),
			ISBNs:           firstNonEmptySlice(splitTag(commonTags, "isbn", "isbn13", "isbn10"), sidecar.ISBNs),
			Explicit:        boolTag(commonTags, "explicit", "itunesadvisory", "advisory"),
			Abridged:        boolTag(commonTags, "abridged"),
			DurationSeconds: duration,
			ExternalIDs: catalog.ExternalIDs{
				ISBN10:        firstTag(commonTags, "isbn10"),
				ISBN13:        firstTag(commonTags, "isbn13", "isbn"),
				ASIN:          firstTag(commonTags, "asin"),
				AudibleASIN:   firstTag(commonTags, "audible_asin", "audibleasin"),
				GoogleBooksID: firstTag(commonTags, "google_books_id", "googlebooksid"),
				OpenLibraryID: firstTag(commonTags, "openlibrary_id", "openlibraryid"),
			},
		},
	}
	if err := s.upsertShelfItem(ctx, item); err != nil {
		return err
	}
	if s.activeScan != nil {
		s.activeScan.seeItem(item.ID)
	}
	if err := s.setShelfItemAuthors(ctx, item.ID, append(authors, narrators...)); err != nil {
		return err
	}
	if err := s.setShelfItemSeries(ctx, item.ID, seriesRefs); err != nil {
		return err
	}

	chapters := flattenBookChapters(probes)
	if err := s.replaceChapters(ctx, item.ID, "", chapters); err != nil {
		return err
	}

	for _, probed := range probes {
		file := probed.AudioFile
		file.ID = stableID("file", file.Path)
		if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{ItemID: item.ID}, file); err != nil {
			return err
		}
	}

	return nil
}

func (s *Scanner) scanPodcastLibrary(ctx context.Context, library Library, root string, files []string) error {
	for _, group := range groupPodcasts(root, files) {
		if err := s.scanPodcast(ctx, library, root, group); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	return err
}

func (s *Scanner) scanPodcast(ctx context.Context, library Library, root string, group groupedAudio) error {
	probes, err := s.probeGroup(ctx, root, group.Files)
	if err != nil {
		return err
	}
	if len(probes) == 0 {
		return nil
	}

	commonTags := probes[0].Tags
	title := firstTag(commonTags, "album", "show", "podcast", "album_artist")
	if title == "" {
		title = filepath.Base(group.Root)
	}
	itemID := stableID("podcast", library.ID, group.Root)
	duration, sizeBytes := groupDurationAndSize(probes)
	categories := splitGenreTag(commonTags, "genre", "category", "categories")
	audioPaths := make([]string, 0, len(probes))
	checksums := make([]string, 0, len(probes))
	for _, probed := range probes {
		audioPaths = append(audioPaths, probed.AudioFile.Path)
		checksums = append(checksums, probed.AudioFile.Checksum)
	}
	cover := s.resolveCover(ctx, group.Root, audioPaths, checksums)

	item := catalog.ShelfItem{
		ID:              itemID,
		LibraryID:       library.ID,
		MediaType:       catalog.ShelfMediaTypePodcast,
		MediaKind:       string(media.KindPodcast),
		Path:            group.Root,
		FolderID:        stableID("folder", group.Root),
		Inode:           fileInode(group.Root),
		SizeBytes:       sizeBytes,
		Genres:          categories,
		DurationSeconds: duration,
		Cover:           cover,
		Podcast: &catalog.PodcastMetadata{
			Title:        title,
			Author:       firstTag(commonTags, "artist", "author", "album_artist"),
			Description:  firstTag(commonTags, "description", "comment", "summary"),
			FeedURL:      firstTag(commonTags, "podcasturl", "feed", "feed_url", "rss"),
			SiteURL:      firstTag(commonTags, "url", "website"),
			Language:     firstTag(commonTags, "language"),
			Explicit:     boolTag(commonTags, "explicit", "itunesadvisory", "advisory"),
			Categories:   categories,
			OwnerName:    firstTag(commonTags, "owner", "owner_name"),
			OwnerEmail:   firstTag(commonTags, "owner_email"),
			EpisodeCount: len(probes),
			ExternalIDs: catalog.ExternalIDs{
				FeedGUID: firstTag(commonTags, "podcast_guid", "guid"),
				ITunesID: firstTag(commonTags, "itunes_id"),
			},
		},
	}
	if err := s.upsertShelfItem(ctx, item); err != nil {
		return err
	}
	if s.activeScan != nil {
		s.activeScan.seeItem(item.ID)
	}

	for _, probed := range probes {
		tags := probed.Tags
		file := probed.AudioFile
		file.ID = stableID("file", file.Path)
		episodeID := stableID("episode", item.ID, file.RelativePath)
		episodeNumber, _ := parseNumberPair(firstTag(tags, "episode", "track", "tracknumber"))
		season, _ := strconv.Atoi(firstTag(tags, "season"))
		episode := catalog.PodcastEpisode{
			ID:              episodeID,
			LibraryID:       library.ID,
			PodcastID:       item.ID,
			Title:           titleOrFile(tags, file.Path),
			Subtitle:        firstTag(tags, "subtitle"),
			Description:     firstTag(tags, "description", "comment", "summary"),
			PublishedAt:     parseDatePtr(firstTag(tags, "date", "releasedate", "pubdate")),
			Season:          season,
			Episode:         episodeNumber,
			EpisodeType:     firstTag(tags, "episode_type", "episodetype"),
			DurationSeconds: file.DurationSeconds,
			Explicit:        boolTag(tags, "explicit", "itunesadvisory", "advisory"),
			EnclosureURL:    firstTag(tags, "enclosure_url", "enclosureurl", "url"),
			EnclosureType:   file.MimeType,
			EnclosureBytes:  file.SizeBytes,
			ExternalIDs: catalog.ExternalIDs{
				FeedGUID: firstTag(tags, "guid", "episode_guid"),
				ITunesID: firstTag(tags, "itunes_id"),
			},
		}
		if err := s.upsertPodcastEpisode(ctx, episode); err != nil {
			return err
		}
		if s.activeScan != nil {
			s.activeScan.seeEpisode(episode.ID)
		}
		if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{ItemID: item.ID, EpisodeID: episode.ID}, file); err != nil {
			return err
		}
		if err := s.replaceChapters(ctx, item.ID, episode.ID, probed.Chapters); err != nil {
			return err
		}
	}

	return nil
}

type probedFile struct {
	AudioFile catalog.AudioFile
	Tags      catalog.Tags
	Chapters  []catalog.AudioChapter
}

func (s *Scanner) probeGroup(ctx context.Context, root string, files []string) ([]probedFile, error) {
	probes := make([]probedFile, 0, len(files))
	for _, path := range files {
		probe, err := s.probe(ctx, path)
		if err != nil {
			return nil, err
		}
		relPath, _ := filepath.Rel(root, path)
		probe.AudioFile.RelativePath = relPath
		probes = append(probes, probedFile{
			AudioFile: probe.AudioFile,
			Tags:      probe.Tags,
			Chapters:  probe.Chapters,
		})
	}
	sort.Slice(probes, func(i, j int) bool {
		discI, trackI := mediaOrder(probes[i].Tags, probes[i].AudioFile.Path)
		discJ, trackJ := mediaOrder(probes[j].Tags, probes[j].AudioFile.Path)
		if discI != discJ {
			return discI < discJ
		}
		if trackI != trackJ {
			return trackI < trackJ
		}
		return probes[i].AudioFile.RelativePath < probes[j].AudioFile.RelativePath
	})
	return probes, nil
}

func groupAudiobooks(root string, files []string) []groupedAudio {
	return groupByRoot(root, files, func(parts []string) string {
		switch {
		case len(parts) >= 3:
			return filepath.Join(root, parts[0], parts[1])
		case len(parts) >= 2:
			return filepath.Join(root, parts[0])
		case len(parts) == 1:
			return filepath.Join(root, parts[0])
		default:
			return root
		}
	})
}

func groupPodcasts(root string, files []string) []groupedAudio {
	return groupByRoot(root, files, func(parts []string) string {
		if len(parts) >= 2 {
			return filepath.Join(root, parts[0])
		}
		return root
	})
}

func groupByRoot(root string, files []string, keyFunc func(parts []string) string) []groupedAudio {
	groups := map[string][]string{}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(rel, string(filepath.Separator))
		groupRoot := keyFunc(parts)
		groups[groupRoot] = append(groups[groupRoot], path)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]groupedAudio, 0, len(keys))
	for _, key := range keys {
		sort.Strings(groups[key])
		out = append(out, groupedAudio{Root: key, Files: groups[key]})
	}
	return out
}

func groupDurationAndSize(probes []probedFile) (int, int64) {
	var duration int
	var size int64
	for _, probe := range probes {
		duration += probe.AudioFile.DurationSeconds
		size += probe.AudioFile.SizeBytes
	}
	return duration, size
}

func contributorsFromNames(names []string, role string) []catalog.Contributor {
	contributors := make([]catalog.Contributor, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		contributors = append(contributors, catalog.Contributor{
			ID:   stableID("person", name),
			Name: name,
			Role: role,
		})
	}
	return contributors
}

func seriesRefsFromTags(tags catalog.Tags) []catalog.SeriesRef {
	names := splitTag(tags, "series", "series_name", "grouping")
	refs := make([]catalog.SeriesRef, 0, len(names))
	for _, name := range names {
		ref := catalog.SeriesRef{
			ID:           stableID("series", name),
			Name:         name,
			SequenceText: firstTag(tags, "series_part", "series_sequence", "series_index"),
		}
		if ref.SequenceText != "" {
			ref.Sequence, _ = strconv.ParseFloat(ref.SequenceText, 64)
		}
		refs = append(refs, ref)
	}
	return refs
}

func flattenBookChapters(probes []probedFile) []catalog.AudioChapter {
	var chapters []catalog.AudioChapter
	offset := 0
	for _, probe := range probes {
		if len(probe.Chapters) == 0 {
			chapters = append(chapters, catalog.AudioChapter{
				Index:        len(chapters) + 1,
				Title:        titleOrFile(probe.Tags, probe.AudioFile.Path),
				StartSeconds: offset,
				EndSeconds:   offset + probe.AudioFile.DurationSeconds,
			})
			offset += probe.AudioFile.DurationSeconds
			continue
		}

		for _, chapter := range probe.Chapters {
			chapter.Index = len(chapters) + 1
			chapter.StartSeconds += offset
			chapter.EndSeconds += offset
			chapters = append(chapters, chapter)
		}
		offset += probe.AudioFile.DurationSeconds
	}
	return chapters
}

func titleOrFile(tags catalog.Tags, path string) string {
	title := firstTag(tags, "title")
	if title != "" {
		return title
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func yearString(value string) string {
	year := yearFromDate(value)
	if year == 0 {
		return ""
	}
	return strconv.Itoa(year)
}

func parseDatePtr(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	formats := []string{time.RFC3339, "2006-01-02", "2006"}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

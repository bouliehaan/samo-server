package scanner

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/media"
)

// scanAudiobookLibrary walks an audiobook library root and writes one
// audiobook row per detected folder. Audiobooks are the "Author/Series/
// Book Title/*.m4b" convention by default. The grouping function in
// groupAudiobooks tolerates both nested and flat layouts.
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

	commonTags := mergeProbeTags(probes)
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

	audiobookID := stableID("audiobook", library.ID, group.Root)
	duration, sizeBytes := groupDurationAndSize(probes)
	authors := contributorRefsFromNames(authorNames, "author")
	narrators := contributorRefsFromNames(narratorNames, "narrator")

	seriesRefs := seriesRefsFromTags(commonTags)
	if len(seriesRefs) == 0 {
		seriesRefs = sidecar.Series
	}
	for _, ref := range seriesRefs {
		if err := s.upsertSeries(ctx, catalog.Series{ID: ref.ID, Name: ref.Name, AudiobookIDs: []string{audiobookID}}); err != nil {
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
	item := catalog.AudiobookItem{
		ID:              audiobookID,
		LibraryID:       library.ID,
		Path:            group.Root,
		FolderID:        stableID("folder", group.Root),
		Inode:           fileInode(group.Root),
		SizeBytes:       sizeBytes,
		Genres:          genres,
		Tags:            splitGenreTag(commonTags, "tag", "tags"),
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
			Explicit:        explicitTag(commonTags),
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
	if err := s.upsertAudiobook(ctx, item); err != nil {
		return err
	}
	if s.activeScan != nil {
		s.activeScan.seeAudiobook(item.ID)
	}
	// setAudiobookContributors upserts every contributor (authors + narrators)
	// into `contributors` before linking, so callers do NOT need to upsert
	// them first. Keeping the upsert solely inside setAudiobookContributors
	// closes the FK timebomb that was the original "shelf author FK
	// failed" bug — narrators were never upserted before they were
	// referenced from the join row.
	if err := s.setAudiobookContributors(ctx, item.ID, append(authors, narrators...)); err != nil {
		return err
	}
	if err := s.setAudiobookSeries(ctx, item.ID, seriesRefs); err != nil {
		return err
	}

	chapters := flattenBookChapters(probes)
	if len(chapters) == 0 {
		chapters = readCueChapters(group.Root, probes)
	}
	if err := s.replaceAudiobookChapters(ctx, item.ID, chapters); err != nil {
		return err
	}

	for _, probed := range probes {
		file := probed.AudioFile
		file.ID = stableID("file", file.Path)
		if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{AudiobookID: item.ID}, file); err != nil {
			return err
		}
	}

	return nil
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

func contributorRefsFromNames(names []string, role string) []catalog.ContributorRef {
	refs := make([]catalog.ContributorRef, 0, len(names))
	for _, name := range names {
		name = trimName(name)
		if name == "" {
			continue
		}
		refs = append(refs, catalog.ContributorRef{
			ID:   stableID("person", name),
			Name: name,
			Role: role,
		})
	}
	return refs
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

func yearString(value string) string {
	year := yearFromDate(value)
	if year == 0 {
		return ""
	}
	return strconv.Itoa(year)
}

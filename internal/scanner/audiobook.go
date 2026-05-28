package scanner

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/media"
)

var partFolderPattern = regexp.MustCompile(`(?i)^(?:disc|disk|cd|part|vol(?:ume)?|chapter|ch(?:apter)?)\s*[\._-]?\s*\d+`)
var chapterFilenamePattern = regexp.MustCompile(`(?i)(?:chapter|ch(?:apter)?|part)[\._-]?\d+|^\d{1,3}\s+-\s+\S`)
var musicTrackFilenamePattern = regexp.MustCompile(`(?i)^\d{1,2}([._-]?(?:track|trk)?)?$`)

// scanAudiobookLibrary walks an audiobook library root and writes one
// audiobook row per detected folder. Audiobooks are the "Author/Series/
// Book Title/*.m4b" convention by default. The grouping function in
// groupAudiobooks tolerates both nested and flat layouts.
func (s *Scanner) scanAudiobookLibrary(ctx context.Context, library Library, root string, files []string) error {
	groups := splitAudiobookGroups(groupAudiobooks(root, files))
	log.Printf("scanner: audiobook library %q: %d files in %d book group(s)", library.Name, len(files), len(groups))
	persisted := 0
	for index, group := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.onActivity != nil {
			s.onActivity(fmt.Sprintf("audiobook %d/%d: %s", index+1, len(groups), filepath.Base(group.Root)))
		}
		log.Printf("scanner: audiobook library %q: book %d/%d %q (%d files)",
			library.Name, index+1, len(groups), group.Root, len(group.Files))
		if err := s.scanAudiobook(ctx, library, root, group); err != nil {
			log.Printf("scanner: audiobook group %q failed: %v", group.Root, err)
			continue
		}
		persisted++
	}
	log.Printf("scanner: audiobook library %q: persisted %d book group(s)", library.Name, persisted)
	return nil
}

func (s *Scanner) scanAudiobook(ctx context.Context, library Library, root string, group groupedAudio) error {
	probes, err := s.probeGroup(ctx, library.ID, root, group.Files, "audiobook_id")
	if err != nil {
		return err
	}
	if len(probes) == 0 && len(group.Files) == 0 {
		return nil
	}
	if len(probes) == 0 {
		log.Printf("scanner: audiobook %q: no ffprobe results for %d file(s); persisting from path/sidecar metadata",
			group.Root, len(group.Files))
	}
	return s.persistAudiobookGroup(ctx, library, root, group, probes)
}

func (s *Scanner) persistAudiobookGroup(ctx context.Context, library Library, root string, group groupedAudio, probes []probedFile) error {
	var commonTags catalog.Tags
	if len(probes) > 0 {
		commonTags = mergeProbeTags(probes)
	}
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
	if len(probes) == 0 {
		sizeBytes = groupSizeBytesFromPaths(group.Files)
	}
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
	var embeddedKnown *bool
	for _, probed := range probes {
		if probed.HasEmbeddedCover {
			known := true
			embeddedKnown = &known
			break
		}
	}
	cover := s.resolveCover(ctx, group.Root, audioPaths, checksums, embeddedKnown)
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
	var upsertErr error
	audiobookID, upsertErr = s.upsertAudiobook(ctx, item)
	if upsertErr != nil {
		return upsertErr
	}
	item.ID = audiobookID
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

	if len(probes) > 0 || s.groupNeedsProbe(group.Files) {
		chapters := flattenBookChapters(probes)
		if len(chapters) == 0 {
			chapters = readCueChapters(group.Root, probes)
		}
		// Do not wipe existing chapters when probing fails or provides
		// no chapter markers; preserve previously indexed chapter rows.
		if len(chapters) > 0 {
			if err := s.replaceAudiobookChapters(ctx, item.ID, chapters); err != nil {
				return err
			}
		}
	}

	if len(probes) > 0 {
		for _, probed := range probes {
			file := probed.AudioFile
			file.ID = stableID("file", file.Path)
			if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{AudiobookID: item.ID}, file, "", ""); err != nil {
				log.Printf("scanner: skip audiobook file %q: %v", file.Path, err)
				continue
			}
		}
		return nil
	}
	for _, path := range group.Files {
		file, err := audioFileFromPath(root, path)
		if err != nil {
			log.Printf("scanner: skip audiobook file %q: %v", path, err)
			continue
		}
		file.ID = stableID("file", file.Path)
		if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{AudiobookID: item.ID}, file, "", ""); err != nil {
			log.Printf("scanner: skip audiobook file %q: %v", file.Path, err)
			continue
		}
	}
	return nil
}

func groupSizeBytesFromPaths(files []string) int64 {
	var total int64
	for _, path := range files {
		info, err := statWithTimeout(path, pathStatTimeout)
		if err == nil {
			total += info.Size()
		}
	}
	return total
}

func audioFileFromPath(root, path string) (catalog.AudioFile, error) {
	info, err := statWithTimeout(path, pathStatTimeout)
	if err != nil {
		return catalog.AudioFile{}, err
	}
	rel, _ := filepath.Rel(root, path)
	return catalog.AudioFile{
		Path:         path,
		RelativePath: rel,
		FileName:     filepath.Base(path),
		SizeBytes:    info.Size(),
		ModifiedAt:   timePtr(info.ModTime().UTC()),
		Checksum:     fileChecksum(path, info),
	}, nil
}

func timePtr(value time.Time) *time.Time {
	v := value
	return &v
}

func groupAudiobooks(root string, files []string) []groupedAudio {
	return groupByRoot(root, files, func(parts []string) string {
		return audiobookGroupKey(root, parts)
	})
}

func splitAudiobookGroups(groups []groupedAudio) []groupedAudio {
	if len(groups) == 0 {
		return nil
	}
	out := make([]groupedAudio, 0, len(groups))
	for _, group := range groups {
		out = append(out, splitAudiobookGroup(group)...)
	}
	return out
}

func splitAudiobookGroup(group groupedAudio) []groupedAudio {
	if !looksLikeSeparateBooksInFolder(group.Files) {
		return []groupedAudio{group}
	}
	out := make([]groupedAudio, 0, len(group.Files))
	for _, path := range group.Files {
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		out = append(out, groupedAudio{
			Root:  filepath.Join(filepath.Dir(path), stem),
			Files: []string{path},
		})
	}
	return out
}

func looksLikeChapterBundle(files []string) bool {
	if len(files) < 3 {
		return false
	}
	var totalSize int64
	largeFiles := 0
	chapterNamed := 0
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		totalSize += info.Size()
		if info.Size() > 30*1024*1024 {
			largeFiles++
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		if chapterFilenamePattern.MatchString(base) {
			chapterNamed++
		}
	}
	avgSize := totalSize / int64(len(files))
	if len(files) >= 3 && chapterNamed*2 >= len(files) {
		return true
	}
	if len(files) >= 3 && avgSize < 25*1024*1024 && largeFiles == 0 && chapterNamed > 0 {
		return true
	}
	return len(files) >= 8 && avgSize < 40*1024*1024
}

func looksLikeMusicAlbum(files []string) bool {
	if len(files) < 2 || len(files) > 30 {
		return false
	}
	numbered := 0
	for _, path := range files {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if musicTrackFilenamePattern.MatchString(strings.TrimSpace(base)) {
			numbered++
		}
	}
	return numbered*2 >= len(files)
}

func looksLikeSeparateBooksInFolder(files []string) bool {
	if len(files) < 2 || looksLikeChapterBundle(files) {
		return false
	}
	largeFiles := 0
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() >= 15*1024*1024 {
			largeFiles++
		}
	}
	return largeFiles >= 2 && largeFiles == len(files)
}

func audiobookGroupKey(libraryRoot string, parts []string) string {
	switch len(parts) {
	case 0:
		return libraryRoot
	case 1:
		// Flat layout: /Library/Book.mp3 → one book per file, not one catch-all
		// row for the entire library root.
		return audiobookBookRootFromFilePart(libraryRoot, parts[0])
	case 2:
		fileStem := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
		if chapterFilenamePattern.MatchString(fileStem) || looksLikePartFolder(parts[1]) {
			// /Library/Book Title/01 - Chapter One.mp3 → chapters of one book.
			return filepath.Join(libraryRoot, parts[0])
		}
		// /Library/Author/Book.mp3 → one book per file, not one row per author.
		return filepath.Join(libraryRoot, parts[0], fileStem)
	default:
		return audiobookGroupRootFromRelParts(libraryRoot, parts[:len(parts)-1])
	}
}

func audiobookBookRootFromFilePart(libraryRoot, filePart string) string {
	stem := strings.TrimSuffix(filePart, filepath.Ext(filePart))
	return filepath.Join(libraryRoot, stem)
}

func audiobookGroupRootFromDir(libraryRoot, dir string) string {
	rel, err := filepath.Rel(libraryRoot, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Clean(dir)
	}
	return audiobookGroupRootFromRelParts(libraryRoot, strings.Split(rel, string(filepath.Separator)))
}

func audiobookGroupRootFromRelParts(libraryRoot string, dirParts []string) string {
	for len(dirParts) > 1 && looksLikePartFolder(dirParts[len(dirParts)-1]) {
		dirParts = dirParts[:len(dirParts)-1]
	}
	if len(dirParts) == 0 {
		return libraryRoot
	}
	return filepath.Join(append([]string{libraryRoot}, dirParts...)...)
}

func looksLikePartFolder(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	return partFolderPattern.MatchString(name)
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
			end := offset + probe.AudioFile.DurationSeconds
			if end <= offset {
				end = offset + 1
			}
			chapters = append(chapters, catalog.AudioChapter{
				Index:        len(chapters) + 1,
				Title:        titleOrFile(probe.Tags, probe.AudioFile.Path),
				StartSeconds: offset,
				EndSeconds:   end,
			})
			offset = end
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
	return normalizeAudiobookChapters(probes, chapters)
}

func yearString(value string) string {
	year := yearFromDate(value)
	if year == 0 {
		return ""
	}
	return strconv.Itoa(year)
}

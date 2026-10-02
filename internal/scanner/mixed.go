package scanner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dhowden/tag"
)

// scanMixedLibrary walks a "mixed" root and routes each subfolder bundle
// into the right domain scanner: audiobooks, podcasts, or music. Each
// top-level subfolder is classified once; loose files at the root default
// to music.
//
// Audiobook detection is the most conservative path because its signals
// are strongest (sidecars, .m4b containers, single huge files). Podcast
// detection is the second-most-conservative — show-name folders full of
// "Show - Episode N.mp3" entries, or any folder whose existing scan history
// already wrote it to the podcasts table. Anything that fails both falls
// back to music, which matches user intent for the common case of a
// mixed library that is mostly an album collection.
func (s *Scanner) scanMixedLibrary(ctx context.Context, library Library, root string, files []string, state *scanState) error {
	groups := splitMixedGroups(root, files)

	if libraryRootLooksLikePodcast(root) {
		return s.scanPodcastLibrary(ctx, library, root, files)
	}

	for _, group := range splitAudiobookGroups(groups.audiobooks) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.scanAudiobook(ctx, library, root, group); err != nil {
			return err
		}
	}
	for _, group := range groups.podcasts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.scanPodcast(ctx, library, root, group); err != nil {
			return err
		}
	}
	if len(groups.music) > 0 {
		if err := s.scanMusicLibraryByFolder(ctx, library, root, groups.music, state); err != nil {
			return err
		}
	}
	return nil
}

type mixedGroups struct {
	audiobooks []groupedAudio
	podcasts   []groupedAudio
	music      []string
}

func splitMixedGroups(root string, files []string) mixedGroups {
	if len(files) == 0 {
		return mixedGroups{}
	}

	// Group files by their nearest containing folder so classification can
	// look at the bundle as a whole (sidecars, file count, total duration
	// proxies).
	folders := map[string][]string{}
	folderOrder := make([]string, 0)
	for _, file := range files {
		folder := filepath.Dir(file)
		if _, seen := folders[folder]; !seen {
			folderOrder = append(folderOrder, folder)
		}
		folders[folder] = append(folders[folder], file)
	}
	sort.Strings(folderOrder)

	out := mixedGroups{}
	rootAbs := filepath.Clean(root)
	for _, folder := range folderOrder {
		folderFiles := folders[folder]
		sort.Strings(folderFiles)
		if folder == rootAbs {
			// Loose files at the top of the library default to music.
			out.music = append(out.music, folderFiles...)
			continue
		}
		tags := &folderTags{files: folderFiles}
		switch {
		case classifyMixedFolderAsAudiobook(folder, folderFiles, tags):
			bookRoot := audiobookGroupRootFromDir(rootAbs, folder)
			out.audiobooks = mergeGroup(out.audiobooks, bookRoot, folderFiles)
		case classifyMixedFolderAsPodcast(folder, folderFiles, tags):
			showRoot := audiobookGroupRootFromDir(rootAbs, folder)
			out.podcasts = mergeGroup(out.podcasts, showRoot, folderFiles)
		default:
			out.music = append(out.music, folderFiles...)
		}
	}

	// Sort groups for deterministic output.
	sort.Slice(out.audiobooks, func(i, j int) bool {
		return out.audiobooks[i].Root < out.audiobooks[j].Root
	})
	sort.Slice(out.podcasts, func(i, j int) bool {
		return out.podcasts[i].Root < out.podcasts[j].Root
	})
	sort.Strings(out.music)
	return out
}

// mergeGroup merges files into an existing group with the same root, or
// appends a new group if none exists. Used by both audiobook and podcast
// classification to dedupe disc subfolders / season subfolders under one
// logical bundle.
func mergeGroup(groups []groupedAudio, root string, files []string) []groupedAudio {
	for index, group := range groups {
		if group.Root == root {
			groups[index].Files = append(groups[index].Files, files...)
			sort.Strings(groups[index].Files)
			return groups
		}
	}
	return append(groups, groupedAudio{Root: root, Files: append([]string(nil), files...)})
}

// classifyFolderAsPodcast picks out the podcast-shaped folders inside a
// mixed library. Signals (any one wins):
//   - .opml / podcasts.json sidecar
//   - a "podcast" genre tag
//   - episode naming ("Episode 12", "Ep. 3", "S02E05") on at least half the files
//   - "Show Name - ..." repeated across files, when the tags carry no album
//     evidence and the shared prefix is not simply the files' artist
//   - a large bundle (>= 8) that is neither track-numbered nor tagged as an album
//
// Durations are NOT used because we don't probe in classification — too slow
// for a synchronous scan — so a borderline show may need its parent folder
// configured as a real podcast library.
func classifyFolderAsPodcast(folder string, files []string) bool {
	return classifyMixedFolderAsPodcast(folder, files, &folderTags{files: files})
}

func classifyMixedFolderAsPodcast(folder string, files []string, tags *folderTags) bool {
	if len(files) < 3 {
		return false
	}
	for _, name := range []string{"podcasts.json", "podcast.json", "feed.opml", "feed.xml", "podcast.opml"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
			return true
		}
	}
	if majorityMatch(files, episodeFilenamePattern) {
		return true
	}
	if tags.load().podcast {
		return true
	}
	if tags.album {
		// Album and track number on the files themselves: this is music
		// whatever the filenames look like.
		return false
	}
	// "Show Name - Episode Title" is the most common episode naming
	// convention. "Artist - Title" looks identical, so a prefix that is just
	// the files' own artist tag is music.
	prefix := ""
	matched := 0
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		dash := strings.Index(base, " - ")
		if dash <= 0 {
			continue
		}
		candidate := strings.TrimSpace(base[:dash])
		if prefix == "" {
			prefix = candidate
			matched = 1
			continue
		}
		if strings.EqualFold(candidate, prefix) {
			matched++
		}
	}
	if prefix != "" && matched*2 >= len(files) && !strings.EqualFold(prefix, tags.artist) {
		return true
	}
	// Old-time radio and serial podcast folders often have many episodes
	// with inconsistent filenames — treat large episode bundles as shows.
	// An album is a large bundle too, so track-numbered files are not.
	if len(files) >= 8 && !majorityMatch(files, numberedTrackFilenamePattern) {
		return true
	}
	return false
}

// classifyFolderAsAudiobook decides whether a single folder's contents look
// like an audiobook bundle rather than music tracks. It is intentionally
// conservative: only strong audiobook signals (a path hint, sidecars, .m4b
// containers, chapter naming, a spoken-word genre tag, or one-file long-form
// audio) trigger the audiobook path. Everything else falls back to music.
//
// "01 - Title.flac" is NOT a chapter signal here. It is how almost every
// ripper and tagger names album tracks, and treating it as one turned every
// album in a mixed library into an audiobook.
func classifyFolderAsAudiobook(folder string, files []string) bool {
	return classifyMixedFolderAsAudiobook(folder, files, &folderTags{files: files})
}

func classifyMixedFolderAsAudiobook(folder string, files []string, tags *folderTags) bool {
	if len(files) == 0 {
		return false
	}
	if audiobookPathHint(folder) {
		return true
	}
	if hasAudiobookSidecar(folder) {
		return true
	}
	// Walk up one level too — an audiobook with disc subfolders often has
	// the sidecar at the audiobook root, not the disc root.
	if parent := filepath.Dir(folder); parent != folder {
		if hasAudiobookSidecar(parent) {
			return true
		}
	}
	// `.m4b` is the de-facto audiobook container.
	for _, file := range files {
		if strings.EqualFold(filepath.Ext(file), ".m4b") {
			return true
		}
	}
	// Multi-file audiobooks named for what they are: "Chapter One",
	// "Part 03", "ch12".
	if len(files) >= 3 && majorityMatch(files, chapterWordFilenamePattern) {
		return true
	}
	if tags.load().spoken {
		return true
	}
	// A long run of numbered parts with no album tags is a book split into
	// files; albums with more than 30 tracks in one folder are rare enough.
	if len(files) > 30 && !tags.album && looksLikeChapterBundle(files) {
		return true
	}
	// Single-file audiobooks, including ones smaller than legacy 50MB cutoff.
	if len(files) == 1 && !tags.album {
		info, err := os.Stat(files[0])
		if err != nil {
			return false
		}
		ext := strings.ToLower(filepath.Ext(files[0]))
		switch {
		case info.Size() > 50*1024*1024:
			return true
		case ext == ".m4b":
			return info.Size() > 1024
		case ext == ".mp3", ext == ".m4a", ext == ".opus", ext == ".flac":
			return info.Size() >= 5*1024*1024
		}
	}
	return false
}

var (
	// "Chapter One", "chapter_03", "Part 2", "Pt. 4", "ch12". Explicitly
	// named parts of a book; a bare "01 - Title" is deliberately absent.
	chapterWordFilenamePattern = regexp.MustCompile(`(?i)\b(?:chapter|chapitre|kapitel)\b|\b(?:ch|part|pt)(?:\.|[\s_-])*\d+`)
	// "Episode 12", "ep-3", "S02E05".
	episodeFilenamePattern = regexp.MustCompile(`(?i)\b(?:episode|ep)(?:\.|[\s_-])*\d+|\bs\d{1,2}e\d{1,3}\b`)
	// "01", "01 Title", "01 - Title", "1. Title", "01-title": how albums are named.
	numberedTrackFilenamePattern = regexp.MustCompile(`^\d{1,3}(?:$|[\s._-])`)
)

// majorityMatch reports whether at least half of the files' names (without
// extension) match pattern.
func majorityMatch(files []string, pattern *regexp.Regexp) bool {
	if len(files) == 0 {
		return false
	}
	matched := 0
	for _, file := range files {
		if pattern.MatchString(strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))) {
			matched++
		}
	}
	return matched*2 >= len(files)
}

// folderTags is what a folder's own tags say about it, read from the first
// file that has any — headers only, never ffprobe — and only when the cheaper
// filename and sidecar signals have not already decided.
type folderTags struct {
	files []string
	read  bool

	spoken  bool   // an audiobook / spoken-word genre
	podcast bool   // a podcast genre
	album   bool   // an album tag and a track number: music
	artist  string // the first file's artist, to tell "Artist - Title" from "Show - Episode"
}

func (t *folderTags) load() *folderTags {
	if t.read {
		return t
	}
	t.read = true
	for index, path := range t.files {
		if index >= 3 {
			break
		}
		var meta tag.Metadata
		err := recoverToError("tag sniff of "+path, func() error {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			meta, err = tag.ReadFrom(file)
			return err
		})
		if err != nil || meta == nil {
			continue
		}
		genre := strings.ToLower(meta.Genre())
		for _, word := range []string{"audiobook", "audio book", "hörbuch", "horbuch", "livre audio", "audiolibro", "spoken", "speech"} {
			if strings.Contains(genre, word) {
				t.spoken = true
			}
		}
		t.podcast = strings.Contains(genre, "podcast")
		track, _ := meta.Track()
		t.album = strings.TrimSpace(meta.Album()) != "" && track > 0 && !t.spoken && !t.podcast
		t.artist = strings.TrimSpace(meta.Artist())
		return t
	}
	return t
}

func audiobookPathHint(folder string) bool {
	for _, segment := range strings.Split(strings.ToLower(filepath.Clean(folder)), string(filepath.Separator)) {
		switch segment {
		case "audiobook", "audiobooks", "audible", "books":
			return true
		}
	}
	return false
}

func hasAudiobookSidecar(folder string) bool {
	for _, name := range []string{"metadata.json", "desc.txt", "reader.txt", "book.nfo"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
			return true
		}
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if strings.HasSuffix(lower, ".opf") {
			return true
		}
	}
	return false
}

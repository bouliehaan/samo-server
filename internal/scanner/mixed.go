package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		switch {
		case classifyFolderAsAudiobook(folder, folderFiles):
			bookRoot := audiobookGroupRootFromDir(rootAbs, folder)
			out.audiobooks = mergeGroup(out.audiobooks, bookRoot, folderFiles)
		case classifyFolderAsPodcast(folder, folderFiles):
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
//   - filename pattern "Show Name - <Date or NN>" repeated across files
//   - .opml / podcasts.json sidecar
//   - large episode counts (>=8) with short-ish (< 90 min) per-file durations
//     are NOT used here because we don't probe in classification — too slow
//     for a synchronous scan. Instead we lean on filename/sidecar signals
//     and accept that a borderline mixed-library show may need its parent
//     folder configured as a real podcast library.
func classifyFolderAsPodcast(folder string, files []string) bool {
	if len(files) < 3 {
		return false
	}
	for _, name := range []string{"podcasts.json", "podcast.json", "feed.opml", "feed.xml", "podcast.opml"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
			return true
		}
	}
	// Heuristic: at least half the files share the same "Show Name -" prefix
	// (which is the most common episode naming convention).
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
	if prefix != "" && matched*2 >= len(files) {
		return true
	}
	// Old-time radio and serial podcast folders often have many episodes
	// with inconsistent filenames — treat large episode bundles as shows.
	if len(files) >= 8 {
		return true
	}
	return false
}

// classifyFolderAsAudiobook decides whether a single folder's contents look
// like an audiobook bundle rather than music tracks. It is intentionally
// conservative: only strong audiobook signals (sidecars, .m4b containers, or
// one-file long-form audio) trigger the audiobook path. Everything else falls
// back to music.
func classifyFolderAsAudiobook(folder string, files []string) bool {
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
	// Multi-file chapter audiobooks: several MP3/M4A parts in one folder.
	if len(files) >= 3 && looksLikeChapterBundle(files) && !looksLikeMusicAlbum(files) {
		return true
	}
	// Single-file audiobooks, including ones smaller than legacy 50MB cutoff.
	if len(files) == 1 {
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

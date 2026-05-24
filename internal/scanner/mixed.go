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
func (s *Scanner) scanMixedLibrary(ctx context.Context, library Library, root string, files []string) error {
	groups := splitMixedGroups(root, files)

	for _, group := range groups.audiobooks {
		if err := s.scanAudiobook(ctx, library, root, group); err != nil {
			return err
		}
	}
	for _, group := range groups.podcasts {
		if err := s.scanPodcast(ctx, library, root, group); err != nil {
			return err
		}
	}
	for _, path := range groups.music {
		if err := s.scanMusicFile(ctx, library, root, path); err != nil {
			return err
		}
	}

	_, err := s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	return err
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
			// Use the highest-level audiobook root (the folder directly
			// under the library root, or the actual folder if it is one).
			bookRoot := audiobookRoot(rootAbs, folder)
			out.audiobooks = mergeGroup(out.audiobooks, bookRoot, folderFiles)
		case classifyFolderAsPodcast(folder, folderFiles):
			showRoot := audiobookRoot(rootAbs, folder)
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

func audiobookRoot(libraryRoot, folder string) string {
	// Walk up until the parent is the library root. The first child of the
	// library root is the audiobook root, even if the actual audio file
	// lives deeper (e.g. `book-name/Disc 1/track-01.mp3`).
	current := filepath.Clean(folder)
	for {
		parent := filepath.Dir(current)
		if parent == libraryRoot || parent == current {
			return current
		}
		current = parent
	}
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
	// Single large file in a folder is almost always an audiobook chapter.
	if len(files) == 1 {
		if info, err := os.Stat(files[0]); err == nil && info.Size() > 50*1024*1024 {
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

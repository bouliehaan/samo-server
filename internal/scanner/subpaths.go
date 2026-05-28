package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func normalizeSubpaths(subpaths []string) []string {
	if len(subpaths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(subpaths))
	out := make([]string, 0, len(subpaths))
	for _, raw := range subpaths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if _, ok := seen[absolute]; ok {
			continue
		}
		seen[absolute] = struct{}{}
		out = append(out, absolute)
	}
	sort.Strings(out)
	return out
}

func pathUnderSubpath(path string, subpaths []string) bool {
	if len(subpaths) == 0 {
		return true
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, root := range subpaths {
		if pathHasPrefix(absolute, root) {
			return true
		}
	}
	return false
}

func pathHasPrefix(path, root string) bool {
	if path == root {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(path, root+sep)
}

func filterFilesUnderSubpaths(files, subpaths []string) []string {
	subpaths = normalizeSubpaths(subpaths)
	if len(subpaths) == 0 {
		return files
	}
	filtered := make([]string, 0, len(files))
	for _, path := range files {
		if pathUnderSubpath(path, subpaths) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func countAudioFilesInSubpaths(ctx context.Context, root string, subpaths []string) (int, error) {
	files, err := audioFiles(ctx, root, nil)
	if err != nil {
		return 0, err
	}
	return len(filterFilesUnderSubpaths(files, subpaths)), nil
}

// ResolveIncrementalScanRoot returns the smallest directory to rescan for a
// filesystem event. Music and sidecar changes rescan the containing album
// folder; directory events rescan the directory itself.
func ResolveIncrementalScanRoot(eventPath string) (string, error) {
	path, err := filepath.Abs(strings.TrimSpace(eventPath))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return path, nil
	}
	return filepath.Dir(path), nil
}

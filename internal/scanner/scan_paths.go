package scanner

import (
	"path/filepath"
	"strings"
)

func normalizeScanPath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

func pathSeen(seen map[string]struct{}, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if _, ok := seen[path]; ok {
		return true
	}
	_, ok := seen[normalizeScanPath(path)]
	return ok
}

func buildSeenPathSet(seenPaths map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(seenPaths)*2)
	for p := range seenPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out[p] = struct{}{}
		out[normalizeScanPath(p)] = struct{}{}
	}
	return out
}

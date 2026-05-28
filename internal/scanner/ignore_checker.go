package scanner

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const scanIgnoreFile = ".ndignore"
const ignoreReadTimeout = 3 * time.Second

// IgnoreChecker stacks .ndignore patterns while walking a library tree (Navidrome).
type IgnoreChecker struct {
	stack [][]string
	flat  []string
}

func newIgnoreChecker() *IgnoreChecker {
	return &IgnoreChecker{stack: [][]string{}}
}

func (ic *IgnoreChecker) pushDir(absDir string) {
	patterns := loadIgnorePatterns(absDir)
	ic.stack = append(ic.stack, patterns)
	ic.rebuild()
}

func (ic *IgnoreChecker) popDir() {
	if len(ic.stack) == 0 {
		return
	}
	ic.stack = ic.stack[:len(ic.stack)-1]
	ic.rebuild()
}

func (ic *IgnoreChecker) resetForDir(root, dir string) {
	ic.stack = ic.stack[:0]
	ic.flat = ic.flat[:0]
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	ic.pushDir(root)
	if dir == root {
		return
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return
	}
	current := root
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		ic.pushDir(current)
	}
}

func (ic *IgnoreChecker) shouldIgnore(relPath string) bool {
	relPath = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(relPath)), "./")
	if relPath == "" || relPath == "." {
		return false
	}
	for _, pattern := range ic.flat {
		if matchIgnorePattern(pattern, relPath) {
			return true
		}
	}
	return false
}

func (ic *IgnoreChecker) rebuild() {
	ic.flat = ic.flat[:0]
	for _, level := range ic.stack {
		ic.flat = append(ic.flat, level...)
	}
}

func loadIgnorePatterns(dir string) []string {
	path := filepath.Join(dir, scanIgnoreFile)
	data, err := readIgnoreFileWithTimeout(path, ignoreReadTimeout)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		// Empty .ndignore ignores everything in this folder (Navidrome behavior).
		return []string{"**"}
	}
	var patterns []string
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

func readIgnoreFileWithTimeout(path string, timeout time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := os.ReadFile(path)
		done <- result{data: data, err: err}
	}()
	select {
	case res := <-done:
		return res.data, res.err
	case <-time.After(timeout):
		log.Printf("scanner: read %q timed out after %s; skipping ignore file", path, timeout)
		return nil, os.ErrDeadlineExceeded
	}
}

func matchIgnorePattern(pattern, relPath string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	relPath = filepath.ToSlash(relPath)
	if pattern == "" {
		return false
	}
	if pattern == "**" || pattern == "*" {
		return true
	}
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		prefix := strings.Trim(strings.TrimSuffix(parts[0], "/"), "/")
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.Trim(strings.TrimPrefix(parts[1], "/"), "/")
		}
		if prefix != "" && !strings.HasPrefix(relPath, prefix) {
			return false
		}
		rest := relPath
		if prefix != "" {
			rest = strings.TrimPrefix(rest, prefix)
			rest = strings.TrimPrefix(rest, "/")
		}
		if suffix == "" {
			return true
		}
		return strings.Contains(rest, suffix) || strings.HasSuffix(rest, suffix)
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(relPath, pattern[1:])
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(filepath.Base(relPath), strings.TrimPrefix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "/*") {
		dir := strings.TrimSuffix(pattern, "/*")
		return relPath == dir || strings.HasPrefix(relPath, dir+"/")
	}
	return relPath == pattern || strings.HasPrefix(relPath, pattern+"/")
}

// ShouldIgnoreLibraryPath reports whether a path under a library root should be
// skipped during walks. Used by the filesystem watcher.
func ShouldIgnoreLibraryPath(root, absPath string) bool {
	root = filepath.Clean(root)
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(root, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	ic := newIgnoreChecker()
	ic.resetForDir(root, filepath.Dir(absPath))
	return ic.shouldIgnore(filepath.ToSlash(rel))
}

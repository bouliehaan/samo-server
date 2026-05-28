package scanner

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const walkReadDirTimeout = 15 * time.Second

// ignoreWalkStack maintains .ndignore patterns while walking a library tree.
// Push/pop on directory entry keeps work O(directories) instead of rebuilding
// the stack per file.
type ignoreWalkStack struct {
	root  string
	ic    *IgnoreChecker
	depth int
}

func newIgnoreWalkStack(root string) *ignoreWalkStack {
	root = filepath.Clean(root)
	ic := newIgnoreChecker()
	ic.pushDir(root)
	return &ignoreWalkStack{root: root, ic: ic}
}

func (w *ignoreWalkStack) enterDir(path string) (skip bool) {
	path = filepath.Clean(path)
	d := relDepthUnderRoot(w.root, path)
	if path != w.root {
		for w.depth >= d && w.depth > 0 {
			w.ic.popDir()
			w.depth--
		}
		w.ic.pushDir(path)
		w.depth++
	}
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return false
	}
	return w.ic.shouldIgnore(filepath.ToSlash(rel))
}

func (w *ignoreWalkStack) shouldIgnorePath(path string) bool {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return false
	}
	return w.ic.shouldIgnore(filepath.ToSlash(rel))
}

// walkLibraryDir enumerates files under root. Unlike filepath.WalkDir, each
// directory read is time-bounded so a stuck NFS/SMB mount cannot freeze scans.
func walkLibraryDir(ctx context.Context, root string, fn func(path string, entry os.DirEntry) error) error {
	root = filepath.Clean(root)
	stack := newIgnoreWalkStack(root)

	type pendingDir struct {
		path string
	}
	dirs := []pendingDir{{path: root}}

	for len(dirs) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]
		dir := current.path

		if dir != root {
			name := filepath.Base(dir)
			if strings.HasPrefix(name, ".") {
				continue
			}
		}
		if stack.enterDir(dir) {
			continue
		}

		entries, err := readDirWithTimeout(dir, walkReadDirTimeout)
		if err != nil {
			log.Printf("scanner: skip directory %q: %v", dir, err)
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") && path != root {
					continue
				}
				dirs = append(dirs, pendingDir{path: path})
				continue
			}
			if stack.shouldIgnorePath(path) {
				continue
			}
			if err := fn(path, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

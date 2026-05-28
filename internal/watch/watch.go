package watch

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/fsnotify/fsnotify"
)

type LibraryRoot struct {
	ID   string
	Path string
}

type Options struct {
	DB             *sql.DB
	ScanSubpaths   func(context.Context, string, []string) (libraries.ScanResult, error)
	ListLibraries  func(context.Context) ([]LibraryRoot, error)
	ScanInProgress func() bool
	Debounce       time.Duration
	Logger         *log.Logger
}

type Watcher struct {
	db             *sql.DB
	scanSubpaths   func(context.Context, string, []string) (libraries.ScanResult, error)
	listLibraries  func(context.Context) ([]LibraryRoot, error)
	scanInProgress func() bool
	debounce       time.Duration
	logger         *log.Logger
}

func New(options Options) *Watcher {
	debounce := options.Debounce
	if debounce <= 0 {
		debounce = 3 * time.Second
	}
	logger := options.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Watcher{
		db:             options.DB,
		scanSubpaths:   options.ScanSubpaths,
		listLibraries:  options.ListLibraries,
		scanInProgress: options.ScanInProgress,
		debounce:       debounce,
		logger:         logger,
	}
}

func (w *Watcher) Run(ctx context.Context) error {
	if w.scanSubpaths == nil {
		return errors.New("watcher scan callback is nil")
	}
	if w.listLibraries == nil {
		return errors.New("watcher library loader is nil")
	}
	roots, err := w.listLibraries(ctx)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return nil
	}
	if w.db == nil {
		return errors.New("watcher database is nil")
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsWatcher.Close()

	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.Path)
	}
	if err := addLibraryWatches(fsWatcher, paths); err != nil {
		return err
	}
	w.logger.Printf("watching %d configured library path(s)", len(roots))

	trigger := make(chan struct{}, 1)
	done := make(chan struct{})
	pending := newPendingChanges()
	go w.scanLoop(ctx, trigger, done, pending, roots)
	defer func() {
		close(trigger)
		<-done
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-fsWatcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := addRecursive(fsWatcher, event.Name); err != nil {
						w.logger.Printf("failed to watch new directory %q: %v", event.Name, err)
					}
				}
			}
			if interestingEvent(event) {
				if change, ok := resolveLibraryChange(event.Name, roots); ok {
					pending.add(change)
					notify(trigger)
				}
			}
		case err, ok := <-fsWatcher.Errors:
			if !ok {
				return nil
			}
			w.logger.Printf("filesystem watcher error: %v", err)
		}
	}
}

type libraryChange struct {
	libraryID string
	subpath   string
}

type pendingChanges struct {
	mu      sync.Mutex
	changes map[string]map[string]struct{}
}

func newPendingChanges() *pendingChanges {
	return &pendingChanges{changes: map[string]map[string]struct{}{}}
}

func (p *pendingChanges) add(change libraryChange) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changes[change.libraryID] == nil {
		p.changes[change.libraryID] = map[string]struct{}{}
	}
	p.changes[change.libraryID][change.subpath] = struct{}{}
}

func (p *pendingChanges) drain() map[string][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string][]string, len(p.changes))
	for libraryID, paths := range p.changes {
		list := make([]string, 0, len(paths))
		for path := range paths {
			list = append(list, path)
		}
		out[libraryID] = list
	}
	p.changes = map[string]map[string]struct{}{}
	return out
}

func resolveLibraryChange(eventPath string, roots []LibraryRoot) (libraryChange, bool) {
	absolute, err := filepath.Abs(strings.TrimSpace(eventPath))
	if err != nil {
		return libraryChange{}, false
	}
	subpath, err := scanner.ResolveIncrementalScanRoot(absolute)
	if err != nil {
		return libraryChange{}, false
	}
	for _, root := range roots {
		if pathUnderRoot(subpath, root.Path) {
			return libraryChange{libraryID: root.ID, subpath: subpath}, true
		}
	}
	return libraryChange{}, false
}

func pathUnderRoot(path, root string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if absolute == rootAbs {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(absolute, rootAbs+sep)
}

func (w *Watcher) scanLoop(ctx context.Context, trigger <-chan struct{}, done chan<- struct{}, pending *pendingChanges, roots []LibraryRoot) {
	defer close(done)

	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case _, ok := <-trigger:
			if !ok {
				if timer != nil {
					timer.Stop()
				}
				return
			}
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				timerC = timer.C
			} else {
				timer.Reset(w.debounce)
			}
		case <-timerC:
			timer = nil
			timerC = nil
			if w.scanInProgress != nil && w.scanInProgress() {
				timer = time.NewTimer(w.debounce)
				timerC = timer.C
				continue
			}
			w.rescan(ctx, pending.drain())
		}
	}
}

func (w *Watcher) rescan(ctx context.Context, grouped map[string][]string) {
	if len(grouped) == 0 {
		return
	}
	started := time.Now()
	for libraryID, subpaths := range grouped {
		w.logger.Printf("library change detected; incremental scan of %d folder(s) in library %s", len(subpaths), libraryID)
		if _, err := w.scanSubpaths(ctx, libraryID, subpaths); err != nil {
			w.logger.Printf("watch-triggered scan failed for library %s: %v", libraryID, err)
		}
	}
	w.logger.Printf("incremental library scan finished in %s", time.Since(started).Round(time.Millisecond))
}

func addLibraryWatches(watcher *fsnotify.Watcher, roots []string) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if err := addRecursive(watcher, absolute); err != nil {
			return err
		}
	}
	return nil
}

func addRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") && path != root {
			return filepath.SkipDir
		}
		if scanner.ShouldIgnoreLibraryPath(root, path) {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

func interestingEvent(event fsnotify.Event) bool {
	if event.Name == "" {
		return false
	}
	if event.Has(fsnotify.Chmod) && !event.Has(fsnotify.Write) {
		return false
	}
	return isInterestingPath(event.Name)
}

func isInterestingPath(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	if name == "desc.txt" || name == "description.txt" || name == "summary.txt" || name == "reader.txt" || name == "narrator.txt" || name == "narrators.txt" || name == "metadata.json" {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".alac", ".flac", ".m4a", ".m4b", ".mp3", ".ogg", ".opus", ".wav", ".wma",
		".opf", ".nfo", ".cue", ".jpg", ".jpeg", ".png", ".webp", ".m3u", ".m3u8":
		return true
	default:
		return false
	}
}

func notify(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

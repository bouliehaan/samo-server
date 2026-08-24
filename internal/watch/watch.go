package watch

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/safego"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/fsnotify/fsnotify"
)

type LibraryRoot struct {
	ID   string
	Path string
}

type Options struct {
	DB           *sql.DB
	ScanSubpaths func(context.Context, string, []string) (libraries.ScanResult, error)
	// ScanLibrary rescans a whole library. Removals need it: a subpath scan
	// only adds and updates — the phases that mark files missing and prune
	// stale rows are skipped when the scan is scoped to subpaths, so a
	// deleted album would linger in the catalog forever.
	ScanLibrary    func(context.Context, string) (libraries.ScanResult, error)
	ListLibraries  func(context.Context) ([]LibraryRoot, error)
	ScanInProgress func() bool
	Debounce       time.Duration
	// Resync is how often the watcher re-reads the library list and repairs
	// incomplete coverage. Without it the watcher would be frozen to whatever
	// libraries existed at boot.
	Resync time.Duration
	Logger *log.Logger
}

type Watcher struct {
	db             *sql.DB
	scanSubpaths   func(context.Context, string, []string) (libraries.ScanResult, error)
	scanLibrary    func(context.Context, string) (libraries.ScanResult, error)
	listLibraries  func(context.Context) ([]LibraryRoot, error)
	scanInProgress func() bool
	debounce       time.Duration
	resync         time.Duration
	logger         *log.Logger
}

func New(options Options) *Watcher {
	debounce := options.Debounce
	if debounce <= 0 {
		debounce = 3 * time.Second
	}
	resync := options.Resync
	if resync <= 0 {
		resync = 30 * time.Second
	}
	logger := options.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Watcher{
		db:             options.DB,
		scanSubpaths:   options.ScanSubpaths,
		scanLibrary:    options.ScanLibrary,
		listLibraries:  options.ListLibraries,
		scanInProgress: options.ScanInProgress,
		debounce:       debounce,
		resync:         resync,
		logger:         logger,
	}
}

// Run supervises the filesystem watch for the whole life of the process. It
// deliberately never gives up: a server that boots before any library is
// configured, a library added later from the UI, a NAS that mounts after
// startup, and a watch that fails to attach all have to heal on their own —
// otherwise "drop a folder in and it shows up" quietly stops working until
// somebody restarts the server.
func (w *Watcher) Run(ctx context.Context) error {
	if w.scanSubpaths == nil {
		return errors.New("watcher scan callback is nil")
	}
	if w.listLibraries == nil {
		return errors.New("watcher library loader is nil")
	}
	if w.db == nil {
		return errors.New("watcher database is nil")
	}

	announced := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		roots, err := w.listLibraries(ctx)
		if err != nil {
			w.logger.Printf("watcher could not load the library list: %v", err)
			if waitErr := sleep(ctx, w.resync); waitErr != nil {
				return waitErr
			}
			continue
		}
		if len(roots) == 0 {
			if !announced {
				w.logger.Printf("no libraries to watch yet; will keep checking every %s", w.resync)
				announced = true
			}
			if waitErr := sleep(ctx, w.resync); waitErr != nil {
				return waitErr
			}
			continue
		}
		announced = false
		if err := w.watchCycle(ctx, roots); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.logger.Printf("library watch restarting after error: %v", err)
			if waitErr := sleep(ctx, w.resync); waitErr != nil {
				return waitErr
			}
		}
	}
}

// watchCycle watches one fixed set of library roots. It returns nil when the
// library list itself changed, so Run can start a fresh cycle against the new
// roots. Everything else is repaired in place.
func (w *Watcher) watchCycle(ctx context.Context, roots []LibraryRoot) error {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsWatcher.Close()

	watched := dirSet{}
	unreachable := w.addLibraryWatches(fsWatcher, roots, watched)
	w.logger.Printf("watching %d library path(s), %d folder(s) total", len(roots), len(watched))

	trigger := make(chan struct{}, 1)
	done := make(chan struct{})
	pending := newPendingChanges()
	safego.Go("library watch scan loop", func() { w.scanLoop(ctx, trigger, done, pending) })
	defer func() {
		close(trigger)
		<-done
	}()

	resync := time.NewTicker(w.resync)
	defer resync.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resync.C:
			next, err := w.listLibraries(ctx)
			if err != nil {
				w.logger.Printf("watcher could not refresh the library list: %v", err)
			} else if rootsChanged(roots, next) {
				w.logger.Printf("library list changed; rebuilding filesystem watches")
				return nil
			}
			// A root that walked to nothing is usually a share that has not
			// mounted yet or a folder that does not exist yet. Attach it in
			// place rather than rebuilding the cycle: a teardown would drop
			// every event that lands while the new watcher is being built.
			unreachable = w.retryUnreachable(fsWatcher, unreachable, watched)
		case event, ok := <-fsWatcher.Events:
			if !ok {
				return errors.New("filesystem watcher closed")
			}
			w.handleEvent(fsWatcher, event, roots, watched, pending, trigger)
		case err, ok := <-fsWatcher.Errors:
			if !ok {
				return errors.New("filesystem watcher closed")
			}
			w.logger.Printf("filesystem watcher error: %v", err)
		}
	}
}

// dirSet remembers every directory we hold a watch on. It is how a Remove
// event can tell "an album folder was deleted" from "a stray file was
// deleted" — once the path is gone, the filesystem can no longer answer that.
// Only the watchCycle event loop touches it, so it needs no lock.
type dirSet map[string]struct{}

func (d dirSet) forget(path string) bool {
	_, ok := d[path]
	if ok {
		delete(d, path)
	}
	prefix := path + string(os.PathSeparator)
	for known := range d {
		if strings.HasPrefix(known, prefix) {
			delete(d, known)
			ok = true
		}
	}
	return ok
}

func (w *Watcher) handleEvent(
	fsWatcher *fsnotify.Watcher,
	event fsnotify.Event,
	roots []LibraryRoot,
	watched dirSet,
	pending *pendingChanges,
	trigger chan<- struct{},
) {
	if event.Name == "" {
		return
	}
	if event.Has(fsnotify.Chmod) && !event.Has(fsnotify.Write) {
		return
	}
	path, err := filepath.Abs(strings.TrimSpace(event.Name))
	if err != nil {
		return
	}
	root, libraryID, ok := owningRoot(path, roots)
	if !ok {
		return
	}
	if hiddenUnderRoot(root, path) || scanner.ShouldIgnoreLibraryPath(root, path) {
		return
	}

	info, statErr := os.Stat(path)
	isDir := statErr == nil && info.IsDir()

	// Dragging a folder into the library is a rename on the same filesystem:
	// one Create for the folder, and nothing for the files inside because they
	// were never written here — they simply moved. Watching the folder is not
	// enough; the folder event itself is the change, or the drop is never seen.
	if isDir && event.Has(fsnotify.Create) {
		if _, failed, firstErr := w.addRecursive(fsWatcher, root, path, watched); failed > 0 {
			w.logger.Printf("could not watch %d folder(s) under new directory %q (first error: %v)", failed, path, firstErr)
		}
	}

	gone := event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)
	if gone {
		// Something left the tree. Whole-library reconcile: only a library-scoped
		// scan runs the phases that mark files missing and prune stale rows.
		wasDir := watched.forget(path)
		if wasDir || isInterestingPath(path) {
			pending.reconcile(libraryID)
			notify(trigger)
		}
		return
	}

	if !isDir && !isInterestingPath(path) {
		return
	}
	subpath := path
	if !isDir {
		subpath = filepath.Dir(path)
	}
	pending.add(libraryID, subpath)
	notify(trigger)
}

// hiddenUnderRoot reports whether any folder between the library root and path
// is a dot-directory. addRecursive refuses to watch those, so events from them
// (a NAS .Trash, .Spotlight-V100, a half-written .tmp staging folder) must not
// start scans either.
func hiddenUnderRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if len(part) > 1 && strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// owningRoot reports which library contains path.
func owningRoot(path string, roots []LibraryRoot) (string, string, bool) {
	for _, root := range roots {
		rootAbs, err := filepath.Abs(strings.TrimSpace(root.Path))
		if err != nil {
			continue
		}
		if path == rootAbs || strings.HasPrefix(path, rootAbs+string(os.PathSeparator)) {
			return rootAbs, root.ID, true
		}
	}
	return "", "", false
}

// libraryPending is the work owed to one library: either a set of folders to
// rescan, or a whole-library reconcile that supersedes them.
type libraryPending struct {
	subpaths map[string]struct{}
	full     bool
}

type pendingChanges struct {
	mu      sync.Mutex
	changes map[string]*libraryPending
}

func newPendingChanges() *pendingChanges {
	return &pendingChanges{changes: map[string]*libraryPending{}}
}

func (p *pendingChanges) entry(libraryID string) *libraryPending {
	if p.changes[libraryID] == nil {
		p.changes[libraryID] = &libraryPending{subpaths: map[string]struct{}{}}
	}
	return p.changes[libraryID]
}

func (p *pendingChanges) add(libraryID, subpath string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entry(libraryID).subpaths[subpath] = struct{}{}
}

func (p *pendingChanges) reconcile(libraryID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entry(libraryID).full = true
}

type pendingScan struct {
	libraryID string
	subpaths  []string
	full      bool
}

func (p *pendingChanges) drain() []pendingScan {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]pendingScan, 0, len(p.changes))
	for libraryID, entry := range p.changes {
		scan := pendingScan{libraryID: libraryID, full: entry.full}
		if !entry.full {
			scan.subpaths = make([]string, 0, len(entry.subpaths))
			for path := range entry.subpaths {
				scan.subpaths = append(scan.subpaths, path)
			}
			sort.Strings(scan.subpaths)
		}
		out = append(out, scan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].libraryID < out[j].libraryID })
	p.changes = map[string]*libraryPending{}
	return out
}

func (w *Watcher) scanLoop(ctx context.Context, trigger <-chan struct{}, done chan<- struct{}, pending *pendingChanges) {
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

func (w *Watcher) rescan(ctx context.Context, scans []pendingScan) {
	for _, scan := range scans {
		if scan.full && w.scanLibrary != nil {
			w.logger.Printf("files removed from library %s; reconciling the whole library", scan.libraryID)
			if _, err := w.scanLibrary(ctx, scan.libraryID); err != nil {
				w.logger.Printf("watch-triggered library scan failed for %s: %v", scan.libraryID, err)
			}
			continue
		}
		if len(scan.subpaths) == 0 {
			continue
		}
		w.logger.Printf("library change detected; incremental scan of %d folder(s) in library %s", len(scan.subpaths), scan.libraryID)
		if _, err := w.scanSubpaths(ctx, scan.libraryID, scan.subpaths); err != nil {
			w.logger.Printf("watch-triggered scan failed for library %s: %v", scan.libraryID, err)
		}
	}
}

// addLibraryWatches attaches watches to every library root and returns the
// roots it could not reach. An unreachable root is logged, not fatal, so one
// bad path cannot stop the other libraries from being watched.
func (w *Watcher) addLibraryWatches(fsWatcher *fsnotify.Watcher, roots []LibraryRoot, watched dirSet) []string {
	var unreachable []string
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			w.logger.Printf("skipping unusable library path %q: %v", path, err)
			continue
		}
		added, failed, firstErr := w.addRecursive(fsWatcher, absolute, absolute, watched)
		if failed > 0 && added > 0 {
			w.logger.Printf("could not watch %d folder(s) under %q (first error: %v); the rest are watched", failed, absolute, firstErr)
		}
		if added == 0 {
			w.logger.Printf("library path %q is not watchable yet (%v); retrying every %s", absolute, firstErr, w.resync)
			unreachable = append(unreachable, absolute)
		}
	}
	return unreachable
}

// retryUnreachable re-attempts the roots that had no coverage, returning the
// ones still out of reach. A missing root fails its walk immediately, so this
// stays cheap even when a share never comes back.
func (w *Watcher) retryUnreachable(fsWatcher *fsnotify.Watcher, unreachable []string, watched dirSet) []string {
	if len(unreachable) == 0 {
		return nil
	}
	var still []string
	for _, root := range unreachable {
		// Stay quiet on repeat failures: a path that is simply wrong would
		// otherwise log on every tick for the life of the process.
		added, _, _ := w.addRecursive(fsWatcher, root, root, watched)
		if added > 0 {
			w.logger.Printf("library path %q is reachable again; watching %d folder(s)", root, added)
			continue
		}
		still = append(still, root)
	}
	return still
}

// addRecursive watches dir and everything under it, reporting how many folders
// it attached to, how many it could not, and the first failure. Errors are
// skipped rather than returned: a single unreadable folder, or hitting the
// kernel's watch limit partway through a big library, must not leave the
// library unwatched entirely.
func (w *Watcher) addRecursive(fsWatcher *fsnotify.Watcher, root, dir string, watched dirSet) (int, int, error) {
	added := 0
	failed := 0
	var firstErr error
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed++
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") && path != dir {
			return filepath.SkipDir
		}
		if scanner.ShouldIgnoreLibraryPath(root, path) {
			return filepath.SkipDir
		}
		if _, ok := watched[path]; ok {
			return nil
		}
		if err := fsWatcher.Add(path); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed++
			return nil
		}
		watched[path] = struct{}{}
		added++
		return nil
	})
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return added, failed, firstErr
}

func rootsChanged(current, next []LibraryRoot) bool {
	if len(current) != len(next) {
		return true
	}
	key := func(roots []LibraryRoot) []string {
		out := make([]string, 0, len(roots))
		for _, root := range roots {
			out = append(out, root.ID+"\x00"+filepath.Clean(strings.TrimSpace(root.Path)))
		}
		sort.Strings(out)
		return out
	}
	a, b := key(current), key(next)
	for i := range a {
		if a[i] != b[i] {
			return true
		}
	}
	return false
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

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

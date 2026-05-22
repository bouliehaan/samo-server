package watch

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/scanner"
)

type Options struct {
	DB        *sql.DB
	Catalog   *catalog.Service
	Scanner   *scanner.Scanner
	Libraries []scanner.Library
	Debounce  time.Duration
	Logger    *log.Logger
}

type Watcher struct {
	db        *sql.DB
	catalog   *catalog.Service
	scanner   *scanner.Scanner
	libraries []scanner.Library
	debounce  time.Duration
	logger    *log.Logger
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
		db:        options.DB,
		catalog:   options.Catalog,
		scanner:   options.Scanner,
		libraries: options.Libraries,
		debounce:  debounce,
		logger:    logger,
	}
}

func (w *Watcher) Run(ctx context.Context) error {
	if len(w.libraries) == 0 {
		return nil
	}
	if w.db == nil {
		return errors.New("watcher database is nil")
	}
	if w.catalog == nil {
		return errors.New("watcher catalog is nil")
	}
	if w.scanner == nil {
		w.scanner = scanner.New(w.db)
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsWatcher.Close()

	if err := addLibraryWatches(fsWatcher, w.libraries); err != nil {
		return err
	}
	w.logger.Printf("watching %d configured library path(s)", len(w.libraries))

	trigger := make(chan struct{}, 1)
	done := make(chan struct{})
	go w.scanLoop(ctx, trigger, done)
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
				notify(trigger)
			}
		case err, ok := <-fsWatcher.Errors:
			if !ok {
				return nil
			}
			w.logger.Printf("filesystem watcher error: %v", err)
		}
	}
}

func (w *Watcher) scanLoop(ctx context.Context, trigger <-chan struct{}, done chan<- struct{}) {
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
			w.rescan(ctx)
		}
	}
}

func (w *Watcher) rescan(ctx context.Context) {
	started := time.Now()
	w.logger.Printf("library change detected; scanning")
	if err := w.scanner.Scan(ctx, w.libraries); err != nil {
		w.logger.Printf("watch-triggered scan failed: %v", err)
		return
	}
	seed, err := catalog.LoadSeedFromDB(ctx, w.db)
	if err != nil {
		w.logger.Printf("catalog refresh failed after scan: %v", err)
		return
	}
	w.catalog.Replace(seed)
	w.logger.Printf("catalog refreshed after filesystem changes in %s", time.Since(started).Round(time.Millisecond))
}

func addLibraryWatches(watcher *fsnotify.Watcher, libraries []scanner.Library) error {
	for _, library := range libraries {
		root := strings.TrimSpace(library.Path)
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
	if name == "desc.txt" || name == "description.txt" || name == "summary.txt" || name == "reader.txt" || name == "narrator.txt" || name == "narrators.txt" {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".alac", ".flac", ".m4a", ".m4b", ".mp3", ".ogg", ".opus", ".wav", ".wma",
		".opf", ".jpg", ".jpeg", ".png", ".webp":
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

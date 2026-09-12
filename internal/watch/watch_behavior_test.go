package watch

import (
	"context"
	"database/sql"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/libraries"
)

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) scan(_ context.Context, libraryID string, subpaths []string) (libraries.ScanResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range subpaths {
		r.calls = append(r.calls, "subpath:"+filepath.Base(p))
	}
	return libraries.ScanResult{}, nil
}

func (r *recorder) full(_ context.Context, libraryID string) (libraries.ScanResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "library:"+libraryID)
	return libraries.ScanResult{}, nil
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func harness(t *testing.T, list func(context.Context) ([]LibraryRoot, error)) *recorder {
	t.Helper()
	rec := &recorder{}
	w := New(Options{
		DB:            new(sql.DB),
		ScanSubpaths:  rec.scan,
		ScanLibrary:   rec.full,
		ListLibraries: list,
		Debounce:      200 * time.Millisecond,
		Resync:        300 * time.Millisecond,
		Logger:        log.New(io.Discard, "", 0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(400 * time.Millisecond)
	return rec
}

func fixed(root string) func(context.Context) ([]LibraryRoot, error) {
	return func(context.Context) ([]LibraryRoot, error) {
		return []LibraryRoot{{ID: "lib1", Path: root}}, nil
	}
}

// Drag-and-drop within one filesystem is a rename: one Create for the folder,
// no events for the files inside.
func TestDragDropFolderIntoLibrary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	staging := filepath.Join(base, "downloads", "New Album")
	mkdirs(t, root, staging)
	write(t, filepath.Join(staging, "01 track.flac"))

	rec := harness(t, fixed(root))
	if err := os.Rename(staging, filepath.Join(root, "New Album")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if len(got) == 0 {
		t.Error("dragging a folder in triggered no scan")
	}
}

// A nested drop: Artist/Album/CD1/track.flac moved in as one tree.
func TestDragDropNestedTreeIntoLibrary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	staging := filepath.Join(base, "downloads", "Artist")
	mkdirs(t, root, filepath.Join(staging, "Album", "CD1"))
	write(t, filepath.Join(staging, "Album", "CD1", "01 track.flac"))

	rec := harness(t, fixed(root))
	if err := os.Rename(staging, filepath.Join(root, "Artist")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if len(got) == 0 {
		t.Error("dragging a nested tree in triggered no scan")
	}
}

// Copying file-by-file must keep working.
func TestCopyFolderIntoLibrary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	mkdirs(t, root)

	rec := harness(t, fixed(root))
	album := filepath.Join(root, "Copied Album")
	mkdirs(t, album)
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(album, "01 track.flac"))
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if len(got) == 0 {
		t.Error("copying a folder in triggered no scan")
	}
}

// Deleting must reach a library-scoped scan, since subpath scans never prune.
func TestDeleteTrackReconcilesLibrary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	mkdirs(t, filepath.Join(root, "Album"))
	track := filepath.Join(root, "Album", "01 track.flac")
	write(t, track)

	rec := harness(t, fixed(root))
	if err := os.Remove(track); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if !contains(got, "library:lib1") {
		t.Errorf("deleting a track should reconcile the library, got %v", got)
	}
}

func TestDeleteAlbumFolderReconcilesLibrary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	album := filepath.Join(root, "Album")
	mkdirs(t, album)
	write(t, filepath.Join(album, "01 track.flac"))

	rec := harness(t, fixed(root))
	if err := os.RemoveAll(album); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if !contains(got, "library:lib1") {
		t.Errorf("deleting an album folder should reconcile the library, got %v", got)
	}
}

// Junk churn must not kick off scans.
func TestIrrelevantFilesDoNotScan(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	album := filepath.Join(root, "Album")
	mkdirs(t, album)

	rec := harness(t, fixed(root))
	for _, name := range []string{".DS_Store", "grab.part", "notes.tmp"} {
		write(t, filepath.Join(album, name))
	}
	time.Sleep(300 * time.Millisecond)
	for _, name := range []string{".DS_Store", "grab.part", "notes.tmp"} {
		_ = os.Remove(filepath.Join(album, name))
	}
	time.Sleep(1200 * time.Millisecond)

	if got := rec.got(); len(got) != 0 {
		t.Errorf("junk files should not trigger scans, got %v", got)
	}
}

// Server boots with no libraries; one is added from the UI later.
func TestLibraryAddedAfterBoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	mkdirs(t, root)

	var mu sync.Mutex
	roots := []LibraryRoot{}
	rec := harness(t, func(context.Context) ([]LibraryRoot, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]LibraryRoot(nil), roots...), nil
	})

	mu.Lock()
	roots = append(roots, LibraryRoot{ID: "lib1", Path: root})
	mu.Unlock()
	time.Sleep(1200 * time.Millisecond) // let the watcher notice and attach

	album := filepath.Join(root, "Late Album")
	mkdirs(t, album)
	write(t, filepath.Join(album, "01 track.flac"))
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if len(got) == 0 {
		t.Error("a library added after boot is never watched")
	}
}

// The library folder does not exist at boot (unmounted share) and appears later.
func TestLibraryPathAppearsLater(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "not-mounted-yet")

	rec := harness(t, fixed(root))
	mkdirs(t, root)
	time.Sleep(1200 * time.Millisecond) // watcher should repair coverage

	album := filepath.Join(root, "Album")
	mkdirs(t, album)
	write(t, filepath.Join(album, "01 track.flac"))
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if len(got) == 0 {
		t.Error("a library path that mounts after boot is never watched")
	}
}

// One unreadable library must not stop the others from being watched.
func TestOneBadRootDoesNotKillTheRest(t *testing.T) {
	base := t.TempDir()
	good := filepath.Join(base, "good")
	mkdirs(t, good)
	list := func(context.Context) ([]LibraryRoot, error) {
		return []LibraryRoot{
			{ID: "broken", Path: filepath.Join(base, "does-not-exist")},
			{ID: "good", Path: good},
		}, nil
	}

	rec := harness(t, list)
	album := filepath.Join(good, "Album")
	mkdirs(t, album)
	write(t, filepath.Join(album, "01 track.flac"))
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	if len(got) == 0 {
		t.Error("a broken library root stopped the healthy one from being watched")
	}
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// NAS and OS junk folders must never start a scan.
func TestHiddenFoldersDoNotScan(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	staging := filepath.Join(base, "staging", ".Trash-1000")
	mkdirs(t, root, staging)
	write(t, filepath.Join(staging, "01 track.flac"))

	rec := harness(t, fixed(root))
	if err := os.Rename(staging, filepath.Join(root, ".Trash-1000")); err != nil {
		t.Fatal(err)
	}
	mkdirs(t, filepath.Join(root, ".Spotlight-V100"))
	time.Sleep(1500 * time.Millisecond)

	if got := rec.got(); len(got) != 0 {
		t.Errorf("hidden folders should not trigger scans, got %v", got)
	}
}

// A scan that refuses to start (ErrScanInProgress is the common one: a scan was
// already running when the debounce fired) must not consume the change. Losing
// it here is what leaves a dropped album invisible until someone hits refresh.
func TestFailedScanIsRetried(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	staging := filepath.Join(base, "downloads", "Late Album")
	mkdirs(t, root, staging)
	write(t, filepath.Join(staging, "01 track.flac"))

	var mu sync.Mutex
	attempts := 0
	var succeeded []string

	w := New(Options{
		DB: new(sql.DB),
		ScanSubpaths: func(_ context.Context, _ string, subpaths []string) (libraries.ScanResult, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts == 1 {
				return libraries.ScanResult{}, libraries.ErrScanInProgress
			}
			for _, p := range subpaths {
				succeeded = append(succeeded, filepath.Base(p))
			}
			return libraries.ScanResult{}, nil
		},
		ListLibraries: fixed(root),
		Debounce:      200 * time.Millisecond,
		Resync:        300 * time.Millisecond,
		Logger:        log.New(io.Discard, "", 0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	time.Sleep(400 * time.Millisecond)

	if err := os.Rename(staging, filepath.Join(root, "Late Album")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if attempts < 2 {
		t.Fatalf("scan attempted %d time(s); the refused change was dropped instead of retried", attempts)
	}
	if len(succeeded) == 0 {
		t.Fatal("retry never delivered the pending folder")
	}
	t.Logf("attempts=%d delivered=%v", attempts, succeeded)
}

package libraries

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/scanner"
)

type Service struct {
	db             *sql.DB
	scanner        *scanner.Scanner
	scanMu         sync.Mutex
	activeJobID    string
	bgCtx          context.Context
	onScanComplete func(context.Context, ScanJob)
}

func New(db *sql.DB, scan *scanner.Scanner) *Service {
	if scan == nil {
		scan = scanner.New(db)
	}
	return &Service{db: db, scanner: scan, bgCtx: context.Background()}
}

// SetBackgroundContext attaches a lifecycle context to scan goroutines so they
// die cleanly when the server shuts down instead of holding the DB open after
// the request that started them completed. Defaults to context.Background()
// if not set, which is fine for tests.
func (s *Service) SetBackgroundContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.bgCtx = ctx
}

// OnScanComplete registers a hook that fires after every async scan, both on
// success and failure. main.go uses this to reload the catalog projection
// without the API handler having to await the scan.
func (s *Service) OnScanComplete(cb func(context.Context, ScanJob)) {
	s.onScanComplete = cb
}

func (s *Service) SyncConfigured(ctx context.Context, configured []config.Library) error {
	for _, library := range configured {
		absolute, err := filepath.Abs(strings.TrimSpace(library.Path))
		if err != nil {
			return fmt.Errorf("resolve library path %q: %w", library.Path, err)
		}
		name := strings.TrimSpace(library.Name)
		if name == "" {
			name = filepath.Base(absolute)
		}
		item := Library{
			ID:        scanner.LibraryID(library.Kind, library.MediaType, absolute),
			Name:      name,
			Kind:      library.Kind,
			MediaType: library.MediaType,
			Path:      absolute,
		}
		if err := upsertConfiguredLibrary(ctx, s.db, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, limit, offset int) (Page, error) {
	limit, offset = normalizePage(limit, offset)
	return listLibraries(ctx, s.db, limit, offset)
}

func (s *Service) Get(ctx context.Context, id string) (Library, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Library{}, ErrNotFound
	}
	return getLibrary(ctx, s.db, id)
}

func (s *Service) Create(ctx context.Context, input CreateLibraryInput) (Library, error) {
	item, err := validateCreateInput(input)
	if err != nil {
		return Library{}, err
	}
	if err := insertLibrary(ctx, s.db, item); err != nil {
		return Library{}, err
	}
	return getLibrary(ctx, s.db, item.ID)
}

func (s *Service) Update(ctx context.Context, id string, input UpdateLibraryInput) (Library, error) {
	return updateLibrary(ctx, s.db, strings.TrimSpace(id), input)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return deleteLibrary(ctx, s.db, strings.TrimSpace(id))
}

func (s *Service) ListScanJobs(ctx context.Context, limit, offset int) (ScanJobPage, error) {
	limit, offset = normalizePage(limit, offset)
	return listScanJobs(ctx, s.db, limit, offset)
}

func (s *Service) GetScanJob(ctx context.Context, id string) (ScanJob, error) {
	return getScanJob(ctx, s.db, strings.TrimSpace(id))
}

func (s *Service) ScanAll(ctx context.Context, trigger string) (ScanResult, error) {
	return s.runScan(ctx, ScanScopeAll, "", trigger)
}

func (s *Service) ScanFilesystem(ctx context.Context) (ScanResult, error) {
	return s.ScanAll(ctx, TriggerFilesystem)
}

func (s *Service) ScanLibrary(ctx context.Context, libraryID, trigger string) (ScanResult, error) {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return ScanResult{}, ErrNotFound
	}
	if _, err := getLibrary(ctx, s.db, libraryID); err != nil {
		return ScanResult{}, err
	}
	return s.runScan(ctx, ScanScopeLibrary, libraryID, trigger)
}

func (s *Service) ScannerLibraries(ctx context.Context) ([]scanner.Library, error) {
	page, err := listLibraries(ctx, s.db, 1000, 0)
	if err != nil {
		return nil, err
	}
	libraries := make([]scanner.Library, 0, len(page.Items))
	for _, item := range page.Items {
		if strings.HasPrefix(item.Path, "samo://") {
			continue
		}
		libraries = append(libraries, scanner.Library{
			ID:        item.ID,
			Name:      item.Name,
			Kind:      item.Kind,
			MediaType: item.MediaType,
			Path:      item.Path,
		})
	}
	return libraries, nil
}

// runScan creates a scan job record, spawns the actual scan in a
// background goroutine, and returns the job immediately. The dashboard
// polls /api/v1/scan/jobs/{id} to watch progress (files_seen ticks up as
// the scanner walks files). Repeat calls while a scan is in flight return
// the already-running job rather than queuing or rejecting — clicking
// "Scan All" twice in a row is harmless.
func (s *Service) runScan(ctx context.Context, scope, libraryID, trigger string) (ScanResult, error) {
	if trigger == "" {
		trigger = TriggerAPI
	}

	// Resolve the library list against the *request* context so validation
	// errors surface synchronously (bad library ID, etc.) rather than dying
	// inside the goroutine where the caller can't see them.
	scanLibraries, err := s.resolveScanLibraries(ctx, scope, libraryID)
	if err != nil {
		return ScanResult{}, err
	}

	s.scanMu.Lock()
	if s.activeJobID != "" {
		runningID := s.activeJobID
		s.scanMu.Unlock()
		// A scan is already running. Surface its current state instead of
		// kicking off a duplicate. The caller's poll loop sees the job ID
		// either way.
		job, err := getScanJob(ctx, s.db, runningID)
		if err != nil {
			return ScanResult{}, err
		}
		return ScanResult{Job: job}, nil
	}

	job := ScanJob{
		ID:            newScanJobID(),
		Status:        ScanStatusRunning,
		Scope:         scope,
		LibraryID:     libraryID,
		TriggerSource: trigger,
		StartedAt:     time.Now().UTC(),
	}
	if err := insertScanJob(ctx, s.db, job); err != nil {
		s.scanMu.Unlock()
		return ScanResult{}, err
	}
	s.activeJobID = job.ID
	s.scanMu.Unlock()

	// Detach the goroutine from the request context so the scan finishes
	// even if the HTTP client disconnects right after kicking it off.
	go s.executeScan(s.bgCtx, job, scanLibraries)

	return ScanResult{Job: job}, nil
}

// resolveScanLibraries figures out which library rows the scan will touch.
// Returning here (synchronously) means bad library IDs and DB errors show
// up in the API response, not silently in the goroutine.
func (s *Service) resolveScanLibraries(ctx context.Context, scope, libraryID string) ([]scanner.Library, error) {
	switch scope {
	case ScanScopeLibrary:
		item, err := getLibrary(ctx, s.db, libraryID)
		if err != nil {
			return nil, err
		}
		return []scanner.Library{toScannerLibrary(item)}, nil
	default:
		return s.ScannerLibraries(ctx)
	}
}

// executeScan is the goroutine body. It owns the scan lifecycle: progress
// updates while running, transitioning the job row to a terminal state
// when done, clearing activeJobID, and firing the post-scan hook.
func (s *Service) executeScan(ctx context.Context, job ScanJob, scanLibraries []scanner.Library) {
	defer func() {
		s.scanMu.Lock()
		s.activeJobID = ""
		s.scanMu.Unlock()
		if s.onScanComplete != nil {
			s.onScanComplete(ctx, job)
		}
	}()

	// Pre-walk every library to get the total file count. This lets the
	// dashboard render "1200 of 1500" instead of a number that climbs with
	// no upper bound. Walks are cheap (directory enumeration, no ffprobe);
	// for a 50k-file library it costs a few seconds against several
	// minutes of probing.
	totalFiles := 0
	for _, lib := range scanLibraries {
		if strings.HasPrefix(lib.Path, "samo://") {
			continue
		}
		count, err := scanner.CountAudioFiles(lib.Path)
		if err != nil {
			// Bail out of pre-counting if any library can't be walked,
			// but don't fail the whole scan — the scan itself will
			// surface the real error.
			totalFiles = 0
			break
		}
		totalFiles += count
	}
	job.FilesTotal = totalFiles
	if totalFiles > 0 {
		_, _ = s.db.ExecContext(ctx,
			`UPDATE scan_jobs SET files_total = ? WHERE id = ?`, totalFiles, job.ID)
	}

	// Throttle DB writes to one per 500ms so a 50,000-file scan doesn't
	// hammer SQLite. The final count is written unconditionally when the
	// scan finishes, so the displayed total always settles to the truth.
	var lastReport time.Time
	onFile := func(total int) {
		if time.Since(lastReport) < 500*time.Millisecond {
			return
		}
		lastReport = time.Now()
		_, _ = s.db.ExecContext(ctx,
			`UPDATE scan_jobs SET files_seen = ? WHERE id = ?`, total, job.ID)
	}

	stats, scanErr := s.scanner.ScanWithProgress(ctx, scanLibraries, scanner.ScanOptions{OnFileSeen: onFile})

	job.FilesSeen = stats.FilesSeen
	job.FilesPruned = stats.FilesPruned
	job.ItemsPruned = stats.ItemsPruned
	finished := time.Now().UTC()
	job.FinishedAt = &finished

	if scanErr != nil {
		job.Status = ScanStatusFailed
		job.Error = scanErr.Error()
	} else {
		job.Status = ScanStatusCompleted
	}
	_ = updateScanJob(ctx, s.db, job)
}

func validateCreateInput(input CreateLibraryInput) (Library, error) {
	kind := strings.TrimSpace(input.Kind)
	mediaType := strings.TrimSpace(input.MediaType)
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return Library{}, ErrInvalidLibrary
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Library{}, fmt.Errorf("resolve library path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Library{}, ErrPathNotDirectory
		}
		return Library{}, fmt.Errorf("stat library path: %w", err)
	}
	if !info.IsDir() {
		return Library{}, ErrPathNotDirectory
	}

	// Translate legacy kind/media_type combinations into the new kind
	// enum. Older clients still send kind="shelf" + media_type="book" or
	// media_type="podcast"; map those onto the new explicit kinds so the
	// rest of the pipeline doesn't have to know about shelf at all.
	if kind == "shelf" {
		switch mediaType {
		case MediaTypeBook:
			kind = KindAudiobook
		case MediaTypePodcast:
			kind = KindPodcast
		default:
			return Library{}, ErrInvalidLibrary
		}
	}
	mediaType = ""

	switch kind {
	case KindMusic, KindAudiobook, KindPodcast, KindMixed:
		// valid
	default:
		return Library{}, ErrInvalidLibrary
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = filepath.Base(absolute)
	}

	return Library{
		ID:          scanner.LibraryID(kind, mediaType, absolute),
		Name:        name,
		Kind:        kind,
		MediaType:   mediaType,
		Path:        absolute,
		Description: strings.TrimSpace(input.Description),
	}, nil
}

func toScannerLibrary(item Library) scanner.Library {
	return scanner.Library{
		ID:        item.ID,
		Name:      item.Name,
		Kind:      item.Kind,
		MediaType: item.MediaType,
		Path:      item.Path,
	}
}

func newScanJobID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return "scan_" + hex.EncodeToString(buf[:])
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

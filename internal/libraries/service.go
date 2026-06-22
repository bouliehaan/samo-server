package libraries

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
)

type ScanCompleteCallback func(context.Context, ScanJob, scanner.ScanStats)

type Service struct {
	db             *sql.DB
	scanner        *scanner.Scanner
	scanMu         sync.Mutex
	activeJobID    string
	activeScanPath string
	activeCancel   context.CancelFunc
	bgCtx          context.Context
	onScanComplete ScanCompleteCallback
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
func (s *Service) OnScanComplete(cb ScanCompleteCallback) {
	s.onScanComplete = cb
}

// ScanInProgress reports whether a scan goroutine is currently running.
func (s *Service) ScanInProgress() bool {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	return s.activeJobID != ""
}

// ReconcileOrphanScans closes out scan_jobs rows left in a non-terminal
// state by a previous process. The activeJobID tracking lives only in
// memory, so any row marked running/pending at startup is by definition
// orphaned: a deploy restart killed the goroutine while the scan was in
// flight (install.sh does `systemctl restart`), or a previous process
// crashed. Without this, those rows sit in the dashboard forever showing
// "RUNNING · 0 files" and can't be cancelled because no in-process
// goroutine owns them.
func (s *Service) ReconcileOrphanScans(ctx context.Context) (int, error) {
	finished := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
		UPDATE scan_jobs
		SET status = ?, finished_at = ?, error = ?
		WHERE status IN (?, ?)`,
		ScanStatusFailed, finished, "server restarted before scan completed",
		ScanStatusRunning, ScanStatusPending)
	if err != nil {
		return 0, fmt.Errorf("reconcile orphan scan jobs: %w", err)
	}
	rows, _ := res.RowsAffected()
	return int(rows), nil
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
	page, err := listScanJobs(ctx, s.db, limit, offset)
	if err != nil {
		return ScanJobPage{}, err
	}
	for index := range page.Items {
		s.enrichActiveScanJob(&page.Items[index])
	}
	return page, nil
}

func (s *Service) GetScanJob(ctx context.Context, id string) (ScanJob, error) {
	job, err := getScanJob(ctx, s.db, strings.TrimSpace(id))
	if err != nil {
		return ScanJob{}, err
	}
	s.enrichActiveScanJob(&job)
	return job, nil
}

func (s *Service) enrichActiveScanJob(job *ScanJob) {
	if job == nil || job.Status != ScanStatusRunning {
		return
	}
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if job.ID == s.activeJobID {
		job.CurrentPath = s.activeScanPath
	}
}

// CancelScan stops the scan job identified by jobID. If the job is the
// in-process active scan, the goroutine's context is cancelled and the
// row transitions to status=cancelled once the scanner exits. If the
// row says "running" but no goroutine owns it (server restarted mid-scan
// and the row never got reconciled, or the goroutine panicked), the row
// is marked cancelled directly so the operator can clear stale entries
// without restarting again.
func (s *Service) CancelScan(ctx context.Context, jobID string) (ScanJob, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ScanJob{}, ErrScanJobNotFound
	}

	s.scanMu.Lock()
	activeID := s.activeJobID
	cancel := s.activeCancel
	s.scanMu.Unlock()

	if activeID == jobID {
		if cancel != nil {
			cancel()
		}
		return getScanJob(ctx, s.db, jobID)
	}

	job, err := getScanJob(ctx, s.db, jobID)
	if err != nil {
		return ScanJob{}, err
	}
	switch job.Status {
	case ScanStatusCancelled:
		return job, nil
	case ScanStatusRunning, ScanStatusPending:
		// Orphan row — no goroutine in this process owns it, so the
		// only honest thing to do is mark it cancelled in the DB.
		finished := time.Now().UTC()
		job.Status = ScanStatusCancelled
		job.FinishedAt = &finished
		if job.Error == "" {
			job.Error = "cancelled (orphaned scan job)"
		}
		if err := updateScanJob(ctx, s.db, job); err != nil {
			return ScanJob{}, err
		}
		return job, nil
	default:
		return ScanJob{}, ErrScanNotCancellable
	}
}

// CancelActiveScan stops whichever scan job is currently running, if any.
func (s *Service) CancelActiveScan(ctx context.Context) (ScanJob, error) {
	s.scanMu.Lock()
	jobID := s.activeJobID
	cancel := s.activeCancel
	s.scanMu.Unlock()
	if jobID == "" {
		return ScanJob{}, ErrScanNotCancellable
	}
	if cancel != nil {
		cancel()
	}
	return getScanJob(ctx, s.db, jobID)
}

type scanRequest struct {
	scope     string
	libraryID string
	subpaths  []string
	trigger   string
	mode      string
}

func (s *Service) ScanAll(ctx context.Context, trigger, mode string) (ScanResult, error) {
	return s.runScan(ctx, scanRequest{scope: ScanScopeAll, trigger: trigger, mode: mode})
}

func (s *Service) ScanFilesystem(ctx context.Context, libraryID string, subpaths []string) (ScanResult, error) {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return ScanResult{}, ErrNotFound
	}
	return s.runScan(ctx, scanRequest{
		scope:     ScanScopeSubpaths,
		libraryID: libraryID,
		subpaths:  subpaths,
		trigger:   TriggerFilesystem,
		mode:      ScanModeQuick,
	})
}

func (s *Service) ScanLibrary(ctx context.Context, libraryID, trigger, mode string) (ScanResult, error) {
	return s.ScanLibrarySubpaths(ctx, libraryID, trigger, mode, nil)
}

// ScanLibrarySubpaths scans only files under the given absolute subdirectories.
// Use mode "full" to re-probe and recompute album grouping (required after album-id logic changes).
func (s *Service) ScanLibrarySubpaths(ctx context.Context, libraryID, trigger, mode string, subpaths []string) (ScanResult, error) {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return ScanResult{}, ErrNotFound
	}
	item, err := getLibrary(ctx, s.db, libraryID)
	if err != nil {
		return ScanResult{}, err
	}
	if strings.HasPrefix(strings.TrimSpace(item.Path), "samo://") {
		return ScanResult{}, fmt.Errorf("%w: remote libraries are synced from feeds, not filesystem scans", ErrInvalidLibrary)
	}
	scope := ScanScopeLibrary
	if len(subpaths) > 0 {
		scope = ScanScopeSubpaths
	}
	return s.runScan(ctx, scanRequest{
		scope:     scope,
		libraryID: libraryID,
		subpaths:  subpaths,
		trigger:   trigger,
		mode:      mode,
	})
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
// the scanner walks files). Repeat calls for the *same* scope/library
// while a scan is in flight return the already-running job. A different
// request (e.g. per-library scan while a full-library scan is running)
// returns ErrScanInProgress so the UI does not attach to the wrong job.
func (s *Service) runScan(ctx context.Context, request scanRequest) (ScanResult, error) {
	// Kickoff is fire-and-forget and must outlive the caller's request. The HTTP
	// handlers pass r.Context(), which a browser refresh/navigation cancels — if
	// the library resolve + job insert rode on it, the scan would abort
	// mid-start ("stuck at starting" / "a refresh stops the scan you started").
	// Pin the whole kickoff to the long-lived background context; the worker
	// (executeScan) already runs on a child of s.bgCtx.
	ctx = s.bgCtx
	if request.trigger == "" {
		request.trigger = TriggerAPI
	}

	scanLibraries, err := s.resolveScanLibraries(ctx, request)
	if err != nil {
		return ScanResult{}, err
	}
	if len(scanLibraries) == 0 {
		return ScanResult{}, fmt.Errorf("%w: no filesystem libraries to scan", ErrInvalidLibrary)
	}
	resolvedMode := s.resolveScanMode(ctx, request.trigger, request.mode, scanLibraries)

	s.scanMu.Lock()
	if s.activeJobID != "" {
		runningID := s.activeJobID
		s.scanMu.Unlock()
		job, err := getScanJob(ctx, s.db, runningID)
		if err != nil {
			return ScanResult{}, err
		}
		if scanJobMatchesRequest(job, request) {
			return ScanResult{Job: job}, nil
		}
		return ScanResult{}, ErrScanInProgress
	}

	job := ScanJob{
		ID:            newScanJobID(),
		Status:        ScanStatusRunning,
		Scope:         request.scope,
		LibraryID:     request.libraryID,
		TriggerSource: request.trigger,
		ScanMode:      resolvedMode,
		StartedAt:     time.Now().UTC(),
	}
	if err := insertScanJob(ctx, s.db, job); err != nil {
		s.scanMu.Unlock()
		return ScanResult{}, err
	}
	scanCtx, cancel := context.WithCancel(s.bgCtx)
	s.activeJobID = job.ID
	s.activeScanPath = ""
	s.activeCancel = cancel
	s.scanMu.Unlock()

	go s.executeScan(scanCtx, job, scanLibraries, request.subpaths)

	return ScanResult{Job: job}, nil
}

func (s *Service) resolveScanMode(ctx context.Context, trigger, explicitMode string, scanLibraries []scanner.Library) string {
	switch strings.ToLower(strings.TrimSpace(explicitMode)) {
	case ScanModeFull:
		return ScanModeFull
	case ScanModeQuick:
		return ScanModeQuick
	case ScanModeRepair:
		return ScanModeRepair
	}
	if trigger == TriggerFilesystem {
		for _, library := range scanLibraries {
			item, err := getLibrary(ctx, s.db, library.ID)
			if err != nil {
				continue
			}
			if item.LastScanAt == nil {
				return ScanModeFull
			}
		}
		return ScanModeQuick
	}
	if trigger == TriggerAPI {
		return ScanModeQuick
	}
	for _, library := range scanLibraries {
		item, err := getLibrary(ctx, s.db, library.ID)
		if err != nil {
			continue
		}
		if item.LastScanAt == nil {
			return ScanModeFull
		}
	}
	return ScanModeQuick
}

func (s *Service) resolveScanLibraries(ctx context.Context, request scanRequest) ([]scanner.Library, error) {
	switch request.scope {
	case ScanScopeLibrary, ScanScopeSubpaths:
		item, err := getLibrary(ctx, s.db, request.libraryID)
		if err != nil {
			return nil, err
		}
		return []scanner.Library{toScannerLibrary(item)}, nil
	default:
		return s.ScannerLibraries(ctx)
	}
}

func (s *Service) executeScan(ctx context.Context, job ScanJob, scanLibraries []scanner.Library, subpaths []string) {
	var lastStats scanner.ScanStats
	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("scan panicked: %v", r)
			s.finishScanJob(ctx, &job, lastStats, panicErr)
		}
		s.scanMu.Lock()
		s.activeJobID = ""
		s.activeScanPath = ""
		s.activeCancel = nil
		s.scanMu.Unlock()
		if s.onScanComplete != nil {
			cb := s.onScanComplete
			jobCopy := job
			statsCopy := lastStats
			go cb(ctx, jobCopy, statsCopy)
		}
	}()

	progress := s.newScanProgressReporter(job.ID)
	progress.Start()
	defer progress.Stop()

	stats, scanErr := s.scanner.ScanWithProgress(ctx, scanLibraries, scanner.ScanOptions{
		JobID: job.ID,
		OnFileSeen: func(total int) {
			progress.SetSeen(total)
		},
		OnWalkProgress: func(seen int) {
			progress.SetSeen(seen)
			progress.SetActivity(fmt.Sprintf("enumerating library… (%d files)", seen))
		},
		OnActivity: progress.SetActivity,
		OnFileActive: func(path string) {
			progress.SetActivity("probing " + filepath.Base(path))
		},
		Mode:     job.ScanMode,
		Subpaths: subpaths,
	})
	lastStats = stats
	job.FilesTotal = stats.FilesSeen
	// Force the terminal progress count so the dashboard doesn't remain on the
	// last heartbeat value when scanner callbacks under-report near completion.
	progress.SetSeen(stats.FilesSeen)
	progress.SetActivity("finalizing scan")
	progress.Flush()
	s.finishScanJob(ctx, &job, stats, scanErr)
}

func (s *Service) finishScanJob(ctx context.Context, job *ScanJob, stats scanner.ScanStats, scanErr error) {
	job.FilesSeen = stats.FilesSeen
	job.FilesPruned = stats.FilesPruned
	job.FilesMarked = stats.FilesMarked
	job.ItemsPruned = stats.ItemsPruned
	finished := time.Now().UTC()
	job.FinishedAt = &finished

	if scanErr != nil {
		if errors.Is(scanErr, context.Canceled) {
			job.Status = ScanStatusCancelled
			job.Error = "cancelled"
		} else {
			job.Status = ScanStatusFailed
			job.Error = scanErr.Error()
		}
	} else {
		job.Status = ScanStatusCompleted
	}
	// The scan context is cancelled when the operator hits Cancel; terminal
	// state must still persist or the job row stays "running" forever.
	persistCtx := context.WithoutCancel(ctx)
	persistCtx, cancel := context.WithTimeout(persistCtx, 2*time.Minute)
	defer cancel()
	if err := storage.Retry(persistCtx, 12, func() error {
		return updateScanJob(persistCtx, s.db, *job)
	}); err != nil {
		log.Printf("libraries: persist scan job %q terminal state: %v", job.ID, err)
	}
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
		if kind == KindMixed {
			if inferred := scanner.LibraryKindFromPath(absolute); inferred != "" {
				kind = inferred
			}
		}
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

func scanJobMatchesRequest(job ScanJob, request scanRequest) bool {
	if job.Scope != request.scope {
		return false
	}
	switch request.scope {
	case ScanScopeLibrary, ScanScopeSubpaths:
		return strings.TrimSpace(job.LibraryID) == strings.TrimSpace(request.libraryID)
	default:
		return true
	}
}

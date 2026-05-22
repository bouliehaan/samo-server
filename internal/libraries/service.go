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
	db      *sql.DB
	scanner *scanner.Scanner
	scanMu  sync.Mutex
}

func New(db *sql.DB, scan *scanner.Scanner) *Service {
	if scan == nil {
		scan = scanner.New(db)
	}
	return &Service{db: db, scanner: scan}
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

func (s *Service) runScan(ctx context.Context, scope, libraryID, trigger string) (ScanResult, error) {
	if trigger == "" {
		trigger = TriggerAPI
	}

	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	job := ScanJob{
		ID:            newScanJobID(),
		Status:        ScanStatusRunning,
		Scope:         scope,
		LibraryID:     libraryID,
		TriggerSource: trigger,
		StartedAt:     time.Now().UTC(),
	}
	if err := insertScanJob(ctx, s.db, job); err != nil {
		return ScanResult{}, err
	}

	var (
		scanLibraries []scanner.Library
		err           error
	)
	switch scope {
	case ScanScopeLibrary:
		item, getErr := getLibrary(ctx, s.db, libraryID)
		if getErr != nil {
			return ScanResult{}, getErr
		}
		scanLibraries = []scanner.Library{toScannerLibrary(item)}
	default:
		scanLibraries, err = s.ScannerLibraries(ctx)
		if err != nil {
			return ScanResult{}, err
		}
	}

	stats, scanErr := s.scanner.ScanWithStats(ctx, scanLibraries)
	job.FilesSeen = stats.FilesSeen
	job.FilesPruned = stats.FilesPruned
	job.ItemsPruned = stats.ItemsPruned
	finished := time.Now().UTC()
	job.FinishedAt = &finished

	if scanErr != nil {
		job.Status = ScanStatusFailed
		job.Error = scanErr.Error()
		if updateErr := updateScanJob(ctx, s.db, job); updateErr != nil {
			return ScanResult{}, updateErr
		}
		return ScanResult{Job: job}, scanErr
	}

	job.Status = ScanStatusCompleted
	if err := updateScanJob(ctx, s.db, job); err != nil {
		return ScanResult{}, err
	}
	return ScanResult{Job: job}, nil
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

	switch kind {
	case KindMusic:
		mediaType = ""
	case KindShelf:
		switch mediaType {
		case MediaTypeBook, MediaTypePodcast:
		default:
			return Library{}, ErrInvalidLibrary
		}
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

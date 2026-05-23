package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/users"
)

// SetupStatus describes the current first-run state. NeedsSetup is the only
// field clients strictly need to decide whether to show the wizard; the rest
// power per-step indicators in the UI.
type SetupStatus struct {
	NeedsSetup     bool   `json:"needsSetup"`
	HasAdmin       bool   `json:"hasAdmin"`
	HasLibrary     bool   `json:"hasLibrary"`
	LibraryCount   int    `json:"libraryCount"`
	HasScanned     bool   `json:"hasScanned"`
	CurrentStep    string `json:"currentStep"`
	BootstrapAdmin string `json:"bootstrapAdmin,omitempty"`
}

const (
	setupStepAdmin     = "admin"
	setupStepLibraries = "libraries"
	setupStepScan      = "scan"
	setupStepDone      = "done"
)

func (s *Server) getSetupStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) createSetupAdmin(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status.HasAdmin {
		writeError(w, http.StatusConflict, "setup already complete: admin user exists")
		return
	}
	var input setupAdminInput
	if !readJSONBody(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	input.Password = strings.TrimSpace(input.Password)
	if input.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if len(input.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	service := s.usersService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}

	result, err := service.BootstrapWithResult(r.Context(), users.BootstrapInput{
		AdminUsername: input.Username,
		AdminPassword: input.Password,
	})
	if err != nil {
		writeUserError(w, err)
		return
	}
	if !result.CreatedAdmin {
		writeError(w, http.StatusConflict, "an admin user already exists")
		return
	}
	login, err := service.Login(r.Context(), users.LoginInput{
		Username: input.Username,
		Password: input.Password,
	})
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, login)
}

func (s *Server) browseSetupDirectories(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !status.NeedsSetup {
		if _, ok := s.requireAdmin(w, r); !ok {
			return
		}
	}
	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	entries, err := browseDirectories(requested)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) createSetupLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetupOrAdmin(w, r) {
		return
	}
	var input libraries.CreateLibraryInput
	if !readJSONBody(w, r, &input) {
		return
	}
	library, err := s.libraries.Create(r.Context(), input)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, library)
}

func (s *Server) runSetupScan(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetupOrAdmin(w, r) {
		return
	}
	result, err := s.libraries.ScanAll(r.Context(), libraries.TriggerAPI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) completeSetup(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetupOrAdmin(w, r) {
		return
	}
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !status.HasAdmin {
		writeError(w, http.StatusConflict, "create the admin user before completing setup")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": setupStepDone})
}

func (s *Server) computeSetupStatus(ctx context.Context) (SetupStatus, error) {
	status := SetupStatus{CurrentStep: setupStepAdmin}
	if s.users == nil || !s.users.Enabled() {
		status.NeedsSetup = true
		return status, nil
	}
	all, err := s.users.List(ctx)
	if err != nil {
		return SetupStatus{}, err
	}
	for _, user := range all {
		if user.ID == users.BootstrapUserID {
			continue
		}
		if user.Role == users.RoleAdmin {
			status.HasAdmin = true
			break
		}
	}

	if s.libraries != nil {
		page, err := s.libraries.List(ctx, 100, 0)
		if err != nil {
			return SetupStatus{}, err
		}
		status.LibraryCount = page.Total
		status.HasLibrary = page.Total > 0
	}
	if s.libraries != nil {
		jobs, err := s.libraries.ListScanJobs(ctx, 1, 0)
		if err == nil && jobs.Total > 0 {
			status.HasScanned = true
		}
	}

	switch {
	case !status.HasAdmin:
		status.CurrentStep = setupStepAdmin
		status.NeedsSetup = true
	case !status.HasLibrary:
		status.CurrentStep = setupStepLibraries
		status.NeedsSetup = true
	case !status.HasScanned:
		status.CurrentStep = setupStepScan
		status.NeedsSetup = true
	default:
		status.CurrentStep = setupStepDone
		status.NeedsSetup = false
	}
	return status, nil
}

func (s *Server) allowSetupOrAdmin(w http.ResponseWriter, r *http.Request) bool {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if status.NeedsSetup {
		// During setup the admin token was just minted by createSetupAdmin and
		// is required for every subsequent step. Require it.
		principal, ok := s.authenticateRequest(r)
		if !ok || principal.User.Role != users.RoleAdmin {
			writeError(w, http.StatusUnauthorized, "setup requires the admin token issued during step 1")
			return false
		}
		return true
	}
	_, ok := s.requireAdmin(w, r)
	return ok
}

type setupAdminInput struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName,omitempty"`
}

type setupDirectoryEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	IsRoot    bool   `json:"isRoot,omitempty"`
	IsParent  bool   `json:"isParent,omitempty"`
	ItemCount int    `json:"itemCount,omitempty"`
}

type setupDirectoryListing struct {
	Path    string                `json:"path"`
	Parent  string                `json:"parent,omitempty"`
	Entries []setupDirectoryEntry `json:"entries"`
}

func browseDirectories(requested string) (setupDirectoryListing, error) {
	if requested == "" || requested == "/" {
		return setupDirectoryListing{
			Path:    "",
			Entries: defaultRootEntries(),
		}, nil
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return setupDirectoryListing{}, errors.New("invalid path")
	}
	if !filepath.IsAbs(absolute) {
		return setupDirectoryListing{}, errors.New("path must be absolute")
	}
	if isSystemPath(absolute) {
		return setupDirectoryListing{}, errors.New("system path is not browsable")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return setupDirectoryListing{}, errors.New("path does not exist")
	}
	if !info.IsDir() {
		return setupDirectoryListing{}, errors.New("path is not a directory")
	}
	rawEntries, err := os.ReadDir(absolute)
	if err != nil {
		return setupDirectoryListing{}, err
	}
	listing := setupDirectoryListing{
		Path:   absolute,
		Parent: filepath.Dir(absolute),
	}
	if listing.Parent == absolute {
		listing.Parent = ""
	}
	if listing.Parent != "" {
		listing.Entries = append(listing.Entries, setupDirectoryEntry{
			Name:     "..",
			Path:     listing.Parent,
			IsDir:    true,
			IsParent: true,
		})
	}
	entries := make([]setupDirectoryEntry, 0, len(rawEntries))
	for _, entry := range rawEntries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		child := filepath.Join(absolute, name)
		if !entry.IsDir() {
			continue
		}
		count := 0
		if children, err := os.ReadDir(child); err == nil {
			count = len(children)
		}
		entries = append(entries, setupDirectoryEntry{
			Name:      name,
			Path:      child,
			IsDir:     true,
			ItemCount: count,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	listing.Entries = append(listing.Entries, entries...)
	return listing, nil
}

func defaultRootEntries() []setupDirectoryEntry {
	candidates := []string{
		"/home",
		"/srv",
		"/mnt",
		"/media",
		"/opt",
		"/var/lib",
		"/data",
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		candidates = append([]string{home}, candidates...)
	}
	seen := map[string]struct{}{}
	entries := make([]setupDirectoryEntry, 0, len(candidates))
	for _, candidate := range candidates {
		clean := filepath.Clean(candidate)
		if _, ok := seen[clean]; ok {
			continue
		}
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[clean] = struct{}{}
		entries = append(entries, setupDirectoryEntry{
			Name:   clean,
			Path:   clean,
			IsDir:  true,
			IsRoot: true,
		})
	}
	return entries
}

func isSystemPath(path string) bool {
	for _, prefix := range []string{"/proc", "/sys", "/dev", "/run"} {
		if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

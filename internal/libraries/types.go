package libraries

import "time"

// Library kinds. Music, audiobooks, podcasts, and radio are independent
// product domains in Samo; mixed libraries are a convenience that auto-
// classify content. The legacy `shelf` kind no longer exists — at migration
// time `shelf`+`media_type=book` became `audiobook` and `shelf`+`media_type=podcast`
// became `podcast`.
const (
	KindMusic     = "music"
	KindAudiobook = "audiobook"
	KindPodcast   = "podcast"
	KindMixed     = "mixed"

	// MediaType* are kept for backwards compatibility with create payloads
	// from older clients. They are no longer persisted; the dispatcher
	// translates them into the correct Kind.
	MediaTypeBook    = "book"
	MediaTypePodcast = "podcast"

	ScanStatusPending   = "pending"
	ScanStatusRunning   = "running"
	ScanStatusCompleted = "completed"
	ScanStatusFailed    = "failed"
	ScanStatusCancelled = "cancelled"

	ScanScopeAll      = "all"
	ScanScopeLibrary  = "library"
	ScanScopeSubpaths = "subpaths"

	TriggerAPI        = "api"
	TriggerStartup    = "startup"
	TriggerFilesystem = "filesystem"

	ScanModeFull   = "full"
	ScanModeQuick  = "quick"
	ScanModeRepair = "repair"
)

type Library struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	MediaType   string     `json:"mediaType,omitempty"`
	Path        string     `json:"path"`
	Description string     `json:"description,omitempty"`
	ItemCount   int        `json:"itemCount"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	LastScanAt  *time.Time `json:"lastScanAt,omitempty"`
}

type CreateLibraryInput struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	MediaType   string `json:"mediaType,omitempty"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type UpdateLibraryInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Path        *string `json:"path,omitempty"`
}

type ScanJob struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	Scope         string     `json:"scope"`
	LibraryID     string     `json:"libraryId,omitempty"`
	TriggerSource string     `json:"triggerSource"`
	ScanMode      string     `json:"scanMode"`
	StartedAt     time.Time  `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
	Error         string     `json:"error,omitempty"`
	FilesSeen     int        `json:"filesSeen"`
	FilesTotal    int        `json:"filesTotal"`
	FilesPruned   int        `json:"filesPruned"`
	FilesMarked   int        `json:"filesMarked"`
	ItemsPruned   int        `json:"itemsPruned"`
	// CurrentPath is populated for the in-process active scan only.
	CurrentPath string `json:"currentPath,omitempty"`
}

type ScanResult struct {
	Job ScanJob `json:"job"`
}

type Page struct {
	Items  []Library `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

type ScanJobPage struct {
	Items  []ScanJob `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

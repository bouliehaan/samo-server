package libraries

import "time"

const (
	KindMusic = "music"
	KindShelf = "shelf"
	KindMixed = "mixed"

	MediaTypeBook    = "book"
	MediaTypePodcast = "podcast"

	ScanStatusPending   = "pending"
	ScanStatusRunning   = "running"
	ScanStatusCompleted = "completed"
	ScanStatusFailed    = "failed"

	ScanScopeAll     = "all"
	ScanScopeLibrary = "library"

	TriggerAPI        = "api"
	TriggerStartup    = "startup"
	TriggerFilesystem = "filesystem"
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
	StartedAt     time.Time  `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
	Error         string     `json:"error,omitempty"`
	FilesSeen     int        `json:"filesSeen"`
	FilesPruned   int        `json:"filesPruned"`
	ItemsPruned   int        `json:"itemsPruned"`
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

package bookmarks

import "time"

// Bookmark is one user-saved position inside an audiobook.
type Bookmark struct {
	ID              string     `json:"id"`
	UserID          string     `json:"userId"`
	AudiobookID     string     `json:"audiobookId"`
	Title           string     `json:"title,omitempty"`
	Note            string     `json:"note,omitempty"`
	PositionSeconds int        `json:"positionSeconds"`
	ChapterID       string     `json:"chapterId,omitempty"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

// Collection is a user-curated ordered list of audiobooks. Collections are
// audiobook-only in Samo — podcasts have their own "podcast subscription"
// model in internal/sources.
type Collection struct {
	ID             string     `json:"id"`
	UserID         string     `json:"userId"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Public         bool       `json:"public"`
	AudiobookIDs   []string   `json:"audiobookIds,omitempty"`
	AudiobookCount int        `json:"audiobookCount"`
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
}

// ListeningSession is one "I pressed play, then later I stopped" event,
// stored for scrobble + per-user listening analytics.
type ListeningSession struct {
	ID                   string     `json:"id"`
	UserID               string     `json:"userId"`
	AudiobookID          string     `json:"audiobookId"`
	StartedAt            *time.Time `json:"startedAt,omitempty"`
	EndedAt              *time.Time `json:"endedAt,omitempty"`
	StartPositionSeconds int        `json:"startPositionSeconds"`
	EndPositionSeconds   int        `json:"endPositionSeconds"`
	DurationSeconds      int        `json:"durationSeconds"`
	Completed            bool       `json:"completed"`
}

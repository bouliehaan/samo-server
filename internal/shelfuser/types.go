package shelfuser

import "time"

type Bookmark struct {
	ID              string     `json:"id"`
	UserID          string     `json:"userId"`
	ItemID          string     `json:"itemId"`
	Title           string     `json:"title,omitempty"`
	Note            string     `json:"note,omitempty"`
	PositionSeconds int        `json:"positionSeconds"`
	ChapterID       string     `json:"chapterId,omitempty"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

type Collection struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Public      bool       `json:"public"`
	ItemIDs     []string   `json:"itemIds,omitempty"`
	ItemCount   int        `json:"itemCount"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
}

type ListeningSession struct {
	ID                   string     `json:"id"`
	UserID               string     `json:"userId"`
	ItemID               string     `json:"itemId"`
	StartedAt            *time.Time `json:"startedAt,omitempty"`
	EndedAt              *time.Time `json:"endedAt,omitempty"`
	StartPositionSeconds int        `json:"startPositionSeconds"`
	EndPositionSeconds   int        `json:"endPositionSeconds"`
	DurationSeconds      int        `json:"durationSeconds"`
	Completed            bool       `json:"completed"`
}

package playback

import (
	"context"
	"database/sql"
	"strings"
)

type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Get(ctx context.Context, userID string, kind TargetKind, id string) (State, error) {
	if s == nil || s.db == nil {
		return State{}, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	id = strings.TrimSpace(id)
	if userID == "" || id == "" {
		return State{}, ErrNotFound
	}
	return loadState(ctx, s.db, userID, kind, id)
}

func (s *Service) Put(ctx context.Context, userID string, kind TargetKind, id string, state State) (State, error) {
	if s == nil || s.db == nil {
		return State{}, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	id = strings.TrimSpace(id)
	if userID == "" || id == "" {
		return State{}, ErrNotFound
	}
	return saveState(ctx, s.db, userID, kind, id, state)
}

func (s *Service) Patch(ctx context.Context, userID string, kind TargetKind, id string, patch PatchInput) (State, error) {
	if s == nil || s.db == nil {
		return State{}, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	id = strings.TrimSpace(id)
	if userID == "" || id == "" {
		return State{}, ErrNotFound
	}
	current, err := loadState(ctx, s.db, userID, kind, id)
	if err != nil {
		return State{}, err
	}
	updated := applyPatch(current, patch)
	updated.UserID = userID
	return saveState(ctx, s.db, userID, kind, id, updated)
}

func ParseTargetKind(raw string) (TargetKind, error) {
	kind := TargetKind(strings.TrimSpace(raw))
	if _, err := tableFor(kind); err != nil {
		return "", ErrInvalidTarget
	}
	return kind, nil
}

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

func (s *Service) Get(ctx context.Context, kind TargetKind, id string) (State, error) {
	if s == nil || s.db == nil {
		return State{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return State{}, ErrNotFound
	}
	return loadState(ctx, s.db, kind, id)
}

func (s *Service) Put(ctx context.Context, kind TargetKind, id string, state State) (State, error) {
	if s == nil || s.db == nil {
		return State{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return State{}, ErrNotFound
	}
	if _, err := loadState(ctx, s.db, kind, id); err != nil {
		return State{}, err
	}
	return saveState(ctx, s.db, kind, id, state)
}

func (s *Service) Patch(ctx context.Context, kind TargetKind, id string, patch PatchInput) (State, error) {
	if s == nil || s.db == nil {
		return State{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return State{}, ErrNotFound
	}
	current, err := loadState(ctx, s.db, kind, id)
	if err != nil {
		return State{}, err
	}
	updated := applyPatch(current, patch)
	return saveState(ctx, s.db, kind, id, updated)
}

func ParseTargetKind(raw string) (TargetKind, error) {
	kind := TargetKind(strings.TrimSpace(raw))
	if _, err := specFor(kind); err != nil {
		return "", ErrInvalidTarget
	}
	return kind, nil
}

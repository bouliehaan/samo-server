package radio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var ErrStationNotFound = errors.New("station not found")

type Service struct {
	mu       sync.RWMutex
	db       *sql.DB
	stations map[string]*station
	order    []string
}

func NewService(cfg Config) (*Service, error) {
	service := &Service{
		stations: map[string]*station{},
		order:    make([]string, 0, len(cfg.Stations)),
	}
	if err := service.applyConfig(cfg); err != nil {
		return nil, err
	}
	return service, nil
}

// NewServiceFromDB hydrates stations from SQLite. If the radio_stations table
// is empty and `cfg` provides stations, the JSON config is imported into the
// DB on first call. After that the DB is the source of truth.
func NewServiceFromDB(ctx context.Context, db *sql.DB, cfg Config) (*Service, error) {
	service := &Service{
		db:       db,
		stations: map[string]*station{},
	}
	if err := service.Reload(ctx, cfg); err != nil {
		return nil, err
	}
	return service, nil
}

// Reload rebuilds the in-memory schedule from DB-backed station records. The
// provided config is used only for first-run import; later reloads should
// pass a zero-value Config.
func (s *Service) Reload(ctx context.Context, cfg Config) error {
	if s == nil {
		return errors.New("nil radio service")
	}
	if s.db != nil {
		if err := ImportConfigIfEmpty(ctx, s.db, cfg); err != nil {
			return err
		}
		records, err := LoadStationsFromDB(ctx, s.db)
		if err != nil {
			return err
		}
		stations := make([]StationConfig, 0, len(records))
		for _, record := range records {
			if !record.Enabled {
				continue
			}
			stations = append(stations, StationToConfig(record))
		}
		return s.applyConfig(Config{Stations: stations})
	}
	return s.applyConfig(cfg)
}

// CreateStation persists a new station and rebuilds the schedule.
func (s *Service) CreateStation(ctx context.Context, input CreateStationInput) (StationRecord, error) {
	if s == nil || s.db == nil {
		return StationRecord{}, errors.New("radio service has no database")
	}
	record, err := CreateStation(ctx, s.db, input)
	if err != nil {
		return StationRecord{}, err
	}
	if err := s.Reload(ctx, Config{}); err != nil {
		return StationRecord{}, err
	}
	return record, nil
}

// UpdateStation patches a station and reloads.
func (s *Service) UpdateStation(ctx context.Context, id string, input UpdateStationInput) (StationRecord, error) {
	if s == nil || s.db == nil {
		return StationRecord{}, errors.New("radio service has no database")
	}
	record, err := UpdateStation(ctx, s.db, id, input)
	if err != nil {
		return StationRecord{}, err
	}
	if err := s.Reload(ctx, Config{}); err != nil {
		return StationRecord{}, err
	}
	return record, nil
}

// DeleteStation removes a station and reloads.
func (s *Service) DeleteStation(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("radio service has no database")
	}
	if err := DeleteStation(ctx, s.db, id); err != nil {
		return err
	}
	return s.Reload(ctx, Config{})
}

// AddStationItem appends an item and reloads.
func (s *Service) AddStationItem(ctx context.Context, stationID string, input CreateStationItemInput) (StationItem, error) {
	if s == nil || s.db == nil {
		return StationItem{}, errors.New("radio service has no database")
	}
	item, err := AddStationItem(ctx, s.db, stationID, input)
	if err != nil {
		return StationItem{}, err
	}
	if err := s.Reload(ctx, Config{}); err != nil {
		return StationItem{}, err
	}
	return item, nil
}

// RemoveStationItem deletes an item and reloads.
func (s *Service) RemoveStationItem(ctx context.Context, itemID string) error {
	if s == nil || s.db == nil {
		return errors.New("radio service has no database")
	}
	if err := RemoveStationItem(ctx, s.db, itemID); err != nil {
		return err
	}
	return s.Reload(ctx, Config{})
}

// ListStationRecords returns all stored stations (including disabled) with
// resolved items, for admin UI use.
func (s *Service) ListStationRecords(ctx context.Context) ([]StationRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return LoadStationsFromDB(ctx, s.db)
}

// GetStationRecord returns the stored record for a single station.
func (s *Service) GetStationRecord(ctx context.Context, id string) (StationRecord, error) {
	if s == nil || s.db == nil {
		return StationRecord{}, ErrStationNotFound
	}
	return LoadStationByID(ctx, s.db, id)
}

func (s *Service) applyConfig(cfg Config) error {
	stations := map[string]*station{}
	order := make([]string, 0, len(cfg.Stations))
	for _, stationConfig := range cfg.Stations {
		if len(stationConfig.Media) == 0 {
			continue
		}
		normalized, err := normalizeStation(stationConfig)
		if err != nil {
			return err
		}
		if _, exists := stations[normalized.summary.ID]; exists {
			return fmt.Errorf("station id %q is duplicated", normalized.summary.ID)
		}
		stations[normalized.summary.ID] = normalized
		order = append(order, normalized.summary.ID)
	}
	sort.Strings(order)

	s.mu.Lock()
	s.stations = stations
	s.order = order
	s.mu.Unlock()
	return nil
}

func (s *Service) StationCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.stations)
}

func (s *Service) ListStations() []StationSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summaries := make([]StationSummary, 0, len(s.order))
	for _, id := range s.order {
		summaries = append(summaries, s.stations[id].summary)
	}
	return summaries
}

func (s *Service) Station(id string) (StationSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	station, ok := s.stations[normalizeID(id)]
	if !ok {
		return StationSummary{}, false
	}
	return station.summary, true
}

func (s *Service) ContentType(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	station, ok := s.stations[normalizeID(id)]
	if !ok {
		return "", false
	}
	return station.summary.ContentType, true
}

func (s *Service) CurrentSlot(stationID string, at time.Time) (ProgramSlot, error) {
	s.mu.RLock()
	station, ok := s.stations[normalizeID(stationID)]
	s.mu.RUnlock()
	if !ok {
		return ProgramSlot{}, ErrStationNotFound
	}
	slot, _, err := station.slotAt(at)
	return slot, err
}

func (s *Service) Upcoming(stationID string, from time.Time, limit int) ([]ProgramSlot, error) {
	s.mu.RLock()
	station, ok := s.stations[normalizeID(stationID)]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrStationNotFound
	}
	if limit <= 0 {
		limit = 24
	}
	if limit > 200 {
		limit = 200
	}

	slots := make([]ProgramSlot, 0, limit)
	cursor := from.UTC()
	for len(slots) < limit {
		slot, _, err := station.slotAt(cursor)
		if err != nil {
			return nil, err
		}
		slots = append(slots, slot)
		cursor = slot.EndsAt
	}

	return slots, nil
}

func (s *station) slotAt(at time.Time) (ProgramSlot, mediaItem, error) {
	if len(s.loop) == 0 || s.summary.TotalDurationSeconds <= 0 {
		return ProgramSlot{}, mediaItem{}, errors.New("station has no playable media")
	}

	at = at.UTC()
	elapsedSeconds := int(at.Sub(s.epoch).Seconds())
	cycleIndex := floorDiv(elapsedSeconds, s.summary.TotalDurationSeconds)
	cycleStart := s.epoch.Add(time.Duration(cycleIndex*s.summary.TotalDurationSeconds) * time.Second)
	position := elapsedSeconds - cycleIndex*s.summary.TotalDurationSeconds

	cursor := 0
	for _, item := range s.loop {
		nextCursor := cursor + item.durationSeconds
		if position < nextCursor {
			startsAt := cycleStart.Add(time.Duration(cursor) * time.Second)
			endsAt := startsAt.Add(time.Duration(item.durationSeconds) * time.Second)
			return ProgramSlot{
				StationID:       s.summary.ID,
				MediaID:         item.id,
				Title:           item.title,
				Artist:          item.artist,
				Album:           item.album,
				Kind:            item.kind,
				StartsAt:        startsAt,
				EndsAt:          endsAt,
				DurationSeconds: item.durationSeconds,
				OffsetSeconds:   position - cursor,
			}, item, nil
		}
		cursor = nextCursor
	}

	return ProgramSlot{}, mediaItem{}, errors.New("failed to resolve schedule slot")
}

func floorDiv(a int, b int) int {
	quotient := a / b
	remainder := a % b
	if remainder != 0 && ((remainder < 0) != (b < 0)) {
		quotient--
	}
	return quotient
}

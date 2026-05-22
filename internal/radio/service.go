package radio

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var ErrStationNotFound = errors.New("station not found")

type Service struct {
	stations map[string]*station
	order    []string
}

func NewService(cfg Config) (*Service, error) {
	service := &Service{
		stations: map[string]*station{},
		order:    make([]string, 0, len(cfg.Stations)),
	}

	for _, stationConfig := range cfg.Stations {
		normalized, err := normalizeStation(stationConfig)
		if err != nil {
			return nil, err
		}
		if _, exists := service.stations[normalized.summary.ID]; exists {
			return nil, fmt.Errorf("station id %q is duplicated", normalized.summary.ID)
		}

		service.stations[normalized.summary.ID] = normalized
		service.order = append(service.order, normalized.summary.ID)
	}

	sort.Strings(service.order)

	return service, nil
}

func (s *Service) StationCount() int {
	if s == nil {
		return 0
	}
	return len(s.stations)
}

func (s *Service) ListStations() []StationSummary {
	summaries := make([]StationSummary, 0, len(s.order))
	for _, id := range s.order {
		summaries = append(summaries, s.stations[id].summary)
	}
	return summaries
}

func (s *Service) Station(id string) (StationSummary, bool) {
	station, ok := s.stations[normalizeID(id)]
	if !ok {
		return StationSummary{}, false
	}
	return station.summary, true
}

func (s *Service) ContentType(id string) (string, bool) {
	station, ok := s.stations[normalizeID(id)]
	if !ok {
		return "", false
	}
	return station.summary.ContentType, true
}

func (s *Service) CurrentSlot(stationID string, at time.Time) (ProgramSlot, error) {
	station, ok := s.stations[normalizeID(stationID)]
	if !ok {
		return ProgramSlot{}, ErrStationNotFound
	}
	slot, _, err := station.slotAt(at)
	return slot, err
}

func (s *Service) Upcoming(stationID string, from time.Time, limit int) ([]ProgramSlot, error) {
	station, ok := s.stations[normalizeID(stationID)]
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

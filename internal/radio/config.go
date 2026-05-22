package radio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jakedebus/samo-server/internal/media"
)

const (
	defaultContentType = "audio/mpeg"
	defaultEpoch       = "1970-01-01T00:00:00Z"
)

func LoadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read radio config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse radio config: %w", err)
	}

	return cfg, nil
}

func normalizeStation(input StationConfig) (*station, error) {
	id := normalizeID(input.ID)
	if id == "" {
		return nil, errors.New("station id is required")
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = id
	}

	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = defaultContentType
	}

	epoch, err := parseEpoch(input.Epoch)
	if err != nil {
		return nil, fmt.Errorf("station %q epoch: %w", id, err)
	}

	media := make([]mediaItem, 0, len(input.Media))
	loop := make([]mediaItem, 0, len(input.Media))
	seenMediaIDs := map[string]struct{}{}
	totalDurationSeconds := 0

	for index, item := range input.Media {
		normalized, err := normalizeMediaItem(item, index)
		if err != nil {
			return nil, fmt.Errorf("station %q media[%d]: %w", id, index, err)
		}
		if _, exists := seenMediaIDs[normalized.id]; exists {
			return nil, fmt.Errorf("station %q media id %q is duplicated", id, normalized.id)
		}
		seenMediaIDs[normalized.id] = struct{}{}

		media = append(media, normalized)
		for range normalized.weight {
			loop = append(loop, normalized)
			totalDurationSeconds += normalized.durationSeconds
		}
	}

	if len(input.Media) > 0 && len(loop) == 0 {
		return nil, fmt.Errorf("station %q has no playable media", id)
	}

	return &station{
		summary: StationSummary{
			ID:                   id,
			Name:                 name,
			Description:          strings.TrimSpace(input.Description),
			ContentType:          contentType,
			MediaCount:           len(media),
			RotationCount:        len(loop),
			TotalDurationSeconds: totalDurationSeconds,
		},
		epoch: epoch,
		media: media,
		loop:  loop,
	}, nil
}

func normalizeMediaItem(input MediaItemConfig, index int) (mediaItem, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return mediaItem{}, errors.New("path is required")
	}
	if input.DurationSeconds <= 0 {
		return mediaItem{}, errors.New("durationSeconds must be greater than zero")
	}

	id := normalizeID(input.ID)
	if id == "" {
		id = normalizeID(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if id == "" {
		id = fmt.Sprintf("media-%d", index+1)
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	kind := input.Kind
	if kind == "" {
		kind = media.KindOther
	}

	weight := input.Weight
	if weight == 0 {
		weight = 1
	}
	if weight < 0 {
		return mediaItem{}, errors.New("weight cannot be negative")
	}

	return mediaItem{
		id:              id,
		title:           title,
		artist:          strings.TrimSpace(input.Artist),
		album:           strings.TrimSpace(input.Album),
		kind:            kind,
		path:            path,
		durationSeconds: input.DurationSeconds,
		weight:          weight,
	}, nil
}

func parseEpoch(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultEpoch
	}

	epoch, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}

	return epoch.UTC(), nil
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(builder.String(), "-")
}

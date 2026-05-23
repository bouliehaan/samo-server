package sources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrInvalidProbeInterval is returned when a caller submits a probe interval
// outside the supported range.
var ErrInvalidProbeInterval = errors.New("probe interval must be between 60 seconds and 24 hours")

// UpdateInternetRadioStation patches mutable station metadata and probe
// settings. Stream URL changes are intentionally not supported — they would
// reshape the deterministic station ID.
func (s *Service) UpdateInternetRadioStation(ctx context.Context, id string, input UpdateInternetRadioStationInput) (InternetRadioStation, error) {
	if s == nil || s.db == nil {
		return InternetRadioStation{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	current, err := s.GetInternetRadioStation(ctx, id)
	if err != nil {
		return InternetRadioStation{}, err
	}

	name := current.Name
	if input.Name != nil {
		candidate := strings.TrimSpace(*input.Name)
		if candidate == "" {
			return InternetRadioStation{}, fmt.Errorf("name cannot be empty")
		}
		name = candidate
	}
	description := current.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	homepageURL := current.HomepageURL
	if input.HomepageURL != nil {
		homepageURL, err = normalizeOptionalHTTPURL(*input.HomepageURL)
		if err != nil {
			return InternetRadioStation{}, err
		}
	}
	imageURL := current.ImageURL
	if input.ImageURL != nil {
		imageURL, err = normalizeOptionalHTTPURL(*input.ImageURL)
		if err != nil {
			return InternetRadioStation{}, err
		}
	}
	country := current.Country
	if input.Country != nil {
		country = strings.TrimSpace(*input.Country)
	}
	language := current.Language
	if input.Language != nil {
		language = strings.TrimSpace(*input.Language)
	}
	tags := current.Tags
	if input.Tags != nil {
		tags = cleanStringSlice(input.Tags)
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	probeEnabled := current.Probe.Enabled
	if input.ProbeEnabled != nil {
		probeEnabled = *input.ProbeEnabled
	}
	interval := current.Probe.IntervalSeconds
	if interval <= 0 {
		interval = DefaultProbeIntervalSeconds
	}
	if input.ProbeIntervalSeconds != nil {
		interval, err = normalizeProbeIntervalSeconds(*input.ProbeIntervalSeconds)
		if err != nil {
			return InternetRadioStation{}, err
		}
	}
	nextProbeAt := timeStringOrNull(time.Now().UTC())
	if !probeEnabled {
		nextProbeAt = nil
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE internet_radio_stations
		SET name = ?,
		    description = ?,
		    homepage_url = ?,
		    image_url = ?,
		    country = ?,
		    language = ?,
		    tags_json = ?,
		    enabled = ?,
		    probe_enabled = ?,
		    probe_interval_seconds = ?,
		    next_probe_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		name, description, homepageURL, imageURL, country, language,
		jsonText(tags), boolInt(enabled), boolInt(probeEnabled), interval, nextProbeAt, id)
	if err != nil {
		return InternetRadioStation{}, fmt.Errorf("update internet radio station: %w", err)
	}
	return s.GetInternetRadioStation(ctx, id)
}

// ProbeInternetRadioStation issues a probe for a single station and persists
// the resulting metadata. The probe respects the station's enabled flag and
// always records its outcome (success or failure) into probe scheduling
// columns.
func (s *Service) ProbeInternetRadioStation(ctx context.Context, id string) (InternetRadioStation, error) {
	if s == nil || s.db == nil {
		return InternetRadioStation{}, ErrDisabled
	}
	station, err := s.GetInternetRadioStation(ctx, id)
	if err != nil {
		return InternetRadioStation{}, err
	}

	if err := s.markProbeStarted(ctx, station.ID); err != nil {
		return InternetRadioStation{}, err
	}

	probe, probeErr := ProbeIcyStream(ctx, s.client, station.StreamURL)
	if probeErr != nil {
		_ = s.markProbeFailure(ctx, station.ID, probeErr)
		return InternetRadioStation{}, probeErr
	}

	if err := s.applyProbeResult(ctx, station, probe); err != nil {
		_ = s.markProbeFailure(ctx, station.ID, err)
		return InternetRadioStation{}, err
	}
	interval := station.Probe.IntervalSeconds
	if interval <= 0 {
		interval = DefaultProbeIntervalSeconds
	}
	if err := s.markProbeSuccess(ctx, station.ID, interval); err != nil {
		return InternetRadioStation{}, err
	}
	return s.GetInternetRadioStation(ctx, station.ID)
}

// ListDueInternetRadioStations returns enabled probe-eligible stations whose
// next_probe_at is at or before the given moment.
func (s *Service) ListDueInternetRadioStations(ctx context.Context, now time.Time, limit int) ([]InternetRadioStation, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, internetRadioStationSelectSQL+`
		WHERE probe_enabled = 1
		  AND enabled = 1
		  AND (next_probe_at IS NULL OR next_probe_at <= ?)
		ORDER BY COALESCE(next_probe_at, '1970-01-01T00:00:00Z'), name COLLATE NOCASE
		LIMIT ?`, now.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("list due internet radio stations: %w", err)
	}
	defer rows.Close()

	var stations []InternetRadioStation
	for rows.Next() {
		station, err := scanInternetRadioStation(rows)
		if err != nil {
			return nil, err
		}
		stations = append(stations, station)
	}
	return stations, rows.Err()
}

// RunInternetRadioProbeCycle probes every due station once. Failures do not
// stop the cycle; per-station outcomes are returned to the caller for
// reporting.
func (s *Service) RunInternetRadioProbeCycle(ctx context.Context, now time.Time) (ProbeCycleResult, error) {
	if s == nil || s.db == nil {
		return ProbeCycleResult{}, ErrDisabled
	}
	stations, err := s.ListDueInternetRadioStations(ctx, now, 100)
	if err != nil {
		return ProbeCycleResult{}, err
	}
	result := ProbeCycleResult{Results: make([]ProbeStationResult, 0, len(stations))}
	for _, station := range stations {
		result.Checked++
		item := ProbeStationResult{StationID: station.ID, Name: station.Name}
		updated, probeErr := s.ProbeInternetRadioStation(ctx, station.ID)
		if probeErr != nil {
			item.Status = ProbeStatusError
			item.Error = probeErr.Error()
			result.Failed++
		} else {
			item.Status = ProbeStatusOK
			if updated.NowPlaying != nil {
				item.NowPlaying = updated.NowPlaying.Raw
			}
			result.Updated++
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

func (s *Service) applyProbeResult(ctx context.Context, station InternetRadioStation, probe IcyProbeResult) error {
	contentType := station.ContentType
	if contentType == "" {
		contentType = probe.ContentType
	}
	codec := station.Codec
	if codec == "" {
		codec = probe.Codec
	}
	bitrate := station.Bitrate
	if bitrate == 0 && probe.Bitrate > 0 {
		bitrate = probe.Bitrate
	}
	description := station.Description
	if description == "" {
		description = probe.Description
	}
	homepage := station.HomepageURL
	if homepage == "" && probe.HomepageURL != "" {
		if normalized, err := normalizeOptionalHTTPURL(probe.HomepageURL); err == nil {
			homepage = normalized
		}
	}
	tags := station.Tags
	if len(tags) == 0 && len(probe.Tags) > 0 {
		tags = cleanStringSlice(probe.Tags)
	}
	nowPlayingRaw := strings.TrimSpace(probe.NowPlaying)
	nowPlayingArtist := strings.TrimSpace(probe.Artist)
	nowPlayingTitle := strings.TrimSpace(probe.Title)
	var nowPlayingAt any
	if nowPlayingRaw != "" || nowPlayingTitle != "" || nowPlayingArtist != "" {
		nowPlayingAt = time.Now().UTC().Format(time.RFC3339)
	} else if station.NowPlaying != nil && station.NowPlaying.UpdatedAt != nil {
		nowPlayingRaw = station.NowPlaying.Raw
		nowPlayingArtist = station.NowPlaying.Artist
		nowPlayingTitle = station.NowPlaying.Title
		nowPlayingAt = station.NowPlaying.UpdatedAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE internet_radio_stations
		SET content_type = ?,
		    codec = ?,
		    bitrate = ?,
		    description = ?,
		    homepage_url = ?,
		    tags_json = ?,
		    now_playing = ?,
		    now_playing_artist = ?,
		    now_playing_title = ?,
		    now_playing_updated_at = ?,
		    last_checked_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		contentType, codec, bitrate, description, homepage, jsonText(tags),
		nowPlayingRaw, nowPlayingArtist, nowPlayingTitle, nowPlayingAt, station.ID)
	if err != nil {
		return fmt.Errorf("apply probe result: %w", err)
	}
	return nil
}

func (s *Service) markProbeStarted(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE internet_radio_stations
		SET last_probe_started_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, strings.TrimSpace(id))
	return err
}

func (s *Service) markProbeSuccess(ctx context.Context, id string, intervalSeconds int) error {
	next := time.Now().UTC().Add(time.Duration(intervalSeconds) * time.Second)
	_, err := s.db.ExecContext(ctx, `
		UPDATE internet_radio_stations
		SET probe_status = ?,
		    last_probe_error = '',
		    consecutive_probe_errors = 0,
		    last_probe_finished_at = CURRENT_TIMESTAMP,
		    next_probe_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		ProbeStatusOK, next.Format(time.RFC3339), strings.TrimSpace(id))
	return err
}

func (s *Service) markProbeFailure(ctx context.Context, id string, cause error) error {
	station, err := s.GetInternetRadioStation(ctx, id)
	if err != nil {
		return err
	}
	errorsCount := station.Probe.ConsecutiveErrors + 1
	interval := station.Probe.IntervalSeconds
	if interval <= 0 {
		interval = DefaultProbeIntervalSeconds
	}
	backoff := probeBackoffSeconds(interval, errorsCount)
	next := time.Now().UTC().Add(time.Duration(backoff) * time.Second)
	message := ""
	if cause != nil {
		message = strings.TrimSpace(cause.Error())
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE internet_radio_stations
		SET probe_status = ?,
		    last_probe_error = ?,
		    consecutive_probe_errors = ?,
		    last_probe_finished_at = CURRENT_TIMESTAMP,
		    next_probe_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		ProbeStatusError, message, errorsCount,
		next.Format(time.RFC3339), strings.TrimSpace(id))
	return err
}

func probeBackoffSeconds(intervalSeconds, consecutiveErrors int) int {
	if consecutiveErrors <= 0 {
		consecutiveErrors = 1
	}
	backoff := MinProbeIntervalSeconds * consecutiveErrors
	if backoff > intervalSeconds {
		backoff = intervalSeconds
	}
	if backoff > 6*3600 {
		backoff = 6 * 3600
	}
	return backoff
}

func normalizeProbeIntervalSeconds(seconds int) (int, error) {
	if seconds < MinProbeIntervalSeconds || seconds > MaxProbeIntervalSeconds {
		return 0, ErrInvalidProbeInterval
	}
	return seconds, nil
}

// ProbePoller drives RunInternetRadioProbeCycle on a ticker, similar to the
// podcast feed poller.
type ProbePoller struct {
	sources *Service
	tick    time.Duration
	logger  func(string, ...any)
	mu      sync.Mutex
}

type ProbePollerOptions struct {
	Sources *Service
	Tick    time.Duration
	Logger  func(string, ...any)
}

func NewProbePoller(options ProbePollerOptions) *ProbePoller {
	tick := options.Tick
	if tick <= 0 {
		tick = time.Minute
	}
	logger := options.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &ProbePoller{
		sources: options.Sources,
		tick:    tick,
		logger:  logger,
	}
}

func (p *ProbePoller) Run(ctx context.Context) error {
	if p == nil || p.sources == nil {
		return ErrDisabled
	}
	ticker := time.NewTicker(p.tick)
	defer ticker.Stop()

	p.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.runOnce(ctx)
		}
	}
}

func (p *ProbePoller) runOnce(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result, err := p.sources.RunInternetRadioProbeCycle(ctx, time.Now().UTC())
	if err != nil {
		p.logger("internet radio probe cycle failed: %v", err)
		return
	}
	if result.Updated == 0 && result.Failed == 0 {
		return
	}
	p.logger("internet radio probe cycle: checked=%d updated=%d failed=%d",
		result.Checked, result.Updated, result.Failed)
}

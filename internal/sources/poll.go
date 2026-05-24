package sources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidPollInterval = errors.New("poll interval must be between 15 minutes and 7 days")
	ErrPollDisabled        = errors.New("podcast feed polling is disabled for this feed")
)

func normalizePollIntervalSeconds(seconds int) (int, error) {
	if seconds < MinPollIntervalSeconds || seconds > MaxPollIntervalSeconds {
		return 0, ErrInvalidPollInterval
	}
	return seconds, nil
}

func (s *Service) UpdatePodcastFeed(ctx context.Context, id string, input UpdatePodcastFeedInput) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	current, err := s.GetPodcastFeed(ctx, id)
	if err != nil {
		return PodcastFeed{}, err
	}

	title := current.Title
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
		if title == "" {
			return PodcastFeed{}, fmt.Errorf("title cannot be empty")
		}
	}

	pollEnabled := current.Poll.Enabled
	interval := current.Poll.IntervalSeconds
	if input.PollEnabled != nil {
		pollEnabled = *input.PollEnabled
	}
	if input.PollIntervalSeconds != nil {
		interval, err = normalizePollIntervalSeconds(*input.PollIntervalSeconds)
		if err != nil {
			return PodcastFeed{}, err
		}
	}

	var nextPollAt any
	switch {
	case !pollEnabled:
		nextPollAt = nil
	case input.PollEnabled != nil || input.PollIntervalSeconds != nil:
		nextPollAt = timeStringOrNull(time.Now().UTC())
	default:
		nextPollAt = timeString(current.Poll.NextPollAt)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET title = ?,
		    poll_enabled = ?,
		    poll_interval_seconds = ?,
		    next_poll_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		title,
		boolInt(pollEnabled),
		interval,
		nextPollAt,
		id,
	)
	if err != nil {
		return PodcastFeed{}, fmt.Errorf("update podcast feed: %w", err)
	}
	return s.GetPodcastFeed(ctx, id)
}

func (s *Service) ListDuePodcastFeeds(ctx context.Context, now time.Time, limit int) ([]PodcastFeed, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, podcastFeedSelectSQL+`
		WHERE poll_enabled = 1
		  AND (next_poll_at IS NULL OR next_poll_at <= ?)
		ORDER BY COALESCE(next_poll_at, '1970-01-01T00:00:00Z'), title COLLATE NOCASE
		LIMIT ?`, now.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("list due podcast feeds: %w", err)
	}
	defer rows.Close()

	var feeds []PodcastFeed
	for rows.Next() {
		feed, err := scanPodcastFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *Service) RunPodcastPollCycle(ctx context.Context, now time.Time) (PollCycleResult, error) {
	if s == nil || s.db == nil {
		return PollCycleResult{}, ErrDisabled
	}
	feeds, err := s.ListDuePodcastFeeds(ctx, now, 100)
	if err != nil {
		return PollCycleResult{}, err
	}

	result := PollCycleResult{Results: make([]PollFeedResult, 0, len(feeds))}
	for _, feed := range feeds {
		result.Checked++
		item := PollFeedResult{FeedID: feed.ID, Title: feed.Title}
		if !feed.Poll.Enabled {
			item.Skipped = true
			item.Status = PollStatusDisabled
			result.Skipped++
			result.Results = append(result.Results, item)
			continue
		}

		updated, err := s.pollRefreshFeed(ctx, feed.ID)
		if err != nil {
			item.Status = PollStatusError
			item.Error = err.Error()
			result.Failed++
		} else {
			item.Status = updated.Status
			result.Updated++
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

func (s *Service) pollRefreshFeed(ctx context.Context, id string) (PodcastFeed, error) {
	return s.RefreshPodcastFeed(ctx, id)
}

func (s *Service) markPollStarted(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET last_poll_started_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, strings.TrimSpace(id))
	return err
}

func (s *Service) markPollSuccess(ctx context.Context, id string, intervalSeconds int) error {
	next := time.Now().UTC().Add(time.Duration(intervalSeconds) * time.Second)
	_, err := s.db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET status = ?,
		    last_error = '',
		    consecutive_errors = 0,
		    last_poll_finished_at = CURRENT_TIMESTAMP,
		    next_poll_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		PollStatusOK,
		next.Format(time.RFC3339),
		strings.TrimSpace(id),
	)
	return err
}

func (s *Service) markPollFailure(ctx context.Context, id string, cause error) error {
	feed, err := s.GetPodcastFeed(ctx, id)
	if err != nil {
		return err
	}
	errorsCount := feed.Poll.ConsecutiveErrors + 1
	backoffSeconds := pollBackoffSeconds(feed.Poll.IntervalSeconds, errorsCount)
	next := time.Now().UTC().Add(time.Duration(backoffSeconds) * time.Second)
	message := ""
	if cause != nil {
		message = strings.TrimSpace(cause.Error())
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET status = ?,
		    last_error = ?,
		    consecutive_errors = ?,
		    last_poll_finished_at = CURRENT_TIMESTAMP,
		    next_poll_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		PollStatusError,
		message,
		errorsCount,
		next.Format(time.RFC3339),
		strings.TrimSpace(id),
	)
	return err
}

func pollBackoffSeconds(intervalSeconds, consecutiveErrors int) int {
	if consecutiveErrors <= 0 {
		consecutiveErrors = 1
	}
	backoff := MinPollIntervalSeconds * consecutiveErrors
	if backoff > intervalSeconds {
		backoff = intervalSeconds
	}
	if backoff > 6*3600 {
		backoff = 6 * 3600
	}
	return backoff
}

func timeStringOrNull(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func scheduleInitialPoll() any {
	return time.Now().UTC().Format(time.RFC3339)
}

type Poller struct {
	sources       *Service
	reloadCatalog func(context.Context) error
	tick          time.Duration
	logger        func(string, ...any)
	mu            sync.Mutex
}

type PollerOptions struct {
	Sources       *Service
	ReloadCatalog func(context.Context) error
	Tick          time.Duration
	Logger        func(string, ...any)
}

func NewPoller(options PollerOptions) *Poller {
	tick := options.Tick
	if tick <= 0 {
		tick = time.Minute
	}
	logger := options.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &Poller{
		sources:       options.Sources,
		reloadCatalog: options.ReloadCatalog,
		tick:          tick,
		logger:        logger,
	}
}

func (p *Poller) Run(ctx context.Context) error {
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

func (p *Poller) runOnce(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := p.sources.RunPodcastPollCycle(ctx, time.Now().UTC())
	if err != nil {
		p.logger("podcast poll cycle failed: %v", err)
		return
	}
	if result.Updated == 0 && result.Failed == 0 {
		return
	}
	if p.reloadCatalog != nil {
		if err := p.reloadCatalog(ctx); err != nil {
			p.logger("catalog refresh failed after podcast poll: %v", err)
		}
	}
	p.logger("podcast poll cycle: checked=%d updated=%d failed=%d skipped=%d",
		result.Checked, result.Updated, result.Failed, result.Skipped)
}

package lastfm

import (
	"context"
	"time"
)

type Poller struct {
	service *Service
	tick    time.Duration
	limit   int
	logger  func(format string, args ...any)
}

type PollerOptions struct {
	Service *Service
	Tick    time.Duration
	Limit   int
	Logger  func(format string, args ...any)
}

func NewPoller(options PollerOptions) *Poller {
	tick := options.Tick
	if tick <= 0 {
		tick = time.Minute
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 25
	}
	return &Poller{
		service: options.Service,
		tick:    tick,
		limit:   limit,
		logger:  options.Logger,
	}
}

func (p *Poller) Run(ctx context.Context) error {
	if p == nil || p.service == nil || !p.service.Enabled() {
		return nil
	}
	ticker := time.NewTicker(p.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			flushed, err := p.service.FlushQueue(ctx, "", p.limit)
			if err != nil && p.logger != nil {
				p.logger("last.fm queue flush failed: %v", err)
				continue
			}
			if flushed > 0 && p.logger != nil {
				p.logger("last.fm flushed %d queued submission(s)", flushed)
			}
		}
	}
}

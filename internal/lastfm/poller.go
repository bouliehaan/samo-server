package lastfm

import (
	"context"
	"time"
)

// Poller drains the durable submission queue in the background.
//
// It deliberately does not decide at startup whether Last.fm is configured:
// credentials can be saved through the UI at any time, and the old poller
// exited immediately when they were absent at boot, leaving every failed
// scrobble stranded until the next restart.
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
		limit = 50
	}
	return &Poller{
		service: options.Service,
		tick:    tick,
		limit:   limit,
		logger:  options.Logger,
	}
}

func (p *Poller) Run(ctx context.Context) error {
	if p == nil || p.service == nil {
		return nil
	}
	// Deliver anything a previous run left behind before settling into the
	// tick, so a restart mid-outage recovers in seconds rather than a minute.
	startup := time.NewTimer(5 * time.Second)
	defer startup.Stop()

	ticker := time.NewTicker(p.tick)
	defer ticker.Stop()

	prune := time.NewTicker(6 * time.Hour)
	defer prune.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-startup.C:
			p.drain(ctx)
		case <-ticker.C:
			p.drain(ctx)
		case <-prune.C:
			if err := p.service.PrunePlays(ctx, 30*24*time.Hour); err != nil {
				p.log("last.fm play prune failed: %v", err)
			}
		}
	}
}

func (p *Poller) drain(ctx context.Context) {
	if !p.service.Enabled() {
		return
	}
	flushed, err := p.service.DrainQueue(ctx, "", p.limit, 20)
	if err != nil && ctx.Err() == nil {
		p.log("last.fm queue flush failed: %v", err)
	}
	if flushed > 0 {
		p.log("last.fm delivered %d queued submission(s)", flushed)
	}
}

func (p *Poller) log(format string, args ...any) {
	if p.logger != nil {
		p.logger(format, args...)
	}
}

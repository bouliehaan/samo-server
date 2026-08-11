package loudness

import (
	"context"
	"time"
)

const (
	// backfillBatch is how many files are claimed per query.
	backfillBatch = 50

	// backfillPause is the gap between files. It exists to keep the sweep a
	// background citizen: with one analysis slot, pausing between items is
	// what lets an on-demand warm — a track about to air — get in front of a
	// sweep that has thousands of files to get through.
	backfillPause = 2 * time.Second

	// backfillIdle is how long to wait before re-checking once everything is
	// measured. New files arrive from library scans, not continuously.
	backfillIdle = 30 * time.Minute
)

// Backfill measures the library in the background so the radio is levelled
// from the first airing rather than the second.
//
// Warming an item during the one before it (what the channel streamer does)
// already covers the common case, but it only helps things the station is
// about to play. A phone that sends six tracks to the aux port wants them
// level NOW, and the gain has to be attached to the payload before playback
// starts — there is no second chance for that queue. Sweeping the library
// ahead of time is what turns every one of those into a cache hit.
//
// It is deliberately slow. There is no deadline here: getting through the
// library over a few hours while the server does its real job is entirely
// fine, and is much better than a burst of analysis that makes the box audibly
// struggle the first time anyone starts it.
type Backfill struct {
	Service *Service

	// Pause overrides the gap between files. Zero uses backfillPause.
	Pause time.Duration
}

// Run sweeps until the context is cancelled. Intended to be launched once at
// startup and left alone.
func (b Backfill) Run(ctx context.Context) {
	if b.Service == nil || !b.Service.Enabled() {
		return
	}
	if !b.Service.meterReady() {
		// meterReady has already said why. Sweeping would write nothing but
		// failure rows against files that are perfectly fine.
		return
	}
	pause := b.Pause
	if pause <= 0 {
		pause = backfillPause
	}
	logger := b.Service.logger

	// idle suppresses the "nothing left" line after the first time, so a
	// fully-measured library does not print the same thing every half hour.
	idle := false

	for ctx.Err() == nil {
		pending, err := b.pending(ctx)
		if err != nil {
			logger.Printf("loudness: backfill query failed: %v", err)
			if !sleep(ctx, backfillIdle) {
				return
			}
			continue
		}
		if len(pending) == 0 {
			if !idle {
				logger.Printf("loudness: library fully measured; sweeping again every %s for new files", backfillIdle)
				idle = true
			}
			if !sleep(ctx, backfillIdle) {
				return
			}
			continue
		}
		idle = false
		// The batch size says nothing about the scale of the job, and "how
		// long until this finishes" is the first question anyone watching the
		// log will have.
		if remaining, err := b.remaining(ctx); err == nil {
			logger.Printf("loudness: measuring %d file(s); %d still unmeasured (about %s to go at this rate)",
				len(pending), remaining, estimate(remaining, pause))
		} else {
			logger.Printf("loudness: measuring %d unmeasured file(s)", len(pending))
		}
		for _, item := range pending {
			if ctx.Err() != nil {
				return
			}
			// Errors are already logged and cached as failures by Measure; the
			// sweep only cares about moving on.
			_, _ = b.Service.Measure(ctx, RequestFor(item.path, item.duration, false))
			if !sleep(ctx, pause) {
				return
			}
		}
	}
}

type pendingFile struct {
	path     string
	duration int
}

// remaining counts everything still unmeasured, for the progress line.
func (b Backfill) remaining(ctx context.Context) (int, error) {
	var count int
	err := b.Service.store.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM media_files f
		WHERE f.missing = 0
		  AND f.path <> ''
		  AND f.duration_seconds >= ?
		  AND NOT EXISTS (
			SELECT 1 FROM loudness_measurements m
			WHERE m.cache_key = 'file:' || f.path
		  )`, minMeasurableSeconds).Scan(&count)
	return count, err
}

// estimate turns a backlog into something human, assuming each file costs the
// pause plus a couple of seconds of analysis. Rough on purpose — it exists to
// answer "overnight or next week", not to be accurate.
func estimate(remaining int, pause time.Duration) time.Duration {
	per := pause + 2*time.Second
	return (time.Duration(remaining) * per).Round(time.Minute)
}

// pending finds audio files with no measurement row at all.
//
// A row is enough to exclude a file, failures included: a failure row carries
// its own cooldown, and a sweep that retried permanently-unreadable files
// every pass would never reach the end of a library that has any.
//
// The key is built in SQL to match Request.key exactly. media_files.path is
// absolute (the scanner stores it that way), so the two agree.
func (b Backfill) pending(ctx context.Context) ([]pendingFile, error) {
	rows, err := b.Service.store.db.QueryContext(ctx, `
		SELECT f.path, f.duration_seconds
		FROM media_files f
		WHERE f.missing = 0
		  AND f.path <> ''
		  AND f.duration_seconds >= ?
		  AND NOT EXISTS (
			SELECT 1 FROM loudness_measurements m
			WHERE m.cache_key = 'file:' || f.path
		  )
		ORDER BY f.duration_seconds ASC, f.path
		LIMIT ?`, minMeasurableSeconds, backfillBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pending []pendingFile
	for rows.Next() {
		var item pendingFile
		if err := rows.Scan(&item.path, &item.duration); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

// sleep waits, reporting false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

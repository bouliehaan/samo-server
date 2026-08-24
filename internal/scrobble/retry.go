package scrobble

import (
	"math/rand/v2"
	"time"
)

// RetryDelay is the geometric backoff for a transient delivery failure: base,
// 2×base, 4×base, ... capped at max, with jitter so a queue that failed
// together does not retry together.
//
// The schedule matters as much as the queue itself. An early version allowed
// eight attempts one minute apart and then discarded the scrobble, so a
// nine-minute network outage silently deleted every listen in it.
func RetryDelay(attempts int, base, max time.Duration) time.Duration {
	if base <= 0 {
		base = 30 * time.Second
	}
	if max < base {
		max = base
	}
	if attempts < 1 {
		attempts = 1
	}
	delay := base
	for i := 1; i < attempts && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	if delay <= 0 {
		return delay
	}
	return delay + time.Duration(rand.Int64N(int64(delay/4)))
}

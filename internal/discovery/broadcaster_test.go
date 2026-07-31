package discovery

import (
	"testing"
	"time"
)

func TestProbeLimiterAllowsOnePerInterval(t *testing.T) {
	limiter := newProbeLimiter(time.Second, 16)
	start := time.Unix(1_700_000_000, 0)

	if !limiter.allow("192.0.2.10", start) {
		t.Fatal("first probe should be answered")
	}
	if limiter.allow("192.0.2.10", start.Add(200*time.Millisecond)) {
		t.Fatal("second probe inside the interval should be dropped")
	}
	if !limiter.allow("192.0.2.10", start.Add(1500*time.Millisecond)) {
		t.Fatal("probe after the interval should be answered again")
	}
}

func TestProbeLimiterIsPerAddress(t *testing.T) {
	limiter := newProbeLimiter(time.Second, 16)
	now := time.Unix(1_700_000_000, 0)

	if !limiter.allow("192.0.2.10", now) {
		t.Fatal("first address should be answered")
	}
	if !limiter.allow("192.0.2.11", now) {
		t.Fatal("a different address must not be throttled by the first")
	}
}

// A flood of forged source addresses must not grow the table without bound —
// that would turn the rate limiter itself into the memory exhaustion it exists
// to prevent.
func TestProbeLimiterBoundsTableSize(t *testing.T) {
	const maxKeys = 64
	limiter := newProbeLimiter(time.Second, maxKeys)
	now := time.Unix(1_700_000_000, 0)

	for i := 0; i < maxKeys*20; i++ {
		limiter.allow(spoofedAddr(i), now)
		if len(limiter.lastSeen) > maxKeys {
			t.Fatalf("table grew past the cap: %d entries after %d probes", len(limiter.lastSeen), i+1)
		}
	}
}

func spoofedAddr(i int) string {
	return "198.51.100." + itoa(i%256) + ":" + itoa(i)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
